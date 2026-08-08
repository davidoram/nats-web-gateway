package natswebgateway

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig"
	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
)

func TestHandlerValidateAcceptsExplicitRoute(t *testing.T) {
	t.Parallel()

	handler := Handler{Routes: []Route{validRoute("get_order", "/orders/{id}", "orders.{id}", "GET")}}
	if err := handler.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestHandlerValidateRejectsUnsafeConfiguration(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		change func(*Handler)
		want   string
	}{
		{name: "no routes", change: func(h *Handler) { h.Routes = nil }, want: "at least one route"},
		{name: "bad name", change: func(h *Handler) { h.Routes[0].Name = "4 route" }, want: "name must"},
		{name: "relative path", change: func(h *Handler) { h.Routes[0].Path = "orders/{id}" }, want: "absolute path"},
		{name: "partial path parameter", change: func(h *Handler) { h.Routes[0].Path = "/orders/order-{id}" }, want: "complete segment"},
		{name: "path wildcard", change: func(h *Handler) { h.Routes[0].Path = "/orders/*" }, want: "without wildcards"},
		{name: "no methods", change: func(h *Handler) { h.Routes[0].Methods = nil }, want: "at least one method"},
		{name: "lowercase method", change: func(h *Handler) { h.Routes[0].Methods = []string{"get"} }, want: "invalid HTTP method"},
		{name: "duplicate method", change: func(h *Handler) { h.Routes[0].Methods = []string{"GET", "GET"} }, want: "duplicate HTTP method"},
		{name: "subject wildcard", change: func(h *Handler) { h.Routes[0].Subject = "orders.*" }, want: "wildcards"},
		{name: "partial subject parameter", change: func(h *Handler) { h.Routes[0].Subject = "orders.order-{id}" }, want: "complete token"},
		{name: "missing parameter grammar", change: func(h *Handler) { h.Routes[0].Parameters = nil }, want: "explicit source and validation expression"},
		{name: "unknown parameter source", change: func(h *Handler) {
			p := h.Routes[0].Parameters["id"]
			p.Source = "header"
			h.Routes[0].Parameters["id"] = p
		}, want: "unsupported source"},
		{name: "path parameter uses query", change: func(h *Handler) {
			p := h.Routes[0].Parameters["id"]
			p.Source = "query"
			h.Routes[0].Parameters["id"] = p
		}, want: "must use a path source"},
		{name: "invalid query name", change: func(h *Handler) {
			h.Routes[0].Path = "/orders"
			h.Routes[0].Parameters["id"] = Parameter{Source: "query", Name: "bad name", Pattern: `^[a-z]+$`}
		}, want: "invalid query source name"},
		{name: "unanchored parameter grammar", change: func(h *Handler) {
			p := h.Routes[0].Parameters["id"]
			p.Pattern = `[a-z]+`
			h.Routes[0].Parameters["id"] = p
		}, want: "explicitly anchored"},
		{name: "unsafe parameter grammar", change: func(h *Handler) {
			p := h.Routes[0].Parameters["id"]
			p.Pattern = `^.+$`
			h.Routes[0].Parameters["id"] = p
		}, want: "unsupported regexp operation"},
		{name: "long wildcard bypass", change: func(h *Handler) {
			p := h.Routes[0].Parameters["id"]
			p.Pattern = `^[A-Za-z.*>]{5,}$`
			h.Routes[0].Parameters["id"] = p
		}, want: "unsafe character"},
		{name: "empty parameter grammar", change: func(h *Handler) {
			p := h.Routes[0].Parameters["id"]
			p.Pattern = `^$`
			h.Routes[0].Parameters["id"] = p
		}, want: "must not match an empty value"},
		{name: "unused parameter grammar", change: func(h *Handler) {
			h.Routes[0].Parameters["other"] = Parameter{Source: "query", Name: "other", Pattern: `^[a-z]+$`}
		}, want: "not used"},
		{name: "credential header", change: func(h *Handler) { h.Routes[0].RequestHeaders = []string{"Authorization"} }, want: "forbidden header"},
		{name: "hop by hop header", change: func(h *Handler) { h.Routes[0].Response.Headers = []string{"Connection"} }, want: "forbidden header"},
		{name: "identity header", change: func(h *Handler) { h.Routes[0].RequestHeaders = []string{"X-Tenant-Id"} }, want: "forbidden header"},
		{name: "exact authenticated header", change: func(h *Handler) { h.Routes[0].RequestHeaders = []string{"X-Authenticated"} }, want: "forbidden header"},
		{name: "exact tenant header", change: func(h *Handler) { h.Routes[0].RequestHeaders = []string{"X-Tenant"} }, want: "forbidden header"},
		{name: "exact user header", change: func(h *Handler) { h.Routes[0].RequestHeaders = []string{"X-User"} }, want: "forbidden header"},
		{name: "noncanonical header", change: func(h *Handler) { h.Routes[0].RequestHeaders = []string{"content-type"} }, want: "non-canonical"},
		{name: "zero timeout", change: func(h *Handler) { h.Routes[0].Timeout = 0 }, want: "timeout"},
		{name: "zero request limit", change: func(h *Handler) { h.Routes[0].MaxRequestBodyBytes = 0 }, want: "max_request_body_bytes"},
		{name: "zero reply limit", change: func(h *Handler) { h.Routes[0].MaxReplyBytes = 0 }, want: "max_reply_bytes"},
		{name: "unknown stream mode", change: func(h *Handler) { h.Routes[0].StreamMode = "stream" }, want: "unsupported stream_mode"},
		{name: "unknown response mode", change: func(h *Handler) { h.Routes[0].Response.Mode = "passthrough" }, want: "unsupported response mode"},
		{name: "header injection content type", change: func(h *Handler) { h.Routes[0].Response.ContentType = "text/plain\r\nX-Evil: true" }, want: "content_type"},
		{name: "bad service code", change: func(h *Handler) { h.Routes[0].Response.ServiceErrorStatuses = map[string]int{"04": 400} }, want: "invalid ADR-32 code"},
		{name: "bad service status", change: func(h *Handler) { h.Routes[0].Response.ServiceErrorStatuses = map[string]int{"4001": 200} }, want: "between 400 and 599"},
		{name: "duplicate name", change: func(h *Handler) {
			h.Routes = append(h.Routes, validRoute("get_order", "/other/{id}", "other.{id}", "GET"))
		}, want: "duplicate name"},
		{name: "overlapping literal and parameter paths", change: func(h *Handler) {
			h.Routes = append(h.Routes, validRoute("special_order", "/orders/special", "orders.special", "GET"))
		}, want: "overlapping paths and methods"},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			handler := Handler{Routes: []Route{validRoute("get_order", "/orders/{id}", "orders.{id}", "GET")}}
			test.change(&handler)
			err := handler.Validate()
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Validate() error = %v, want error containing %q", err, test.want)
			}
		})
	}
}

