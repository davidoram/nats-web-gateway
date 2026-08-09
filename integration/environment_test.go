//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/micro"
)

const (
	defaultCaddyURL = "http://127.0.0.1:18080"
	defaultNATSURL  = "nats://127.0.0.1:14222"
)

func TestLocalEnvironment(t *testing.T) {
	nc := connectWithRetry(t, 30*time.Second)
	t.Cleanup(nc.Close)
	waitForService(t, nc, 30*time.Second)

	t.Run("Caddy loads the gateway module", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		req, err := http.NewRequest(http.MethodGet, envOrDefault("CADDY_URL", defaultCaddyURL)+"/health", nil)
		if err != nil {
			t.Fatalf("create Caddy health request: %v", err)
		}
		response, err := doWithRetry(ctx, http.DefaultClient, req, 2*time.Second)
		if err != nil {
			t.Fatalf("request Caddy health endpoint: %v", err)
		}
		defer response.Body.Close()
		body, err := io.ReadAll(io.LimitReader(response.Body, 1024))
		if err != nil {
			t.Fatalf("read Caddy health response: %v", err)
		}
		if response.StatusCode != http.StatusOK || string(body) != "ready\n" {
			t.Fatalf("Caddy health response = %d %q, want 200 %q", response.StatusCode, body, "ready\\n")
		}
	})

	t.Run("ADR-32 endpoint replies and reports errors", func(t *testing.T) {
		response, err := nc.Request("demo.echo", []byte("hello"), 5*time.Second)
		if err != nil {
			t.Fatalf("request echo endpoint: %v", err)
		}
		if string(response.Data) != "hello" {
			t.Fatalf("echo response = %q, want hello", response.Data)
		}

		response, err = nc.Request("demo.echo", []byte("error"), 5*time.Second)
		if err != nil {
			t.Fatalf("request fixture error: %v", err)
		}
		if got := response.Header.Get(micro.ErrorCodeHeader); got != "4001" {
			t.Fatalf("%s = %q, want 4001", micro.ErrorCodeHeader, got)
		}
		if got := response.Header.Get(micro.ErrorHeader); got != "fixture requested an error" {
			t.Fatalf("%s = %q, want fixture error description", micro.ErrorHeader, got)
		}
	})

	t.Run("Caddy translates bounded HTTP request reply", func(t *testing.T) {
		request, err := http.NewRequest(http.MethodPost, envOrDefault("CADDY_URL", defaultCaddyURL)+"/echo/order-42?view=summary", strings.NewReader("hello over HTTP"))
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Content-Type", "text/plain")
		request.Header.Set("Authorization", "must-not-forward")
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatalf("HTTP request/reply: %v", err)
		}
		defer response.Body.Close()
		body, err := io.ReadAll(response.Body)
		if err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != http.StatusOK || string(body) != "hello over HTTP" || response.Header.Get("Content-Type") != "text/plain" {
			t.Fatalf("HTTP response = %d %q %q", response.StatusCode, body, response.Header.Get("Content-Type"))
		}
	})

	t.Run("Caddy maps protocol failures deterministically", func(t *testing.T) {
		tests := []struct {
			name, method, path, body string
			wantStatus               int
		}{
			{name: "mapped ADR-32 error", method: http.MethodPost, path: "/echo/order-42?view=summary", body: "error", wantStatus: http.StatusBadRequest},
			{name: "no responders", method: http.MethodGet, path: "/errors/no-responders", wantStatus: http.StatusServiceUnavailable},
			{name: "malformed JSON", method: http.MethodGet, path: "/errors/malformed", wantStatus: http.StatusBadGateway},
			{name: "deadline", method: http.MethodGet, path: "/errors/timeout", wantStatus: http.StatusGatewayTimeout},
		}
		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				request, err := http.NewRequest(test.method, envOrDefault("CADDY_URL", defaultCaddyURL)+test.path, strings.NewReader(test.body))
				if err != nil {
					t.Fatal(err)
				}
				response, err := http.DefaultClient.Do(request)
				if err != nil {
					t.Fatal(err)
				}
				defer response.Body.Close()
				body, err := io.ReadAll(io.LimitReader(response.Body, 2048))
				if err != nil {
					t.Fatal(err)
				}
				if response.StatusCode != test.wantStatus || response.Header.Get("Content-Type") != "application/json" || !strings.Contains(string(body), `"error"`) {
					t.Fatalf("failure response = %d %q %q", response.StatusCode, response.Header.Get("Content-Type"), body)
				}
			})
		}
	})

	t.Run("ADR-32 discovery is available", func(t *testing.T) {
		response, err := nc.Request("$SRV.PING", nil, 5*time.Second)
		if err != nil {
			t.Fatalf("request service ping: %v", err)
		}
		var ping micro.Ping
		if err := json.Unmarshal(response.Data, &ping); err != nil {
			t.Fatalf("decode service ping: %v", err)
		}
		if ping.Name != "Echo" || ping.Version != "0.1.0" || ping.Type != micro.PingResponseType {
			t.Fatalf("unexpected service ping: %+v", ping)
		}
	})

	t.Run("fixture credentials enforce least privilege", func(t *testing.T) {
		_, err := nats.Connect(envOrDefault("NATS_URL", defaultNATSURL), nats.UserInfo("gateway", "wrong-password"), nats.Timeout(2*time.Second))
		if !errors.Is(err, nats.ErrAuthorization) {
			t.Fatalf("wrong password error = %v, want authorization violation", err)
		}

		permissionErrors := make(chan error, 1)
		denied, err := nats.Connect(envOrDefault("NATS_URL", defaultNATSURL),
			nats.UserInfo("gateway", "local-gateway-only"), nats.Timeout(2*time.Second),
			nats.ErrorHandler(func(_ *nats.Conn, _ *nats.Subscription, err error) {
				select {
				case permissionErrors <- err:
				default:
				}
			}))
		if err != nil {
			t.Fatalf("connect permission test client: %v", err)
		}
		defer denied.Close()
		if _, err := denied.SubscribeSync("demo.echo"); err != nil {
			t.Fatalf("create denied subscription: %v", err)
		}
		if err := denied.FlushTimeout(2 * time.Second); err != nil && !strings.Contains(err.Error(), "Permissions Violation") {
			t.Fatalf("flush denied subscription: %v", err)
		}
		select {
		case err := <-permissionErrors:
			if !strings.Contains(err.Error(), "Permissions Violation") {
				t.Fatalf("unauthorized subscription error = %v, want permissions violation", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for subscription permission violation")
		}
	})
}

func waitForService(t *testing.T, nc *nats.Conn, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		response, err := nc.Request("$SRV.PING", nil, 500*time.Millisecond)
		if err == nil && len(response.Data) > 0 {
			return
		}
		lastErr = err
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("wait for ADR-32 service readiness: %v", lastErr)
}

func doWithRetry(ctx context.Context, client *http.Client, request *http.Request, attemptTimeout time.Duration) (*http.Response, error) {
	var lastErr error
	for {
		if err := ctx.Err(); err != nil {
			return nil, errors.Join(err, lastErr)
		}
		attemptCtx, cancel := context.WithTimeout(ctx, attemptTimeout)
		response, err := client.Do(request.Clone(attemptCtx))
		cancel()
		if err == nil {
			return response, nil
		}
		if response != nil {
			response.Body.Close()
		}
		lastErr = err
		timer := time.NewTimer(250 * time.Millisecond)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return nil, errors.Join(ctx.Err(), lastErr)
		case <-timer.C:
		}
	}
}

