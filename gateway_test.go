package natswebgateway

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp"
	natstest "github.com/nats-io/nats-server/v2/test"
	"github.com/nats-io/nats.go"
)

func TestHandlerRegistration(t *testing.T) {
	module, err := caddy.GetModule("http.handlers.nats_web_gateway")
	if err != nil {
		t.Fatalf("get registered module: %v", err)
	}

	instance := module.New()
	if _, ok := instance.(*Handler); !ok {
		t.Fatalf("registered constructor returned %T, want *Handler", instance)
	}
}

func TestHandlerProvisionTracksConnectionStateAndCleansUp(t *testing.T) {
	t.Setenv("TEST_NATS_PASSWORD", "secret-value")
	handler := validHandler(validRoute("health", "/health", "demo.echo", "GET"))
	handler.NATS.Username = "gateway"
	handler.NATS.Password = "{env.TEST_NATS_PASSWORD}"
	fake := &fakeNATSConnection{connected: true}
	var gotURL string
	handler.connect = func(url string, options ...nats.Option) (natsConnection, error) {
		gotURL = url
		natsOptions := nats.GetDefaultOptions()
		for _, option := range options {
			if err := option(&natsOptions); err != nil {
				return nil, err
			}
		}
		if natsOptions.User != "gateway" || natsOptions.Password != "secret-value" {
			t.Fatalf("resolved credentials = %q/%q, want gateway/resolved secret", natsOptions.User, natsOptions.Password)
		}
		fake.options = natsOptions
		return fake, nil
	}

	if err := handler.Provision(caddy.Context{}); err != nil {
		t.Fatalf("Provision() error = %v", err)
	}
	if gotURL != "nats://127.0.0.1:4222" || !handler.Ready() {
		t.Fatalf("connection URL/ready = %q/%t", gotURL, handler.Ready())
	}
	fake.options.DisconnectedErrCB(nil, errors.New("network unavailable"))
	if handler.Ready() {
		t.Fatal("Ready() = true after disconnect")
	}
	fake.options.ReconnectedCB(nil)
	if !handler.Ready() {
		t.Fatal("Ready() = false after reconnect")
	}

	if err := handler.Cleanup(); err != nil {
		t.Fatalf("Cleanup() error = %v", err)
	}
	fake.options.ReconnectedCB(nil)
	if handler.Ready() || fake.drains.Load() != 1 || fake.closes.Load() != 0 {
		t.Fatalf("cleanup ready/drains/closes = %t/%d/%d", handler.Ready(), fake.drains.Load(), fake.closes.Load())
	}
	if err := handler.Cleanup(); err != nil || fake.drains.Load() != 1 {
		t.Fatalf("second Cleanup() error/drains = %v/%d", err, fake.drains.Load())
	}
}

func TestHandlerProvisionFailureDoesNotPublishLifecycle(t *testing.T) {
	handler := validHandler(validRoute("health", "/health", "demo.echo", "GET"))
	wantErr := errors.New("authentication rejected")
	handler.connect = func(string, ...nats.Option) (natsConnection, error) { return nil, wantErr }
	err := handler.Provision(caddy.Context{})
	if !errors.Is(err, wantErr) || handler.Ready() {
		t.Fatalf("Provision() error/ready = %v/%t, want wrapped failure/false", err, handler.Ready())
	}
}

func TestConnectionLifecycleShutdownWinsConcurrentReconnect(t *testing.T) {
	t.Parallel()

	for range 1_000 {
		lifecycle := &connectionLifecycle{ready: true}
		start := make(chan struct{})
		var transitions sync.WaitGroup
		transitions.Add(2)
		go func() {
			defer transitions.Done()
			<-start
			lifecycle.setReady(true)
		}()
		go func() {
			defer transitions.Done()
			<-start
			lifecycle.beginStopping()
		}()
		close(start)
		transitions.Wait()
		if lifecycle.isReady() {
			t.Fatal("lifecycle became ready after concurrent shutdown and reconnect")
		}
	}
}

