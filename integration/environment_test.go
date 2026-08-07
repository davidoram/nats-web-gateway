//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
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
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, envOrDefault("CADDY_URL", defaultCaddyURL)+"/health", nil)
		if err != nil {
			t.Fatalf("create Caddy health request: %v", err)
		}
		response, err := doWithRetry(req, 30*time.Second)
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

func doWithRetry(request *http.Request, timeout time.Duration) (*http.Response, error) {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		response, err := http.DefaultClient.Do(request.Clone(request.Context()))
		if err == nil {
			return response, nil
		}
		lastErr = err
		time.Sleep(250 * time.Millisecond)
	}
	return nil, lastErr
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
