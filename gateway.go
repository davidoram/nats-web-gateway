// Package natswebgateway implements the NATS Web Gateway Caddy HTTP handler.
package natswebgateway

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
	"github.com/caddyserver/caddy/v2/caddyconfig/httpcaddyfile"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp"
	"github.com/nats-io/nats.go"
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

	lifecycle *connectionLifecycle
	connect   connectFunc
}

type natsConnection interface {
	IsConnected() bool
	Drain() error
	Close()
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
		if err := h.lifecycle.connection.Drain(); err != nil && !errors.Is(err, nats.ErrConnectionClosed) && !errors.Is(err, nats.ErrConnectionReconnecting) {
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

// ServeHTTP passes the request to the next handler until gateway routes are
// implemented. It preserves the request context and response writer unchanged.
func (Handler) ServeHTTP(w http.ResponseWriter, r *http.Request, next caddyhttp.Handler) error {
	return next.ServeHTTP(w, r)
}

var (
	_ caddy.Module                = (*Handler)(nil)
	_ caddy.Provisioner           = (*Handler)(nil)
	_ caddy.Validator             = (*Handler)(nil)
	_ caddy.CleanerUpper          = (*Handler)(nil)
	_ caddyfile.Unmarshaler       = (*Handler)(nil)
	_ caddyhttp.MiddlewareHandler = (*Handler)(nil)
)