func TestOverlappingHandlersOwnIndependentConnections(t *testing.T) {
	first := provisionFakeHandler(t)
	second := provisionFakeHandler(t)
	if err := first.handler.Cleanup(); err != nil {
		t.Fatalf("first Cleanup() error = %v", err)
	}
	if first.handler.Ready() || !second.handler.Ready() || second.connection.drains.Load() != 0 {
		t.Fatalf("overlap state first/second/second drains = %t/%t/%d", first.handler.Ready(), second.handler.Ready(), second.connection.drains.Load())
	}
	if err := second.handler.Cleanup(); err != nil {
		t.Fatalf("second Cleanup() error = %v", err)
	}
}

type provisionedFake struct {
	handler    *Handler
	connection *fakeNATSConnection
}

func provisionFakeHandler(t *testing.T) provisionedFake {
	t.Helper()
	handler := validHandler(validRoute("health", "/health", "demo.echo", "GET"))
	fake := &fakeNATSConnection{connected: true}
	handler.connect = func(_ string, options ...nats.Option) (natsConnection, error) {
		fake.options = nats.GetDefaultOptions()
		for _, option := range options {
			if err := option(&fake.options); err != nil {
				return nil, err
			}
		}
		return fake, nil
	}
	if err := handler.Provision(caddy.Context{}); err != nil {
		t.Fatalf("Provision() error = %v", err)
	}
	return provisionedFake{handler: &handler, connection: fake}
}

type fakeNATSConnection struct {
	connected bool
	options   nats.Options
	request   func(context.Context, *nats.Msg) (*nats.Msg, error)
	drains    atomic.Int32
	closes    atomic.Int32
}

func (connection *fakeNATSConnection) RequestMsgWithContext(ctx context.Context, message *nats.Msg) (*nats.Msg, error) {
	if connection.request != nil {
		return connection.request(ctx, message)
	}
	return nil, errors.New("unexpected request")
}

func (connection *fakeNATSConnection) IsConnected() bool { return connection.connected }

func (connection *fakeNATSConnection) Drain() error {
	connection.drains.Add(1)
	connection.connected = false
	if connection.options.ClosedCB != nil {
		connection.options.ClosedCB(nil)
	}
	return nil
}

func (connection *fakeNATSConnection) Close() {
	connection.closes.Add(1)
	connection.connected = false
	if connection.options.ClosedCB != nil {
		connection.options.ClosedCB(nil)
	}
}

func TestHandlerCleanupBoundsDrain(t *testing.T) {
	handler := validHandler(validRoute("health", "/health", "demo.echo", "GET"))
	handler.NATS.DrainTimeout = caddy.Duration(time.Millisecond)
	fake := &fakeNATSConnection{connected: true}
	handler.connect = func(_ string, options ...nats.Option) (natsConnection, error) {
		fake.options = nats.GetDefaultOptions()
		for _, option := range options {
			if err := option(&fake.options); err != nil {
				return nil, err
			}
		}
		return &nonClosingDrainConnection{fake}, nil
	}
	if err := handler.Provision(caddy.Context{}); err != nil {
		t.Fatalf("Provision() error = %v", err)
	}
	if err := handler.Cleanup(); err == nil || fake.closes.Load() != 1 {
		t.Fatalf("Cleanup() error/closes = %v/%d, want timeout/1", err, fake.closes.Load())
	}
}

