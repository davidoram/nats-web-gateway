package natswebgateway

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp"
	"github.com/davidoram/nats-web-gateway/internal/credentials"
	server "github.com/nats-io/nats-server/v2/server"
	natstest "github.com/nats-io/nats-server/v2/test"
	"github.com/nats-io/nats.go"
)

func TestSecurityContextPoolIsolatesReusesAndBoundsConnections(t *testing.T) {
	var connects atomic.Int32
	var connectionsMu sync.Mutex
	var connections []*fakeNATSConnection
	connector := func(_ string, options ...nats.Option) (natsConnection, error) {
		connection := &fakeNATSConnection{connected: true}
		connection.options = nats.GetDefaultOptions()
		for _, option := range options {
			if err := option(&connection.options); err != nil {
				return nil, err
			}
		}
		connects.Add(1)
		connectionsMu.Lock()
		connections = append(connections, connection)
		connectionsMu.Unlock()
		return connection, nil
	}
	pool := newSecurityContextPool(SecurityContext{MaxConnections: 2, IdleTimeout: caddy.Duration(time.Hour), MaxLifetime: caddy.Duration(time.Hour)}, "nats://example", connector, []nats.Option{nats.ClosedHandler(func(*nats.Conn) {})})
	t.Cleanup(pool.close)

	alice := adaptedBearer(t, "alice")
	bob := adaptedBearer(t, "bob")
	first, err := pool.acquire(alice)
	if err != nil {
		t.Fatal(err)
	}
	second, err := pool.acquire(alice)
	if err != nil {
		t.Fatal(err)
	}
	if first.connection != second.connection || connects.Load() != 1 {
		t.Fatalf("same context connection/connects = %p/%p/%d", first.connection, second.connection, connects.Load())
	}
	bobLease, err := pool.acquire(bob)
	if err != nil {
		t.Fatal(err)
	}
	if bobLease.connection == first.connection || connects.Load() != 2 {
		t.Fatal("distinct contexts shared a connection")
	}
	if _, err := pool.acquire(adaptedBearer(t, "charlie")); !errors.Is(err, errSecurityContextLimit) {
		t.Fatalf("third context error = %v, want cardinality limit", err)
	}
	first.release()
	second.release()
	bobLease.release()
}

func TestSecurityContextPoolExpiresIdleConnectionAndClosesOnCleanup(t *testing.T) {
	connections := make(chan *fakeNATSConnection, 2)
	connector := func(_ string, options ...nats.Option) (natsConnection, error) {
		connection := &fakeNATSConnection{connected: true, options: nats.GetDefaultOptions()}
		for _, option := range options {
			if err := option(&connection.options); err != nil {
				return nil, err
			}
		}
		connections <- connection
		return connection, nil
	}
	pool := newSecurityContextPool(SecurityContext{MaxConnections: 1, IdleTimeout: caddy.Duration(10 * time.Millisecond), MaxLifetime: caddy.Duration(time.Hour)}, "nats://example", connector, []nats.Option{nats.ClosedHandler(func(*nats.Conn) {})})
	lease, err := pool.acquire(adaptedBearer(t, "alice"))
	if err != nil {
		t.Fatal(err)
	}
	first := <-connections
	lease.release()
	eventually(t, time.Second, func() bool { return first.closes.Load() == 1 })
	lease, err = pool.acquire(adaptedBearer(t, "alice"))
	if err != nil {
		t.Fatal(err)
	}
	second := <-connections
	pool.close()
	lease.release()
	if second.closes.Load() != 1 {
		t.Fatalf("cleanup closes = %d, want 1", second.closes.Load())
	}
}

func TestSecurityContextPoolRetainsIdentityAcrossReconnect(t *testing.T) {
	connection := &fakeNATSConnection{connected: true, options: nats.GetDefaultOptions()}
	connector := func(_ string, options ...nats.Option) (natsConnection, error) {
		for _, option := range options {
			if err := option(&connection.options); err != nil {
				return nil, err
			}
		}
		return connection, nil
	}
	pool := newSecurityContextPool(SecurityContext{MaxConnections: 1, IdleTimeout: caddy.Duration(time.Hour), MaxLifetime: caddy.Duration(time.Hour)}, "nats://example", connector, []nats.Option{nats.ClosedHandler(func(*nats.Conn) {})})
	t.Cleanup(pool.close)
	adapted := adaptedBearer(t, "alice")
	lease, err := pool.acquire(adapted)
	if err != nil {
		t.Fatal(err)
	}
	lease.release()
	connection.connected = false
	if _, err := pool.acquire(adapted); !errors.Is(err, nats.ErrConnectionReconnecting) {
		t.Fatalf("reconnecting acquire error = %v", err)
	}
	if connection.closes.Load() != 0 {
		t.Fatal("reconnecting identity-bound connection was replaced")
	}
	connection.connected = true
	lease, err = pool.acquire(adapted)
	if err != nil || lease.connection != connection {
		t.Fatalf("recovered acquire = %v, %p", err, lease)
	}
	lease.release()
}

