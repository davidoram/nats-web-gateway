package natswebgateway

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp"
	"github.com/davidoram/nats-web-gateway/internal/credentials"
	natstest "github.com/nats-io/nats-server/v2/test"
	"github.com/nats-io/nats.go"
)

func TestResolveDownstreamIdentityValidatesNATSUserInfo(t *testing.T) {
	config := DownstreamIdentity{Source: downstreamIdentitySourceNATSUserID, Header: "X-Authenticated-User", MaxValueBytes: 8}
	tests := []struct {
		name string
		data string
		err  error
	}{
		{name: "authenticated user", data: `{"data":{"user":"alice"}}`},
		{name: "missing user", data: `{"data":{"user":""}}`, err: errDownstreamIdentityUnavailable},
		{name: "missing data", data: `{}`, err: errDownstreamIdentityUnavailable},
		{name: "server error", data: `{"error":{"code":503}}`, err: errDownstreamIdentityUnavailable},
		{name: "malformed", data: `{`, err: errDownstreamIdentityUnavailable},
		{name: "line injection", data: `{"data":{"user":"a\r\nb"}}`, err: errDownstreamIdentityInvalid},
		{name: "control injection", data: `{"data":{"user":"a\u0000b"}}`, err: errDownstreamIdentityInvalid},
		{name: "oversized", data: `{"data":{"user":"alice-long"}}`, err: errDownstreamIdentityInvalid},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			connection := &fakeNATSConnection{connected: true, request: func(_ context.Context, message *nats.Msg) (*nats.Msg, error) {
				if message.Subject != natsUserInfoSubject || message.Reply != "" || len(message.Data) != 0 {
					t.Fatalf("identity request = %+v", message)
				}
				return &nats.Msg{Data: []byte(test.data)}, nil
			}}
			identity, err := resolveDownstreamIdentity(context.Background(), connection, config)
			if !errors.Is(err, test.err) {
				t.Fatalf("error = %v, want %v", err, test.err)
			}
			if test.err == nil && identity != "alice" {
				t.Fatalf("identity = %q", identity)
			}
		})
	}
}

func TestDownstreamIdentityConfigurationFailsClosed(t *testing.T) {
	valid := func() Route {
		route := validRoute("identity", "/identity", "identity.echo", http.MethodGet)
		route.SecurityContext = &SecurityContext{
			Mechanism: credentials.MechanismBearerToken, MaxConnections: 1,
			IdleTimeout: caddy.Duration(time.Minute), MaxLifetime: caddy.Duration(time.Hour),
			DownstreamIdentity: &DownstreamIdentity{Source: downstreamIdentitySourceNATSUserID, Header: "X-Authenticated-User", MaxValueBytes: 128},
		}
		return route
	}
	if err := valid().validate(); err != nil {
		t.Fatalf("valid identity configuration: %v", err)
	}
	tests := []struct {
		name   string
		mutate func(*Route)
	}{
		{name: "unsupported source", mutate: func(route *Route) { route.SecurityContext.DownstreamIdentity.Source = "credential_claim" }},
		{name: "missing header", mutate: func(route *Route) { route.SecurityContext.DownstreamIdentity.Header = "" }},
		{name: "noncanonical header", mutate: func(route *Route) { route.SecurityContext.DownstreamIdentity.Header = "x-user-id" }},
		{name: "NATS header", mutate: func(route *Route) { route.SecurityContext.DownstreamIdentity.Header = "Nats-User" }},
		{name: "unbounded value", mutate: func(route *Route) { route.SecurityContext.DownstreamIdentity.MaxValueBytes = 0 }},
		{name: "allowlist collision", mutate: func(route *Route) { route.RequestHeaders = append(route.RequestHeaders, "X-Authenticated-User") }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			route := valid()
			test.mutate(&route)
			if err := route.validate(); err == nil {
				t.Fatal("validation succeeded")
			}
		})
	}
}

func TestDownstreamIdentitySupportsEveryCredentialMechanismOnlyThroughNATSProtocol(t *testing.T) {
	for _, mechanism := range []credentials.Mechanism{
		credentials.MechanismBearerToken, credentials.MechanismUserPassword, credentials.MechanismNKey,
		credentials.MechanismNKeyJWT, credentials.MechanismTLS,
	} {
		route := validRoute("identity", "/identity", "identity.echo", http.MethodGet)
		route.SecurityContext = &SecurityContext{
			Mechanism: mechanism, MaxConnections: 1, IdleTimeout: caddy.Duration(time.Minute), MaxLifetime: caddy.Duration(time.Hour),
			DownstreamIdentity: &DownstreamIdentity{Source: downstreamIdentitySourceNATSUserID, Header: "X-Authenticated-User", MaxValueBytes: 128},
		}
		if err := route.validate(); err != nil {
			t.Errorf("mechanism %q: %v", mechanism, err)
		}
	}
}