func TestHandlerCleanupClosesReconnectingConnectionImmediately(t *testing.T) {
	handler := validHandler(validRoute("health", "/health", "demo.echo", "GET"))
	handler.NATS.DrainTimeout = caddy.Duration(time.Hour)
	fake := &fakeNATSConnection{connected: true}
	handler.connect = func(_ string, options ...nats.Option) (natsConnection, error) {
		fake.options = nats.GetDefaultOptions()
		for _, option := range options {
			if err := option(&fake.options); err != nil {
				return nil, err
			}
		}
		return &reconnectingNATSConnection{fake}, nil
	}
	if err := handler.Provision(caddy.Context{}); err != nil {
		t.Fatalf("Provision() error = %v", err)
	}
	started := time.Now()
	if err := handler.Cleanup(); err != nil {
		t.Fatalf("Cleanup() error = %v", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("Cleanup() took %s, want immediate reconnect close", elapsed)
	}
	if fake.drains.Load() != 1 || fake.closes.Load() != 1 || handler.Ready() {
		t.Fatalf("cleanup drains/closes/ready = %d/%d/%t", fake.drains.Load(), fake.closes.Load(), handler.Ready())
	}
}

type nonClosingDrainConnection struct{ *fakeNATSConnection }

func (connection *nonClosingDrainConnection) Drain() error {
	connection.drains.Add(1)
	return nil
}

type reconnectingNATSConnection struct{ *fakeNATSConnection }

func (connection *reconnectingNATSConnection) Drain() error {
	connection.drains.Add(1)
	return nats.ErrConnectionReconnecting
}

func TestHandlerPassesRequestToNextHandler(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("next handler failed")
	request := httptest.NewRequest(http.MethodGet, "/example", nil)
	recorder := httptest.NewRecorder()
	called := false
	next := caddyhttp.HandlerFunc(func(w http.ResponseWriter, r *http.Request) error {
		called = true
		if w != recorder {
			t.Error("response writer was replaced")
		}
		if r != request {
			t.Error("request was replaced")
		}
		return wantErr
	})

	err := (Handler{}).ServeHTTP(recorder, request, next)
	if !called {
		t.Fatal("next handler was not called")
	}
	if !errors.Is(err, wantErr) {
		t.Fatalf("ServeHTTP error = %v, want %v", err, wantErr)
	}
}

func TestHandlerMapsRequestFailures(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		requestErr error
		wantStatus int
		wantBody   string
	}{
		{name: "no responders", requestErr: nats.ErrNoResponders, wantStatus: http.StatusServiceUnavailable, wantBody: "service unavailable"},
		{name: "timeout", requestErr: context.DeadlineExceeded, wantStatus: http.StatusGatewayTimeout, wantBody: "upstream request timed out"},
		{name: "permission", requestErr: nats.ErrPermissionViolation, wantStatus: http.StatusForbidden, wantBody: "forbidden"},
		{name: "authentication", requestErr: nats.ErrAuthorization, wantStatus: http.StatusUnauthorized, wantBody: "unauthorized"},
		{name: "connectivity", requestErr: nats.ErrDisconnected, wantStatus: http.StatusServiceUnavailable, wantBody: "service unavailable"},
		{name: "internal", requestErr: errors.New("unexpected"), wantStatus: http.StatusInternalServerError, wantBody: "internal gateway error"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			handler := validHandler(validRoute("example", "/example", "example", http.MethodGet))
			fake := &fakeNATSConnection{connected: true, request: func(context.Context, *nats.Msg) (*nats.Msg, error) {
				return nil, test.requestErr
			}}
			handler.connect = func(_ string, options ...nats.Option) (natsConnection, error) {
				fake.options = nats.GetDefaultOptions()
				for _, option := range options {
					if err := option(&fake.options); err != nil {
						return nil, err
					}
				}
				return fake, nil
			}
			if err := handler.Provision(caddy.Context{}); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = handler.Cleanup() })
			recorder := httptest.NewRecorder()
			err := handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/example", nil), caddyhttp.HandlerFunc(func(http.ResponseWriter, *http.Request) error {
				t.Fatal("next handler called")
				return nil
			}))
			if err != nil || recorder.Code != test.wantStatus || !strings.Contains(recorder.Body.String(), test.wantBody) {
				t.Fatalf("ServeHTTP() = error %v, status %d, body %q", err, recorder.Code, recorder.Body.String())
			}
		})
	}
}

