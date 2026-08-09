// Package natswebgateway implements the NATS Web Gateway Caddy HTTP handler.
package natswebgateway

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
	"github.com/caddyserver/caddy/v2/caddyconfig/httpcaddyfile"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp"
	internalroutes "github.com/davidoram/nats-web-gateway/internal/routes"
	"github.com/davidoram/nats-web-gateway/internal/translation"
	"github.com/nats-io/nats.go"
	"golang.org/x/net/http/httpguts"
)

func init() {
	caddy.RegisterModule(Handler{})
	httpcaddyfile.RegisterHandlerDirective("nats_web_gateway", parseCaddyfile)
	httpcaddyfile.RegisterDirectiveOrder("nats_web_gateway", httpcaddyfile.Before, "respond")
}

// Handler is the NATS Web Gateway Caddy HTTP middleware.
//
// Routes are validated before Caddy begins serving. HTTP-to-NATS execution is
// introduced by later tasks.
type Handler struct {
	NATS   NATSConnection `json:"nats"`
	Routes []Route        `json:"routes"`

	lifecycle      *connectionLifecycle
	compiledRoutes []compiledRoute
	connect        connectFunc
}

type natsConnection interface {
	IsConnected() bool
	RequestMsgWithContext(context.Context, *nats.Msg) (*nats.Msg, error)
	Drain() error
	Close()
}

type compiledRoute struct {
	config Route
	route  internalroutes.Route
}

type connectFunc func(string, ...nats.Option) (natsConnection, error)

type connectionLifecycle struct {
	connection natsConnection
	stateMu    sync.RWMutex
	ready      bool
	stopping   bool
	closed     chan struct{}
	closedOnce sync.Once
	cleanup    sync.Once
	drainWait  time.Duration
	cleanupErr error
}

func (lifecycle *connectionLifecycle) setReady(ready bool) {
	lifecycle.stateMu.Lock()
	defer lifecycle.stateMu.Unlock()
	if ready && lifecycle.stopping {
		return
	}
	lifecycle.ready = ready
}

func (lifecycle *connectionLifecycle) beginStopping() {
	lifecycle.stateMu.Lock()
	lifecycle.stopping = true
	lifecycle.ready = false
	lifecycle.stateMu.Unlock()
}

func (lifecycle *connectionLifecycle) isReady() bool {
	lifecycle.stateMu.RLock()
	defer lifecycle.stateMu.RUnlock()
	return lifecycle.ready
}

// CaddyModule returns the Caddy module information for Handler.
func (Handler) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{
		ID:  "http.handlers.nats_web_gateway",
		New: func() caddy.Module { return new(Handler) },
	}
}

// Validate rejects unsafe, incomplete, or ambiguous route configuration.
func (h Handler) Validate() error {
	if err := h.NATS.validate(); err != nil {
		return err
	}
	return validateRoutes(h.Routes)
}

