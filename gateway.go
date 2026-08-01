// Package natswebgateway implements the NATS Web Gateway Caddy HTTP handler.
package natswebgateway

import (
	"net/http"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp"
)

func init() {
	caddy.RegisterModule(Handler{})
}

// Handler is the NATS Web Gateway Caddy HTTP middleware.
//
// The initial scaffold deliberately passes requests to the next handler. Route
// configuration and HTTP-to-NATS behavior are introduced by later tasks.
type Handler struct{}

// CaddyModule returns the Caddy module information for Handler.
func (Handler) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{
		ID:  "http.handlers.nats_web_gateway",
		New: func() caddy.Module { return new(Handler) },
	}
}

// ServeHTTP passes the request to the next handler until gateway routes are
// implemented. It preserves the request context and response writer unchanged.
func (Handler) ServeHTTP(w http.ResponseWriter, r *http.Request, next caddyhttp.Handler) error {
	return next.ServeHTTP(w, r)
}

var (
	_ caddy.Module                = (*Handler)(nil)
	_ caddyhttp.MiddlewareHandler = (*Handler)(nil)
)
