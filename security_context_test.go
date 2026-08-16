package natswebgateway

import (
	"context"
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
	first, err := pool.acquire(context.Background(), alice)
	if err != nil {
		t.Fatal(err)
	}
	if remaining := time.Until(first.expiresAt); remaining < 59*time.Minute || remaining > time.Hour {
		t.Fatalf("lease lifetime remaining = %v", remaining)
	}
	second, err := pool.acquire(context.Background(), alice)
	if err != nil {
		t.Fatal(err)
	}
	if first.connection != second.connection || connects.Load() != 1 {
		t.Fatalf("same context connection/connects = %p/%p/%d", first.connection, second.connection, connects.Load())
	}
	bobLease, err := pool.acquire(context.Background(), bob)
	if err != nil {
		t.Fatal(err)
	}
	if bobLease.connection == first.connection || connects.Load() != 2 {
		t.Fatal("distinct contexts shared a connection")
	}
	if _, err := pool.acquire(context.Background(), adaptedBearer(t, "charlie")); !errors.Is(err, errSecurityContextLimit) {
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
	lease, err := pool.acquire(context.Background(), adaptedBearer(t, "alice"))
	if err != nil {
		t.Fatal(err)
	}
	first := <-connections
	lease.release()
	eventually(t, time.Second, func() bool { return first.closes.Load() == 1 })
	lease, err = pool.acquire(context.Background(), adaptedBearer(t, "alice"))
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
	lease, err := pool.acquire(context.Background(), adapted)
	if err != nil {
		t.Fatal(err)
	}
	lease.release()
	connection.connected = false
	if _, err := pool.acquire(context.Background(), adapted); !errors.Is(err, nats.ErrConnectionReconnecting) {
		t.Fatalf("reconnecting acquire error = %v", err)
	}
	if connection.closes.Load() != 0 {
		t.Fatal("reconnecting identity-bound connection was replaced")
	}
	connection.connected = true
	lease, err = pool.acquire(context.Background(), adapted)
	if err != nil || lease.connection != connection {
		t.Fatalf("recovered acquire = %v, %p", err, lease)
	}
	lease.release()
}

func TestSecurityContextPoolConnectDoesNotBlockOtherContextsOrCleanup(t *testing.T) {
	started := make(chan struct{})
	unblock := make(chan struct{})
	connector := func(_ string, options ...nats.Option) (natsConnection, error) {
		configured := nats.GetDefaultOptions()
		for _, option := range options {
			if err := option(&configured); err != nil {
				return nil, err
			}
		}
		if configured.Token == "alice" {
			close(started)
			<-unblock
		}
		return &fakeNATSConnection{connected: true, options: configured}, nil
	}
	pool := newSecurityContextPool(SecurityContext{MaxConnections: 2, IdleTimeout: caddy.Duration(time.Hour), MaxLifetime: caddy.Duration(time.Hour)}, "nats://example", connector, []nats.Option{nats.ClosedHandler(func(*nats.Conn) {})})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	alice := adaptedBearer(t, "alice")
	aliceResult := make(chan error, 1)
	go func() {
		_, err := pool.acquire(ctx, alice)
		aliceResult <- err
	}()
	<-started
	if err := <-aliceResult; !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("blocked acquire error = %v", err)
	}
	bob, err := pool.acquire(context.Background(), adaptedBearer(t, "bob"))
	if err != nil {
		t.Fatalf("independent context acquire: %v", err)
	}
	bob.release()
	closed := make(chan struct{})
	go func() {
		pool.close()
		close(closed)
	}()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("cleanup blocked behind an in-flight connection")
	}
	close(unblock)
}

func TestSecurityContextPoolRefreshesJWTAndRetiresOldIdentity(t *testing.T) {
	var jwtMu sync.Mutex
	jwt := "jwt-a"
	request := httptest.NewRequest(http.MethodGet, "https://example.test", nil)
	request = request.WithContext(credentials.WithNKeyJWTProof(request.Context(), credentials.NKeyJWTProof{
		JWT: func() (string, error) {
			jwtMu.Lock()
			defer jwtMu.Unlock()
			return jwt, nil
		},
		Sign: func(nonce []byte) ([]byte, error) { return nonce, nil },
	}))
	adapted, err := (credentials.Adapter{Mechanism: credentials.MechanismNKeyJWT}).Adapt(request)
	if err != nil {
		t.Fatal(err)
	}
	var connections []*fakeNATSConnection
	var observedJWTs []string
	connector := func(_ string, options ...nats.Option) (natsConnection, error) {
		configured := nats.GetDefaultOptions()
		for _, option := range options {
			if err := option(&configured); err != nil {
				return nil, err
			}
		}
		observed, callbackErr := configured.UserJWT()
		if callbackErr != nil {
			return nil, callbackErr
		}
		observedJWTs = append(observedJWTs, observed)
		connection := &fakeNATSConnection{connected: true, options: configured}
		connections = append(connections, connection)
		return connection, nil
	}
	pool := newSecurityContextPool(SecurityContext{MaxConnections: 1, IdleTimeout: caddy.Duration(time.Hour), MaxLifetime: caddy.Duration(time.Hour)}, "nats://example", connector, []nats.Option{nats.ClosedHandler(func(*nats.Conn) {})})
	t.Cleanup(pool.close)
	first, err := pool.acquire(context.Background(), adapted)
	if err != nil {
		t.Fatal(err)
	}
	first.release()
	jwtMu.Lock()
	jwt = "jwt-b"
	jwtMu.Unlock()
	second, err := pool.acquire(context.Background(), adapted)
	if err != nil {
		t.Fatal(err)
	}
	second.release()
	if len(observedJWTs) != 2 || observedJWTs[0] != "jwt-a" || observedJWTs[1] != "jwt-b" {
		t.Fatalf("connected JWTs = %v", observedJWTs)
	}
	if connections[0].closes.Load() != 1 || connections[0] == connections[1] {
		t.Fatal("rotated JWT reused or retained the old identity connection")
	}
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

	route := validRoute("tenant", "/tenant/{id}", "tenant.{id}", http.MethodGet)
	route.SecurityContext = &SecurityContext{Mechanism: credentials.MechanismUserPassword, MaxConnections: 2, IdleTimeout: caddy.Duration(time.Minute), MaxLifetime: caddy.Duration(time.Hour)}
	handler := validHandler(route)
	handler.NATS.URLs = []string{natsServer.ClientURL()}
	if err := handler.Provision(caddy.Context{}); err != nil {
		t.Fatal(err)
	}
	if handler.lifecycle.connection != nil || !handler.Ready() {
		t.Fatalf("protected-only operator connection/ready = %v/%t", handler.lifecycle.connection, handler.Ready())
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