// Provision establishes this handler instance's operator connection. A failed
// initial connection fails closed so Caddy never serves a configuration whose
// NATS dependency or credentials were invalid at load time.
func (h *Handler) Provision(ctx caddy.Context) error {
	if err := h.Validate(); err != nil {
		return err
	}
	replacer := caddy.NewReplacer()
	username, password := "", ""
	if h.NATS.Username != "" {
		var err error
		username, err = replacer.ReplaceOrErr(h.NATS.Username, true, true)
		if err != nil {
			return fmt.Errorf("resolve NATS username: %w", err)
		}
		password, err = replacer.ReplaceOrErr(h.NATS.Password, true, true)
		if err != nil {
			return fmt.Errorf("resolve NATS password: %w", err)
		}
	}
	connector := h.connect
	if connector == nil {
		connector = func(url string, options ...nats.Option) (natsConnection, error) {
			return nats.Connect(url, options...)
		}
	}
	lifecycle := &connectionLifecycle{
		closed:    make(chan struct{}),
		drainWait: time.Duration(h.NATS.DrainTimeout),
	}
	options := []nats.Option{
		nats.Name("nats-web-gateway"),
		nats.Timeout(time.Duration(h.NATS.ConnectTimeout)),
		nats.ReconnectWait(time.Duration(h.NATS.ReconnectWait)),
		nats.MaxReconnects(h.NATS.MaxReconnects),
		nats.DrainTimeout(time.Duration(h.NATS.DrainTimeout)),
		nats.DisconnectErrHandler(func(_ *nats.Conn, _ error) { lifecycle.setReady(false) }),
		nats.ReconnectHandler(func(_ *nats.Conn) { lifecycle.setReady(true) }),
		nats.ClosedHandler(func(_ *nats.Conn) {
			lifecycle.setReady(false)
			lifecycle.closedOnce.Do(func() { close(lifecycle.closed) })
		}),
	}
	if username != "" {
		options = append(options, nats.UserInfo(username, password))
	}
	connection, err := connector(strings.Join(h.NATS.URLs, ","), options...)
	if err != nil {
		return fmt.Errorf("connect to NATS: %w", err)
	}
	lifecycle.connection = connection
	h.compiledRoutes = make([]compiledRoute, 0, len(h.Routes))
	for _, configured := range h.Routes {
		parameters := make(map[string]internalroutes.Parameter, len(configured.Parameters))
		for name, parameter := range configured.Parameters {
			parameters[name] = internalroutes.Parameter{Source: parameter.Source, Name: parameter.Name, Pattern: parameter.Pattern}
		}
		compiled, compileErr := internalroutes.Compile(configured.Path, configured.Subject, configured.Methods, parameters)
		if compileErr != nil {
			connection.Close()
			return fmt.Errorf("compile route %q: %w", configured.Name, compileErr)
		}
		h.compiledRoutes = append(h.compiledRoutes, compiledRoute{config: configured, route: compiled})
	}
	lifecycle.setReady(connection.IsConnected())
	h.lifecycle = lifecycle
	return nil
}

// Ready reports whether this instance currently has a usable NATS connection.
// It is deliberately separate from process liveness.
func (h *Handler) Ready() bool {
	return h != nil && h.lifecycle != nil && h.lifecycle.isReady()
}

// Cleanup drains and closes only this handler instance's connection. This
// permits old and new Caddy configurations to overlap safely during reloads.
func (h *Handler) Cleanup() error {
	if h == nil || h.lifecycle == nil {
		return nil
	}
	h.lifecycle.cleanup.Do(func() {
		h.lifecycle.beginStopping()
		err := h.lifecycle.connection.Drain()
		if errors.Is(err, nats.ErrConnectionClosed) {
			return
		}
		if errors.Is(err, nats.ErrConnectionReconnecting) {
			h.lifecycle.connection.Close()
			return
		}
		if err != nil {
			h.lifecycle.cleanupErr = fmt.Errorf("drain NATS connection: %w", err)
			h.lifecycle.connection.Close()
			return
		}
		timer := time.NewTimer(h.lifecycle.drainWait)
		defer timer.Stop()
		select {
		case <-h.lifecycle.closed:
		case <-timer.C:
			h.lifecycle.connection.Close()
			h.lifecycle.cleanupErr = errors.New("drain NATS connection: timed out")
		}
	})
	return h.lifecycle.cleanupErr
}

func parseCaddyfile(helper httpcaddyfile.Helper) (caddyhttp.MiddlewareHandler, error) {
	var handler Handler
	if err := handler.UnmarshalCaddyfile(helper.Dispenser); err != nil {
		return nil, err
	}
	return &handler, nil
}

