// Package natswebgateway implements the NATS Web Gateway Caddy HTTP handler.
package natswebgateway

import (
	"net/http"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
	"github.com/caddyserver/caddy/v2/caddyconfig/httpcaddyfile"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp"
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
	Routes []Route `json:"routes"`
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
	return validateRoutes(h.Routes)
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
	_ caddy.Validator             = (*Handler)(nil)
	_ caddyfile.Unmarshaler       = (*Handler)(nil)
	_ caddyhttp.MiddlewareHandler = (*Handler)(nil)
)
