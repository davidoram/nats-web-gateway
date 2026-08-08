package natswebgateway

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp"
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
	drains    atomic.Int32
	closes    atomic.Int32
}

func (connection *fakeNATSConnection) IsConnected() bool { return connection.connected }

func (connection *fakeNATSConnection) Drain() error {
	connection.drains.Add(1)
	connection.connected = false
	connection.options.ClosedCB(nil)
	return nil
}

func (connection *fakeNATSConnection) Close() {
	connection.closes.Add(1)
	connection.connected = false
	connection.options.ClosedCB(nil)
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

type nonClosingDrainConnection struct{ *fakeNATSConnection }

func (connection *nonClosingDrainConnection) Drain() error {
	connection.drains.Add(1)
	return nil
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
