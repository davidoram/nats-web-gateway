// Package natswebgateway implements the NATS Web Gateway Caddy HTTP handler.
package natswebgateway

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
	"github.com/caddyserver/caddy/v2/caddyconfig/httpcaddyfile"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp"
	"github.com/davidoram/nats-web-gateway/internal/credentials"
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
	pool   *securityContextPool
}

type connectFunc func(string, ...nats.Option) (natsConnection, error)

type connectionLifecycle struct {
	connection  natsConnection
	permissions *permissionTracker
	stateMu     sync.RWMutex
	ready       bool
	stopping    bool
	closed      chan struct{}
	closedOnce  sync.Once
	cleanup     sync.Once
	drainWait   time.Duration
	cleanupErr  error
}

var publishPermissionPattern = regexp.MustCompile(`(?i)permissions violation for publish to "([^"\s]+)"`)

type permissionTracker struct {
	mu      sync.Mutex
	waiters map[string]map[*permissionWaiter]struct{}
}

type permissionWaiter struct {
	cancel context.CancelCauseFunc
}

type permissionAwareRequester struct {
	connection natsConnection
	tracker    *permissionTracker
	cancel     context.CancelCauseFunc
}

func (requester permissionAwareRequester) RequestMsgWithContext(ctx context.Context, message *nats.Msg) (*nats.Msg, error) {
	unregister := requester.tracker.register(message.Subject, requester.cancel)
	defer unregister()
	return requester.connection.RequestMsgWithContext(ctx, message)
}

func newPermissionTracker() *permissionTracker {
	return &permissionTracker{waiters: make(map[string]map[*permissionWaiter]struct{})}
}

func (tracker *permissionTracker) register(subject string, cancel context.CancelCauseFunc) func() {
	waiter := &permissionWaiter{cancel: cancel}
	tracker.mu.Lock()
	if tracker.waiters[subject] == nil {
		tracker.waiters[subject] = make(map[*permissionWaiter]struct{})
	}
	tracker.waiters[subject][waiter] = struct{}{}
	tracker.mu.Unlock()
	return func() {
		tracker.mu.Lock()
		delete(tracker.waiters[subject], waiter)
		if len(tracker.waiters[subject]) == 0 {
			delete(tracker.waiters, subject)
		}
		tracker.mu.Unlock()
	}
}