func TestHandlerMapsAsynchronousPublishPermissionFailure(t *testing.T) {
	handler := validHandler(validRoute("example", "/example", "denied.subject", http.MethodGet))
	fake := &fakeNATSConnection{connected: true}
	fake.request = func(ctx context.Context, _ *nats.Msg) (*nats.Msg, error) {
		fake.options.AsyncErrorCB(nil, nil, fmt.Errorf("%w: Permissions Violation for Publish to \"denied.subject\"", nats.ErrPermissionViolation))
		<-ctx.Done()
		return nil, ctx.Err()
	}
	handler.connect = func(_ string, options ...nats.Option) (natsConnection, error) {
		fake.options = nats.GetDefaultOptions()
		for _, option := range options {
			if err := option(&fake.options); err != nil {
				return nil, err
			}
		}
		return fake, nil
	}
	if err := handler.Provision(caddy.Context{}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = handler.Cleanup() })
	recorder := httptest.NewRecorder()
	if err := handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/example", nil), caddyhttp.HandlerFunc(func(http.ResponseWriter, *http.Request) error { return nil })); err != nil {
		t.Fatal(err)
	}
	if recorder.Code != http.StatusForbidden || !strings.Contains(recorder.Body.String(), "forbidden") {
		t.Fatalf("permission response = %d %q", recorder.Code, recorder.Body.String())
	}
}

func TestHandlerCancellationWritesNoResponse(t *testing.T) {
	handler := validHandler(validRoute("example", "/example", "example", http.MethodGet))
	fake := &fakeNATSConnection{connected: true, request: func(ctx context.Context, _ *nats.Msg) (*nats.Msg, error) {
		return nil, ctx.Err()
	}}
	handler.connect = func(_ string, options ...nats.Option) (natsConnection, error) {
		fake.options = nats.GetDefaultOptions()
		for _, option := range options {
			if err := option(&fake.options); err != nil {
				return nil, err
			}
		}
		return fake, nil
	}
	if err := handler.Provision(caddy.Context{}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = handler.Cleanup() })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/example", nil).WithContext(ctx)
	if err := handler.ServeHTTP(recorder, request, caddyhttp.HandlerFunc(func(http.ResponseWriter, *http.Request) error { return nil })); err != nil {
		t.Fatal(err)
	}
	if recorder.Body.Len() != 0 || len(recorder.Header()) != 0 {
		t.Fatalf("canceled response headers/body = %v/%q", recorder.Header(), recorder.Body.String())
	}
}

func TestHandlerNegotiatesOnlyDeclaredRepresentations(t *testing.T) {
	route := validRoute("image", "/image", "image", http.MethodGet)
	route.Response.ContentType = "image/png"
	route.Response.Representations = []string{"image/webp"}
	route.Response.NegotiateAccept = true
	handler := validHandler(route)
	var requests atomic.Int32
	fake := &fakeNATSConnection{connected: true, request: func(_ context.Context, message *nats.Msg) (*nats.Msg, error) {
		requests.Add(1)
		if got := message.Header.Get("Accept"); got != "image/webp" {
			t.Fatalf("forwarded Accept = %q, want selected representation", got)
		}
		return &nats.Msg{Header: nats.Header{"Content-Type": {"image/webp"}}, Data: []byte("webp")}, nil
	}}
	handler.connect = func(_ string, options ...nats.Option) (natsConnection, error) {
		fake.options = nats.GetDefaultOptions()
		for _, option := range options {
			if err := option(&fake.options); err != nil {
				return nil, err
			}
		}
		return fake, nil
	}
	if err := handler.Provision(caddy.Context{}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = handler.Cleanup() })

	request := httptest.NewRequest(http.MethodGet, "/image", nil)
	request.Header.Add("Accept", "image/png;q=0.2")
	request.Header.Add("Accept", "image/webp;q=0.9")
	recorder := httptest.NewRecorder()
	if err := handler.ServeHTTP(recorder, request, caddyhttp.HandlerFunc(func(http.ResponseWriter, *http.Request) error { return nil })); err != nil {
		t.Fatal(err)
	}
	if recorder.Code != http.StatusOK || recorder.Header().Get("Content-Type") != "image/webp" || recorder.Header().Get("Vary") != "Accept" || recorder.Body.String() != "webp" {
		t.Fatalf("negotiated response = %d %v %q", recorder.Code, recorder.Header(), recorder.Body.String())
	}

	request = httptest.NewRequest(http.MethodGet, "/image", nil)
	request.Header.Set("Accept", "text/html")
	recorder = httptest.NewRecorder()
	if err := handler.ServeHTTP(recorder, request, caddyhttp.HandlerFunc(func(http.ResponseWriter, *http.Request) error { return nil })); err != nil {
		t.Fatal(err)
	}
	if recorder.Code != http.StatusNotAcceptable || requests.Load() != 1 {
		t.Fatalf("unacceptable response status/requests = %d/%d", recorder.Code, requests.Load())
	}
}