// ServeHTTP executes the first matching declared request/reply route. Requests
// outside this handler's declared surface continue through the Caddy chain.
func (h Handler) ServeHTTP(w http.ResponseWriter, r *http.Request, next caddyhttp.Handler) error {
	for _, candidate := range h.compiledRoutes {
		if candidate.config.StreamMode != streamModeRequestReply {
			continue
		}
		subject, matched, err := candidate.route.Match(r.Method, r.URL.EscapedPath(), r.URL.Query())
		if !matched {
			continue
		}
		if err != nil {
			writeGatewayError(w, http.StatusBadRequest, "invalid request parameters")
			return nil
		}
		if h.lifecycle == nil || !h.Ready() {
			writeGatewayError(w, http.StatusServiceUnavailable, "service unavailable")
			return nil
		}
		if candidate.config.Response.NegotiateAccept {
			w.Header().Add("Vary", "Accept")
		}
		representation, err := selectRepresentation(strings.Join(r.Header.Values("Accept"), ","), candidate.config.Response)
		if err != nil {
			writeGatewayError(w, http.StatusNotAcceptable, "no acceptable representation")
			return nil
		}
		requestHeaders := r.Header.Clone()
		forwardHeaders := candidate.config.RequestHeaders
		if candidate.config.Response.NegotiateAccept {
			requestHeaders.Set("Accept", representation)
			if !slices.Contains(forwardHeaders, "Accept") {
				forwardHeaders = append(slices.Clone(forwardHeaders), "Accept")
			}
		}
		ctx, cancel := context.WithTimeout(r.Context(), time.Duration(candidate.config.Timeout))
		reply, err := translation.Execute(ctx, h.lifecycle.connection, translation.Request{
			Subject: subject, Header: requestHeaders, Body: r.Body,
		}, forwardHeaders, candidate.config.MaxRequestBodyBytes, candidate.config.MaxReplyBytes)
		cancel()
		if err != nil {
			switch {
			case errors.Is(err, translation.ErrRequestTooLarge):
				writeGatewayError(w, http.StatusRequestEntityTooLarge, "request body too large")
			case errors.Is(err, translation.ErrReplyTooLarge):
				writeGatewayError(w, http.StatusBadGateway, "upstream reply too large")
			case errors.Is(err, translation.ErrMalformedReply):
				writeGatewayError(w, http.StatusBadGateway, "malformed upstream reply")
			case errors.Is(err, context.Canceled):
				return nil
			case errors.Is(err, context.DeadlineExceeded), errors.Is(err, nats.ErrTimeout):
				writeGatewayError(w, http.StatusGatewayTimeout, "upstream request timed out")
			case errors.Is(err, nats.ErrNoResponders):
				writeGatewayError(w, http.StatusServiceUnavailable, "service unavailable")
			case errors.Is(err, nats.ErrPermissionViolation):
				writeGatewayError(w, http.StatusForbidden, "forbidden")
			case errors.Is(err, nats.ErrAuthorization):
				writeGatewayError(w, http.StatusUnauthorized, "unauthorized")
			case errors.Is(err, nats.ErrConnectionClosed), errors.Is(err, nats.ErrDisconnected), errors.Is(err, nats.ErrConnectionReconnecting):
				writeGatewayError(w, http.StatusServiceUnavailable, "service unavailable")
			default:
				writeGatewayError(w, http.StatusInternalServerError, "internal gateway error")
			}
			return nil
		}
		status, serviceMessage, err := validateReply(reply, candidate.config.Response, representation)
		if err != nil {
			writeGatewayError(w, http.StatusBadGateway, "malformed upstream reply")
			return nil
		}
		if serviceMessage != "" {
			writeGatewayError(w, status, serviceMessage)
			return nil
		}
		responseHeaders, err := safeResponseHeaders(reply.Header, candidate.config.Response.Headers)
		if err != nil {
			writeGatewayError(w, http.StatusBadGateway, "malformed upstream reply")
			return nil
		}
		for name, values := range responseHeaders {
			for _, value := range values {
				w.Header().Add(name, value)
			}
		}
		w.Header().Set("Content-Type", representation)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		_, err = w.Write(reply.Data)
		return err
	}
	return next.ServeHTTP(w, r)
}

func safeResponseHeaders(source nats.Header, allowlist []string) (http.Header, error) {
	result := make(http.Header, len(allowlist))
	for _, name := range allowlist {
		for _, value := range source.Values(name) {
			if !httpguts.ValidHeaderFieldValue(value) {
				return nil, fmt.Errorf("invalid upstream header %q", name)
			}
			result.Add(name, value)
		}
	}
	return result, nil
}

var (
	_ caddy.Module                = (*Handler)(nil)
	_ caddy.Provisioner           = (*Handler)(nil)
	_ caddy.Validator             = (*Handler)(nil)
	_ caddy.CleanerUpper          = (*Handler)(nil)
	_ caddyfile.Unmarshaler       = (*Handler)(nil)
	_ caddyhttp.MiddlewareHandler = (*Handler)(nil)
)