func TestProtectedRouteReplacesSpoofedIdentityWithNATSAuthenticatedUser(t *testing.T) {
	route := validRoute("identity", "/identity", "identity.echo", http.MethodPost)
	route.RequestHeaders = []string{"Content-Type"}
	route.SecurityContext = &SecurityContext{
		Mechanism: credentials.MechanismBearerToken, MaxConnections: 1,
		IdleTimeout: caddy.Duration(time.Minute), MaxLifetime: caddy.Duration(time.Hour),
		DownstreamIdentity: &DownstreamIdentity{Source: downstreamIdentitySourceNATSUserID, Header: "X-Authenticated-User", MaxValueBytes: 128},
	}
	handler := validHandler(route)
	var mu sync.Mutex
	serviceRequests := 0
	handler.connect = func(_ string, options ...nats.Option) (natsConnection, error) {
		connection := &fakeNATSConnection{connected: true, options: nats.GetDefaultOptions()}
		for _, option := range options {
			if err := option(&connection.options); err != nil {
				return nil, err
			}
		}
		connection.request = func(_ context.Context, message *nats.Msg) (*nats.Msg, error) {
			if message.Subject == natsUserInfoSubject {
				return &nats.Msg{Data: []byte(`{"data":{"user":"nats-alice"}}`)}, nil
			}
			mu.Lock()
			defer mu.Unlock()
			serviceRequests++
			if got := message.Header.Values("X-Authenticated-User"); len(got) != 1 || got[0] != "nats-alice" {
				t.Fatalf("downstream identity = %q", got)
			}
			return &nats.Msg{Data: []byte("ok"), Header: nats.Header{"Content-Type": []string{"application/octet-stream"}}}, nil
		}
		return connection, nil
	}
	if err := handler.Provision(caddy.Context{}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = handler.Cleanup() })

	request := httptest.NewRequest(http.MethodPost, "/identity", strings.NewReader("body"))
	request.Header.Set("Authorization", "Bearer opaque-credential")
	request.Header.Add("X-Authenticated-User", "mallory")
	request.Header.Add("X-Authenticated-User", "second-spoof")
	recorder := httptest.NewRecorder()
	if err := handler.ServeHTTP(recorder, request, caddyhttp.HandlerFunc(func(http.ResponseWriter, *http.Request) error {
		t.Fatal("next handler called")
		return nil
	})); err != nil {
		t.Fatal(err)
	}
	if recorder.Code != http.StatusOK || serviceRequests != 1 {
		t.Fatalf("response/service requests = %d/%d, body %q", recorder.Code, serviceRequests, recorder.Body.String())
	}
}

func TestProtectedRouteDoesNotPublishWhenAuthenticatedIdentityIsUnavailable(t *testing.T) {
	route := validRoute("identity", "/identity", "identity.echo", http.MethodGet)
	route.SecurityContext = &SecurityContext{
		Mechanism: credentials.MechanismBearerToken, MaxConnections: 1,
		IdleTimeout: caddy.Duration(time.Minute), MaxLifetime: caddy.Duration(time.Hour),
		DownstreamIdentity: &DownstreamIdentity{Source: downstreamIdentitySourceNATSUserID, Header: "X-Authenticated-User", MaxValueBytes: 128},
	}
	handler := validHandler(route)
	handler.connect = func(_ string, options ...nats.Option) (natsConnection, error) {
		connection := &fakeNATSConnection{connected: true, options: nats.GetDefaultOptions()}
		for _, option := range options {
			if err := option(&connection.options); err != nil {
				return nil, err
			}
		}
		connection.request = func(_ context.Context, message *nats.Msg) (*nats.Msg, error) {
			if message.Subject != natsUserInfoSubject {
				t.Fatalf("service request published after identity failure: %s", message.Subject)
			}
			return &nats.Msg{Data: []byte(`{"data":{"user":"sensitive-user\r\ninjected"}}`)}, nil
		}
		return connection, nil
	}
	if err := handler.Provision(caddy.Context{}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = handler.Cleanup() })
	request := httptest.NewRequest(http.MethodGet, "/identity", nil)
	request.Header.Set("Authorization", "Bearer opaque-credential")
	response := serveProtected(t, &handler, request)
	if response.Code != http.StatusServiceUnavailable || strings.Contains(response.Body.String(), "identity") || strings.Contains(response.Body.String(), "sensitive-user") {
		t.Fatalf("response = %d %q", response.Code, response.Body.String())
	}
}