func TestDoWithRetryUsesFreshAttemptContexts(t *testing.T) {
	t.Parallel()

	var attempts atomic.Int32
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.Context().Err() != nil {
			return nil, request.Context().Err()
		}
		if attempts.Add(1) < 3 {
			return nil, errors.New("not ready")
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader("ready\n")),
			Header:     make(http.Header),
			Request:    request,
		}, nil
	})}
	request, err := http.NewRequest(http.MethodGet, "http://example.test/health", nil)
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	response, err := doWithRetry(ctx, client, request, 100*time.Millisecond)
	if err != nil {
		t.Fatalf("retry request: %v", err)
	}
	response.Body.Close()
	if got := attempts.Load(); got != 3 {
		t.Fatalf("attempts = %d, want 3", got)
	}
}

func TestDoWithRetryHonorsOverallDeadline(t *testing.T) {
	t.Parallel()

	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		<-request.Context().Done()
		return nil, request.Context().Err()
	})}
	request, err := http.NewRequest(http.MethodGet, "http://example.test/health", nil)
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_, err = doWithRetry(ctx, client, request, time.Second)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("retry error = %v, want deadline exceeded", err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (roundTrip roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	response, err := roundTrip(request)
	if err != nil {
		return nil, fmt.Errorf("round trip: %w", err)
	}
	return response, nil
}

func connectWithRetry(t *testing.T, timeout time.Duration) *nats.Conn {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		nc, err := nats.Connect(envOrDefault("NATS_URL", defaultNATSURL),
			nats.UserInfo("gateway", "local-gateway-only"), nats.Name("integration-test"), nats.Timeout(time.Second))
		if err == nil {
			return nc
		}
		lastErr = err
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("connect to local NATS environment: %v", lastErr)
	return nil
}

func envOrDefault(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