func TestHandlerValidateAllowsNonOverlappingRoutes(t *testing.T) {
	t.Parallel()

	tests := []Route{
		validRoute("post_order", "/orders/{id}", "orders.create.{id}", "POST"),
		validRoute("get_nested", "/orders/{id}/lines", "orders.lines.{id}", "GET"),
		validRoute("get_health", "/health", "health", "GET"),
	}
	handler := Handler{Routes: append([]Route{validRoute("get_order", "/orders/{id}", "orders.{id}", "GET")}, tests...)}
	if err := handler.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestHandlerValidateAcceptsQuerySubjectParameter(t *testing.T) {
	t.Parallel()

	route := validRoute("search_orders", "/orders", "orders.search.{term}", "GET")
	route.Parameters["term"] = Parameter{Source: "query", Name: "q", Pattern: `^[A-Za-z0-9_-]+$`}
	if err := (Handler{Routes: []Route{route}}).Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestJSONConfiguration(t *testing.T) {
	t.Parallel()

	input := `{
  "routes": [{
    "name": "get_order",
    "path": "/orders/{id}",
    "methods": ["GET"],
    "subject": "orders.{id}",
	    "parameters": {"id": {"source": "path", "name": "id", "pattern": "^[A-Za-z0-9_-]+$"}},
    "request_headers": ["Content-Type", "Traceparent"],
    "timeout": "2s",
    "max_request_body_bytes": 1048576,
    "max_reply_bytes": 1048576,
    "response": {
      "mode": "raw",
      "headers": ["Content-Type"],
      "content_type": "application/octet-stream",
      "service_error_statuses": {"4001": 400}
    },
    "stream_mode": "request_reply"
  }]
}`
	var handler Handler
	if err := json.Unmarshal([]byte(input), &handler); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if err := handler.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if got := time.Duration(handler.Routes[0].Timeout); got != 2*time.Second {
		t.Fatalf("timeout = %s, want 2s", got)
	}
}

func TestCaddyfileConfiguration(t *testing.T) {
	t.Parallel()

	input := `nats_web_gateway {
  route get_order {
    path /orders/{id}
    methods GET
    subject orders.{id}
	    parameter id path id ^[A-Za-z0-9_-]+$
    request_headers Content-Type Traceparent
    timeout 2s
    max_request_body_bytes 1048576
    max_reply_bytes 1048576
    response_mode raw
    response_headers Content-Type
    response_content_type application/octet-stream
    service_error_status 4001 400
    stream_mode request_reply
  }
}`
	var handler Handler
	if err := handler.UnmarshalCaddyfile(caddyfile.NewTestDispenser(input)); err != nil {
		t.Fatalf("UnmarshalCaddyfile() error = %v", err)
	}
	if len(handler.Routes) != 1 {
		t.Fatalf("routes = %d, want 1", len(handler.Routes))
	}
	if got := handler.Routes[0].Subject; got != "orders.{id}" {
		t.Fatalf("subject = %q, want orders.{id}", got)
	}
}

func TestCaddyfileConfigurationRejectsUnknownOption(t *testing.T) {
	t.Parallel()

	input := `nats_web_gateway {
  route get_order {
    arbitrary_subject true
  }
}`
	var handler Handler
	err := handler.UnmarshalCaddyfile(caddyfile.NewTestDispenser(input))
	if err == nil || !strings.Contains(err.Error(), "unrecognized route option") {
		t.Fatalf("UnmarshalCaddyfile() error = %v, want unknown option error", err)
	}
}

func TestCaddyfileConfigurationRejectsDuplicateOption(t *testing.T) {
	t.Parallel()

	input := `nats_web_gateway {
  route get_order {
    path /orders
    path /other
  }
}`
	var handler Handler
	err := handler.UnmarshalCaddyfile(caddyfile.NewTestDispenser(input))
	if err == nil || !strings.Contains(err.Error(), "already specified") {
		t.Fatalf("UnmarshalCaddyfile() error = %v, want duplicate option error", err)
	}
}

func TestCaddyfileAdapterRegistration(t *testing.T) {
	t.Parallel()

	input := `:8080 {
  nats_web_gateway {
    route health {
      path /health
      methods GET
      subject health
      timeout 1s
      max_request_body_bytes 1024
      max_reply_bytes 1024
      response_mode raw
      stream_mode request_reply
    }
  }
}`
	adapter := caddyconfig.GetAdapter("caddyfile")
	if adapter == nil {
		t.Fatal("Caddyfile adapter is not registered")
	}
	adapted, _, err := adapter.Adapt([]byte(input), nil)
	if err != nil {
		t.Fatalf("Adapt() error = %v", err)
	}
	if !strings.Contains(string(adapted), `"handler":"nats_web_gateway"`) {
		t.Fatalf("adapted JSON does not contain gateway handler: %s", adapted)
	}
}

func FuzzValidateTemplates(f *testing.F) {
	for _, seed := range []string{"/", "/orders/{id}", "orders.{id}", "{", "*", "a..b"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, value string) {
		_, _ = validatePath(value)
		_, _ = validateSubject(value)
	})
}

func validRoute(name, path, subject, method string) Route {
	parameters := map[string]Parameter{}
	for _, match := range placeholderPattern.FindAllStringSubmatch(path+subject, -1) {
		parameters[match[1]] = Parameter{Source: "path", Name: match[1], Pattern: `^[A-Za-z0-9_-]+$`}
	}
	return Route{
		Name:                name,
		Path:                path,
		Methods:             []string{method},
		Subject:             subject,
		Parameters:          parameters,
		RequestHeaders:      []string{"Content-Type", "Traceparent"},
		Timeout:             caddy.Duration(2 * time.Second),
		MaxRequestBodyBytes: 1 << 20,
		MaxReplyBytes:       1 << 20,
		Response: Response{
			Mode:                 responseModeRaw,
			Headers:              []string{"Content-Type"},
			ContentType:          "application/octet-stream",
			ServiceErrorStatuses: map[string]int{"4001": 400},
		},
		StreamMode: streamModeRequestReply,
	}
}