func TestHandlerExecutesMatchingRequestReplyRoute(t *testing.T) {
	server := natstest.RunRandClientPortServer()
	t.Cleanup(server.Shutdown)

	service, err := nats.Connect(server.ClientURL())
	if err != nil {
		t.Fatalf("connect test service to NATS: %v", err)
	}
	t.Cleanup(service.Close)
	received := make(chan *nats.Msg, 1)
	_, err = service.Subscribe("orders.*.*", func(message *nats.Msg) {
		received <- message
		reply := nats.NewMsg(message.Reply)
		reply.Data = []byte("response")
		reply.Header.Set("X-Result", "created")
		reply.Header.Set("Set-Cookie", "unsafe")
		if publishErr := service.PublishMsg(reply); publishErr != nil {
			t.Errorf("publish test service reply: %v", publishErr)
		}
	})
	if err != nil {
		t.Fatalf("subscribe test service: %v", err)
	}
	if err := service.Flush(); err != nil {
		t.Fatalf("flush test service subscription: %v", err)
	}

	handler := validHandler(validRoute("get_order", "/orders/{id}", "orders.{id}.{view}", "POST"))
	handler.NATS.URLs = []string{server.ClientURL()}
	handler.Routes[0].Parameters["view"] = Parameter{Source: "query", Name: "view", Pattern: `^[A-Za-z0-9_-]+$`}
	handler.Routes[0].RequestHeaders = []string{"Content-Type"}
	handler.Routes[0].Response.Headers = []string{"X-Result"}
	if err := handler.Provision(caddy.Context{}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = handler.Cleanup() })

	request := httptest.NewRequest(http.MethodPost, "/orders/order-42?view=summary", strings.NewReader("request"))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "secret")
	recorder := httptest.NewRecorder()
	nextCalled := false
	err = handler.ServeHTTP(recorder, request, caddyhttp.HandlerFunc(func(http.ResponseWriter, *http.Request) error { nextCalled = true; return nil }))
	if err != nil || nextCalled || recorder.Code != http.StatusOK || recorder.Body.String() != "response" {
		t.Fatalf("ServeHTTP() error/next/status/body = %v/%t/%d/%q", err, nextCalled, recorder.Code, recorder.Body.String())
	}
	if recorder.Header().Get("X-Result") != "created" || recorder.Header().Get("Set-Cookie") != "" {
		t.Fatalf("response headers = %v", recorder.Header())
	}
	select {
	case message := <-received:
		if message.Subject != "orders.order-42.summary" || string(message.Data) != "request" || message.Header.Get("Content-Type") != "application/json" {
			t.Fatalf("NATS message = subject %q data %q headers %v", message.Subject, message.Data, message.Header)
		}
		if message.Header.Get("Authorization") != "" {
			t.Fatal("NATS message forwarded Authorization header")
		}
	default:
		t.Fatal("test service did not receive NATS request")
	}
}

func TestSafeResponseHeadersRejectsInjection(t *testing.T) {
	_, err := safeResponseHeaders(nats.Header{"Content-Type": {"text/plain\r\nX-Evil: true"}}, []string{"Content-Type"})
	if err == nil {
		t.Fatal("safeResponseHeaders() accepted a line break")
	}
}