func TestDownstreamServiceReceivesExactlyNATSAuthenticatedIdentity(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "nats.conf")
	config := `
listen: 127.0.0.1:-1
accounts: {
  SYS: { users: [{user: sys, password: sys-secret}] }
  APP: {
    users: [
      {user: alice, password: alice-secret, permissions: {publish: ["$SYS.REQ.USER.INFO", "identity.echo"], subscribe: ["_INBOX.>"]}}
      {user: bob, password: bob-secret, permissions: {publish: ["$SYS.REQ.USER.INFO", "identity.echo"], subscribe: ["_INBOX.>"]}}
      {user: service, password: service-secret, permissions: {publish: ["_INBOX.>"], subscribe: ["identity.echo"]}}
    ]
  }
}

system_account: SYS
`
	if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	natsServer, _ := natstest.RunServerWithConfig(configPath)
	t.Cleanup(natsServer.Shutdown)
	service, err := nats.Connect(natsServer.ClientURL(), nats.UserInfo("service", "service-secret"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(service.Close)
	received := make(chan string, 2)
	if _, err := service.Subscribe("identity.echo", func(message *nats.Msg) {
		received <- message.Header.Get("X-Authenticated-User")
		response := nats.NewMsg(message.Reply)
		response.Header.Set("Content-Type", "application/octet-stream")
		response.Data = []byte("ok")
		_ = service.PublishMsg(response)
	}); err != nil {
		t.Fatal(err)
	}
	if err := service.Flush(); err != nil {
		t.Fatal(err)
	}

	route := validRoute("identity", "/identity", "identity.echo", http.MethodGet)
	route.SecurityContext = &SecurityContext{
		Mechanism: credentials.MechanismUserPassword, MaxConnections: 2,
		IdleTimeout: caddy.Duration(time.Minute), MaxLifetime: caddy.Duration(time.Hour),
		DownstreamIdentity: &DownstreamIdentity{Source: downstreamIdentitySourceNATSUserID, Header: "X-Authenticated-User", MaxValueBytes: 128},
	}
	handler := validHandler(route)
	handler.NATS.URLs = []string{natsServer.ClientURL()}
	if err := handler.Provision(caddy.Context{}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = handler.Cleanup() })

	var requests sync.WaitGroup
	for _, user := range []string{"alice", "bob"} {
		user := user
		requests.Add(1)
		go func() {
			defer requests.Done()
			request := httptest.NewRequest(http.MethodGet, "/identity", nil)
			request.SetBasicAuth(user, user+"-secret")
			request.Header.Set("X-Authenticated-User", "mallory")
			if response := serveProtected(t, &handler, request); response.Code != http.StatusOK {
				t.Errorf("%s response = %d %q", user, response.Code, response.Body.String())
			}
		}()
	}
	requests.Wait()
	got := map[string]int{<-received: 1, <-received: 1}
	if got["alice"] != 1 || got["bob"] != 1 || len(got) != 2 {
		t.Fatalf("received identities = %v", got)
	}
}

func TestOverlappingHandlersOwnIndependentDownstreamIdentity(t *testing.T) {
	makeHandler := func(identity string, received chan<- string) *Handler {
		route := validRoute("identity", "/identity", "identity.echo", http.MethodGet)
		route.SecurityContext = &SecurityContext{
			Mechanism: credentials.MechanismBearerToken, MaxConnections: 1,
			IdleTimeout: caddy.Duration(time.Minute), MaxLifetime: caddy.Duration(time.Hour),
			DownstreamIdentity: &DownstreamIdentity{Source: downstreamIdentitySourceNATSUserID, Header: "X-Authenticated-User", MaxValueBytes: 128},
		}
		handler := validHandler(route)
		handler.connect = func(_ string, options ...nats.Option) (natsConnection, error) {
			connection := &fakeNATSConnection{connected: true, options: nats.GetDefaultOptions()}
			for _, option := range options {
				if err := option(&connection.options); err != nil {
					return nil, err
				}
			}
			connection.request = func(_ context.Context, message *nats.Msg) (*nats.Msg, error) {
				if message.Subject == natsUserInfoSubject {
					return &nats.Msg{Data: []byte(`{"data":{"user":"` + identity + `"}}`)}, nil
				}
				received <- message.Header.Get("X-Authenticated-User")
				return &nats.Msg{Data: []byte("ok"), Header: nats.Header{"Content-Type": []string{"application/octet-stream"}}}, nil
			}
			return connection, nil
		}
		if err := handler.Provision(caddy.Context{}); err != nil {
			t.Fatal(err)
		}
		return &handler
	}
	received := make(chan string, 2)
	oldHandler := makeHandler("old-user", received)
	newHandler := makeHandler("new-user", received)
	t.Cleanup(func() {
		_ = oldHandler.Cleanup()
		_ = newHandler.Cleanup()
	})
	for _, handler := range []*Handler{oldHandler, newHandler} {
		request := httptest.NewRequest(http.MethodGet, "/identity", nil)
		request.Header.Set("Authorization", "Bearer credential")
		if response := serveProtected(t, handler, request); response.Code != http.StatusOK {
			t.Fatalf("response = %d %q", response.Code, response.Body.String())
		}
	}
	got := map[string]bool{<-received: true, <-received: true}
	if !got["old-user"] || !got["new-user"] || len(got) != 2 {
		t.Fatalf("overlapping identities = %v", got)
	}
}

func FuzzResolveDownstreamIdentity(f *testing.F) {
	for _, seed := range []string{`{"data":{"user":"alice"}}`, `{}`, `{"data":{"user":"a\r\nb"}}`, "not-json"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, data string) {
		connection := &fakeNATSConnection{connected: true, request: func(context.Context, *nats.Msg) (*nats.Msg, error) {
			return &nats.Msg{Data: []byte(data)}, nil
		}}
		_, _ = resolveDownstreamIdentity(context.Background(), connection, DownstreamIdentity{MaxValueBytes: 128})
	})
}
