package natswebgateway

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
	"github.com/davidoram/nats-web-gateway/internal/credentials"
	"github.com/nats-io/nats.go"
)

func TestRouteProfilesResolveReplacementClearingAndExtension(t *testing.T) {
	base := validRoute("", "", "orders.list", "GET")
	base.Name, base.Path, base.Methods = "", "", nil
	base.RequestHeaders = []string{"Content-Type"}
	h := validHandler(Route{Name: "list", Path: "/orders", Methods: []string{"GET"}, Profile: "api", RequestHeaders: []string{}, Extend: &RouteExtensions{RequestHeaders: []string{"Traceparent"}}})
	h.RouteProfiles = []RouteProfile{{Name: "api", Route: base}}
	routes, err := h.resolvedRoutes()
	if err != nil {
		t.Fatal(err)
	}
	if got := routes[0].RequestHeaders; len(got) != 1 || got[0] != "Traceparent" {
		t.Fatalf("request headers = %v", got)
	}
	if err := h.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestRouteProfilesRejectUnknownDuplicateCycleAndRouteIdentity(t *testing.T) {
	tests := []struct {
		name     string
		profiles []RouteProfile
		profile  string
		want     string
	}{
		{name: "unknown", profile: "missing", want: "unknown"},
		{name: "duplicate", profiles: []RouteProfile{{Name: "a"}, {Name: "a"}}, want: "duplicate"},
		{name: "cycle", profiles: []RouteProfile{{Name: "a", Extends: "b"}, {Name: "b", Extends: "a"}}, want: "cycle"},
		{name: "identity", profiles: []RouteProfile{{Name: "a", Route: Route{Path: "/hidden"}}}, want: "route-specific"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := validHandler(validRoute("r", "/r", "r", "GET"))
			h.RouteProfiles = tt.profiles
			h.Routes[0].Profile = tt.profile
			if err := h.Validate(); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestJSONRouteProfile(t *testing.T) {
	input := `{"nats":{"urls":["nats://127.0.0.1:4222"],"connect_timeout":"5s","reconnect_wait":"1s","max_reconnects":-1,"drain_timeout":"30s"},"route_profiles":[{"name":"api","subject":"orders.list","timeout":"2s","max_request_body_bytes":1024,"max_reply_bytes":1024,"response":{"mode":"binary","content_type":"application/octet-stream"},"stream_mode":"request_reply"}],"routes":[{"name":"list","path":"/orders","methods":["GET"],"profile":"api"}]}`
	var h Handler
	if err := json.Unmarshal([]byte(input), &h); err != nil {
		t.Fatal(err)
	}
	if err := h.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestCaddyfileRouteProfileAdaptsToEffectiveRoute(t *testing.T) {
	input := `nats_web_gateway {
 nats_urls nats://127.0.0.1:4222
 connect_timeout 5s
 reconnect_wait 1s
 max_reconnects -1
 drain_timeout 30s
 route_profile api {
  subject orders.list
  timeout 2s
  max_request_body_bytes 1024
  max_reply_bytes 1024
  response_mode binary
  response_content_type application/octet-stream
  stream_mode request_reply
 }
 route list {
  use api
  path /orders
  methods GET
 }
}`
	var h Handler
	if err := h.UnmarshalCaddyfile(caddyfile.NewTestDispenser(input)); err != nil {
		t.Fatal(err)
	}
	if len(h.RouteProfiles) != 0 || h.Routes[0].Profile != "" {
		t.Fatalf("Caddyfile did not emit effective routes: %#v", h)
	}
	routes, err := h.resolvedRoutes()
	if err != nil {
		t.Fatal(err)
	}
	if routes[0].Subject != "orders.list" || time.Duration(routes[0].Timeout) != 2*time.Second {
		t.Fatalf("resolved = %#v", routes[0])
	}
}

func TestRouteProfileExtensionRejectsMapCollision(t *testing.T) {
	base := validRoute("", "", "orders.{id}", "GET")
	base.Name, base.Path, base.Methods = "", "", nil
	h := validHandler(Route{Name: "r", Path: "/r/{id}", Methods: []string{"GET"}, Profile: "base", Extend: &RouteExtensions{Parameters: map[string]Parameter{"id": {Source: "path", Name: "id", Pattern: `^[a-z]+$`}}}})
	h.RouteProfiles = []RouteProfile{{Name: "base", Route: base}}
	if err := h.Validate(); err == nil || !strings.Contains(err.Error(), "duplicate key") {
		t.Fatalf("error = %v", err)
	}
}

func TestRouteProfileRejectsDuplicateClear(t *testing.T) {
	h := validHandler(Route{Name: "r", Path: "/r", Methods: []string{"GET"}, Clear: []string{"response.headers", "response.headers"}})
	if err := h.Validate(); err == nil || !strings.Contains(err.Error(), "more than once") {
		t.Fatalf("error = %v", err)
	}
}

func TestRouteProfileClearRequiredFieldFailsEffectiveValidation(t *testing.T) {
	base := validRoute("", "", "orders", "GET")
	base.Name, base.Path, base.Methods = "", "", nil
	h := validHandler(Route{Name: "r", Path: "/r", Methods: []string{"GET"}, Profile: "api", Clear: []string{"timeout"}})
	h.RouteProfiles = []RouteProfile{{Name: "api", Route: base}}
	if err := h.Validate(); err == nil || !strings.Contains(err.Error(), "timeout") {
		t.Fatalf("error = %v", err)
	}
}

func TestNestedProfileFieldsMergeAndCanBeCleared(t *testing.T) {
	base := validRoute("", "", "orders", "GET")
	base.Name, base.Path, base.Methods = "", "", nil
	base.Response.NegotiateAccept = true
	base.Response.Representations = []string{"image/png"}
	override := RouteProfile{Name: "child", Extends: "base", Route: Route{Response: Response{ContentType: "image/png"}, Clear: []string{"response.negotiate_accept", "response.representations"}}}
	h := validHandler(Route{Name: "r", Path: "/r", Methods: []string{"GET"}, Profile: "child"})
	h.RouteProfiles = []RouteProfile{{Name: "base", Route: base}, override}
	routes, err := h.resolvedRoutes()
	if err != nil {
		t.Fatal(err)
	}
	if routes[0].Response.Mode != "binary" || routes[0].Response.ContentType != "image/png" || routes[0].Response.NegotiateAccept || len(routes[0].Response.Representations) != 0 {
		t.Fatalf("response = %#v", routes[0].Response)
	}
	if err := h.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestRouteProfileSharesCompletePolicyInventory(t *testing.T) {
	security := &SecurityContext{Mechanism: credentials.MechanismBearerToken, MaxCredentialBytes: 4096, MaxConnections: 4, IdleTimeout: caddy.Duration(time.Minute), MaxLifetime: caddy.Duration(time.Hour)}
	base := validRoute("", "", "events.{tenant}", "GET")
	base.Name, base.Path, base.Methods = "", "", nil
	base.Parameters = map[string]Parameter{"tenant": {Source: "query", Name: "tenant", Pattern: `^[A-Za-z0-9_-]+$`}}
	base.Timeout = caddy.Duration(3 * time.Second)
	base.MaxRequestBodyBytes, base.MaxReplyBytes = 2048, 4096
	base.Response = Response{Mode: responseModeBinary, Headers: []string{"Content-Type"}, ContentType: "application/octet-stream", ServiceErrorStatuses: map[string]int{"4001": 409}}
	base.StreamMode = streamModeCoreSSE
	base.CoreSSE = &CoreSSE{BufferMessages: 8, BufferBytes: 8192, HeartbeatInterval: caddy.Duration(time.Second), MaxDuration: caddy.Duration(time.Minute), MaxConnections: 5}
	base.SecurityContext = security
	h := validHandler(Route{Name: "events", Path: "/events", Methods: []string{"GET"}, Profile: "shared"})
	h.RouteProfiles = []RouteProfile{{Name: "shared", Route: base}}
	routes, err := h.resolvedRoutes()
	if err != nil {
		t.Fatal(err)
	}
	got := routes[0]
	if got.Subject != base.Subject || len(got.Parameters) != 1 || got.Timeout != base.Timeout || got.MaxRequestBodyBytes != 2048 || got.MaxReplyBytes != 4096 || got.Response.ServiceErrorStatuses["4001"] != 409 || got.CoreSSE == nil || got.CoreSSE.MaxConnections != 5 || got.SecurityContext == nil || got.SecurityContext.MaxConnections != 4 {
		t.Fatalf("effective route did not inherit complete policy: %#v", got)
	}
	if err := h.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestProvisionSharesEquivalentSecurityPoolsAndStreamQuotas(t *testing.T) {
	security := &SecurityContext{Mechanism: credentials.MechanismBearerToken, MaxConnections: 2, IdleTimeout: caddy.Duration(time.Minute), MaxLifetime: caddy.Duration(time.Hour)}
	stream := &CoreSSE{BufferMessages: 2, BufferBytes: 1024, HeartbeatInterval: caddy.Duration(time.Second), MaxDuration: caddy.Duration(time.Minute), MaxConnections: 3}
	one := validRoute("one", "/one", "one", "GET")
	two := validRoute("two", "/two", "two", "GET")
	one.SecurityContext = security
	two.SecurityContext = security
	h := validHandler(one, two)
	if err := h.Provision(caddy.Context{}); err != nil {
		t.Fatal(err)
	}
	if h.compiledRoutes[0].pool != h.compiledRoutes[1].pool {
		t.Fatal("equivalent security policies received distinct pools")
	}
	one.SecurityContext = nil
	two.SecurityContext = nil
	one.StreamMode = streamModeCoreSSE
	two.StreamMode = streamModeCoreSSE
	one.CoreSSE = stream
	two.CoreSSE = stream
	h = validHandler(one, two)
	h.connect = func(string, ...nats.Option) (natsConnection, error) { return &fakeNATSConnection{connected: true}, nil }
	if err := h.Provision(caddy.Context{}); err != nil {
		t.Fatal(err)
	}
	if h.compiledRoutes[0].streams != h.compiledRoutes[1].streams {
		t.Fatal("equivalent stream policies received distinct quotas")
	}
}

var _ = caddy.Duration(0)