func (tracker *permissionTracker) handle(err error) {
	if !errors.Is(err, nats.ErrPermissionViolation) {
		return
	}
	match := publishPermissionPattern.FindStringSubmatch(err.Error())
	if len(match) != 2 {
		return
	}
	tracker.mu.Lock()
	waiters := make([]*permissionWaiter, 0, len(tracker.waiters[match[1]]))
	for waiter := range tracker.waiters[match[1]] {
		waiters = append(waiters, waiter)
	}
	tracker.mu.Unlock()
	for _, waiter := range waiters {
		waiter.cancel(nats.ErrPermissionViolation)
	}
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

func (lifecycle *connectionLifecycle) isServing() bool {
	lifecycle.stateMu.RLock()
	defer lifecycle.stateMu.RUnlock()
	return !lifecycle.stopping
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

// Provision compiles routes and establishes this handler instance's operator
// connection when at least one route uses it. Protected-only handlers connect
// lazily with each request's adapted security context.
func (h *Handler) Provision(ctx caddy.Context) error {
	if err := h.Validate(); err != nil {
		return err
	}
	connector := h.connect
	if connector == nil {
		connector = func(url string, options ...nats.Option) (natsConnection, error) {
			return nats.Connect(url, options...)
		}
	}
	lifecycle := &connectionLifecycle{
		closed:      make(chan struct{}),
		drainWait:   time.Duration(h.NATS.DrainTimeout),
		permissions: newPermissionTracker(),
	}
	hasOperatorRoute := false
	h.compiledRoutes = make([]compiledRoute, 0, len(h.Routes))
	for _, configured := range h.Routes {
		parameters := make(map[string]internalroutes.Parameter, len(configured.Parameters))
		for name, parameter := range configured.Parameters {
			parameters[name] = internalroutes.Parameter{Source: parameter.Source, Name: parameter.Name, Pattern: parameter.Pattern}
		}
		compiled, compileErr := internalroutes.Compile(configured.Path, configured.Subject, configured.Methods, parameters)
		if compileErr != nil {
			return fmt.Errorf("compile route %q: %w", configured.Name, compileErr)
		}
		candidate := compiledRoute{config: configured, route: compiled}
		if configured.SecurityContext != nil {
			base := []nats.Option{
				nats.Name("nats-web-gateway-protected"),
				nats.Timeout(time.Duration(h.NATS.ConnectTimeout)),
				nats.ReconnectWait(time.Duration(h.NATS.ReconnectWait)),
				nats.MaxReconnects(h.NATS.MaxReconnects),
				nats.DrainTimeout(time.Duration(h.NATS.DrainTimeout)),
			}
			candidate.pool = newSecurityContextPool(*configured.SecurityContext, strings.Join(h.NATS.URLs, ","), connector, base)
		} else {
			hasOperatorRoute = true
		}
		h.compiledRoutes = append(h.compiledRoutes, candidate)
	}
	if !hasOperatorRoute {
		lifecycle.setReady(true)
		h.lifecycle = lifecycle
		return nil
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
	options := []nats.Option{
		nats.Name("nats-web-gateway"), nats.Timeout(time.Duration(h.NATS.ConnectTimeout)),
		nats.ReconnectWait(time.Duration(h.NATS.ReconnectWait)), nats.MaxReconnects(h.NATS.MaxReconnects),
		nats.DrainTimeout(time.Duration(h.NATS.DrainTimeout)),
		nats.DisconnectErrHandler(func(_ *nats.Conn, _ error) { lifecycle.setReady(false) }),
		nats.ReconnectHandler(func(_ *nats.Conn) { lifecycle.setReady(true) }),
		nats.ClosedHandler(func(_ *nats.Conn) {
			lifecycle.setReady(false)
			lifecycle.closedOnce.Do(func() { close(lifecycle.closed) })
		}),
		nats.ErrorHandler(func(_ *nats.Conn, _ *nats.Subscription, err error) { lifecycle.permissions.handle(err) }),
	}
	if username != "" {
		options = append(options, nats.UserInfo(username, password))
	}
	connection, err := connector(strings.Join(h.NATS.URLs, ","), options...)
	if err != nil {
		return fmt.Errorf("connect to NATS: %w", err)
	}
	lifecycle.connection = connection
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
		for _, route := range h.compiledRoutes {
			if route.pool != nil {
				route.pool.close()
			}
		}
		if h.lifecycle.connection == nil {
			return
		}
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
		if h.lifecycle == nil || (candidate.pool == nil && !h.Ready()) || (candidate.pool != nil && !h.lifecycle.isServing()) {
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
		timeoutCtx, timeoutCancel := context.WithTimeout(r.Context(), time.Duration(candidate.config.Timeout))
		ctx, cancel := context.WithCancelCause(timeoutCtx)
		connection := h.lifecycle.connection
		tracker := h.lifecycle.permissions
		var release func()
		if candidate.pool != nil {
			adapted, adaptErr := (credentials.Adapter{
				Mechanism: candidate.config.SecurityContext.Mechanism, MaxCredentialBytes: candidate.config.SecurityContext.MaxCredentialBytes,
			}).Adapt(r)
			if adaptErr != nil {
				cancel(nil)
				timeoutCancel()
				writeGatewayError(w, http.StatusUnauthorized, "unauthorized")
				return nil
			}
			lease, acquireErr := candidate.pool.acquire(ctx, adapted)
			if acquireErr != nil {
				cancel(nil)
				timeoutCancel()
				if errors.Is(acquireErr, context.Canceled) {
					return nil
				} else if errors.Is(acquireErr, context.DeadlineExceeded) {
					writeGatewayError(w, http.StatusGatewayTimeout, "upstream request timed out")
				} else if errors.Is(acquireErr, nats.ErrAuthorization) || credentialFailure(acquireErr) {
					writeGatewayError(w, http.StatusUnauthorized, "unauthorized")
				} else {
					writeGatewayError(w, http.StatusServiceUnavailable, "service unavailable")
				}
				return nil
			}
			connection, tracker, release = lease.connection, lease.tracker, lease.release
			defer release()
		}
		requester := permissionAwareRequester{connection: connection, tracker: tracker, cancel: cancel}
		reply, err := translation.Execute(ctx, requester, translation.Request{
			Subject: subject, Header: requestHeaders, Body: r.Body,
		}, forwardHeaders, candidate.config.MaxRequestBodyBytes, candidate.config.MaxReplyBytes)
		cancel(nil)
		timeoutCancel()
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

func credentialFailure(err error) bool {
	return errors.Is(err, credentials.ErrCredentialMissing) || errors.Is(err, credentials.ErrCredentialMalformed) ||
		errors.Is(err, credentials.ErrCredentialAmbiguous) || errors.Is(err, credentials.ErrProofUnavailable)
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