func TestProtectedRouteUsesNATSAuthenticationAndSubjectPermissions(t *testing.T) {
	options := &server.Options{Host: "127.0.0.1", Port: -1, Users: []*server.User{
		{Username: "gateway", Password: "operator"},
		{Username: "alice", Password: "alice-secret", Permissions: &server.Permissions{Publish: &server.SubjectPermission{Allow: []string{"tenant.alice"}}, Subscribe: &server.SubjectPermission{Allow: []string{"_INBOX.>"}}}},
		{Username: "bob", Password: "bob-secret", Permissions: &server.Permissions{Publish: &server.SubjectPermission{Allow: []string{"tenant.bob"}}, Subscribe: &server.SubjectPermission{Allow: []string{"_INBOX.>"}}}},
	}}
	natsServer := natstest.RunServer(options)
	t.Cleanup(natsServer.Shutdown)
	service, err := nats.Connect(natsServer.ClientURL(), nats.UserInfo("gateway", "operator"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(service.Close)
	if _, err := service.Subscribe("tenant.*", func(message *nats.Msg) { _ = message.Respond([]byte(`{"ok":true}`)) }); err != nil {
		t.Fatal(err)
	}
	if err := service.Flush(); err != nil {
		t.Fatal(err)
	}

	t.Setenv("TEST_OPERATOR_PASSWORD", "operator")
	route := validRoute("tenant", "/tenant/{id}", "tenant.{id}", http.MethodGet)
	route.SecurityContext = &SecurityContext{Mechanism: credentials.MechanismUserPassword, MaxConnections: 2, IdleTimeout: caddy.Duration(time.Minute), MaxLifetime: caddy.Duration(time.Hour)}
	handler := validHandler(route)
	handler.NATS.URLs = []string{natsServer.ClientURL()}
	handler.NATS.Username = "gateway"
	handler.NATS.Password = "{env.TEST_OPERATOR_PASSWORD}"
	if err := handler.Provision(caddy.Context{}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = handler.Cleanup() })

	request := httptest.NewRequest(http.MethodGet, "/tenant/alice", nil)
	request.SetBasicAuth("alice", "alice-secret")
	if response := serveProtected(t, &handler, request); response.Code != http.StatusOK {
		t.Fatalf("authorized status/body = %d/%q", response.Code, response.Body.String())
	}
	request = httptest.NewRequest(http.MethodGet, "/tenant/bob", nil)
	request.SetBasicAuth("alice", "alice-secret")
	if response := serveProtected(t, &handler, request); response.Code != http.StatusForbidden {
		t.Fatalf("permission status/body = %d/%q", response.Code, response.Body.String())
	}
	request = httptest.NewRequest(http.MethodGet, "/tenant/bob", nil)
	request.SetBasicAuth("bob", "wrong")
	if response := serveProtected(t, &handler, request); response.Code != http.StatusUnauthorized {
		t.Fatalf("authentication status/body = %d/%q", response.Code, response.Body.String())
	}
}

func adaptedBearer(t *testing.T, token string) credentials.Context {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "https://example.test", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	adapted, err := (credentials.Adapter{Mechanism: credentials.MechanismBearerToken}).Adapt(request)
	if err != nil {
		t.Fatal(err)
	}
	return adapted
}

func eventually(t *testing.T, timeout time.Duration, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatal("condition did not become true")
		}
		time.Sleep(time.Millisecond)
	}
}

func serveProtected(t *testing.T, handler *Handler, request *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	if err := handler.ServeHTTP(recorder, request, caddyhttp.HandlerFunc(func(http.ResponseWriter, *http.Request) error {
		t.Fatal("next handler called")
		return nil
	})); err != nil {
		t.Fatal(err)
	}
	return recorder
}
