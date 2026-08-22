//go:build integration

package integration_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
)

const (
	authCaddyURL        = "http://127.0.0.1:28080"
	hydraPublicURL      = "http://127.0.0.1:14444"
	hydraAdminURL       = "http://127.0.0.1:14445"
	fixtureClientID     = "gateway-fixture"
	fixtureClientSecret = "local-client-secret"
)

var authIntegrationHTTPClient = &http.Client{Timeout: 2 * time.Second}

func TestHydraAuthCallout(t *testing.T) {
	waitHTTP(t, hydraAdminURL+"/health/ready", 30*time.Second)
	token := requestToken(t, fixtureClientID, fixtureClientSecret, "gateway:invoke", "nats-gateway")
	waitProtected(t, token, 30*time.Second)

	t.Run("real opaque token succeeds without forwarding Authorization", func(t *testing.T) {
		status, body := protected(t, "Bearer "+token)
		if status != http.StatusOK || body != "hydra authorized" {
			t.Fatalf("response = %d %q", status, body)
		}
	})

	t.Run("Hydra audience and scope are enforced", func(t *testing.T) {
		wrongAudience := requestToken(t, "wrong-audience-fixture", "local-wrong-audience-secret", "gateway:invoke", "other-audience")
		wrongScope := requestToken(t, "wrong-scope-fixture", "local-wrong-scope-secret", "other:scope", "nats-gateway")
		for name, credential := range map[string]string{"wrong audience": wrongAudience, "insufficient scope": wrongScope} {
			t.Run(name, func(t *testing.T) {
				status, body := protected(t, "Bearer "+credential)
				if status != http.StatusUnauthorized || body != "{\"error\":\"unauthorized\"}\n" {
					t.Fatalf("response = %d %q", status, body)
				}
			})
		}
	})

	t.Run("issued NATS permissions are least privilege", func(t *testing.T) {
		errorsSeen := make(chan error, 2)
		nc, err := nats.Connect("nats://127.0.0.1:24222", nats.Token(token), nats.Timeout(2*time.Second), nats.ErrorHandler(func(_ *nats.Conn, _ *nats.Subscription, err error) { errorsSeen <- err }))
		if err != nil {
			t.Fatal(err)
		}
		defer nc.Close()
		if err := nc.Publish("outside.grant", []byte("denied")); err != nil {
			t.Fatal(err)
		}
		if _, err := nc.SubscribeSync("outside.grant"); err != nil {
			t.Fatal(err)
		}
		_ = nc.FlushTimeout(time.Second)
		for range 2 {
			select {
			case <-errorsSeen:
			case <-time.After(2 * time.Second):
				t.Fatal("missing NATS permission violation")
			}
		}
	})

	t.Run("credential and authorization failures are minimal and fail closed", func(t *testing.T) {
		oversized := "Bearer " + strings.Repeat("x", 4097)
		for _, tc := range []struct{ name, value string }{{"missing", ""}, {"malformed", "Basic abc"}, {"oversized", oversized}, {"unknown", "Bearer random-opaque-token"}} {
			t.Run(tc.name, func(t *testing.T) {
				status, body := protected(t, tc.value)
				if status != http.StatusUnauthorized || body != "{\"error\":\"unauthorized\"}\n" {
					t.Fatalf("response = %d %q", status, body)
				}
			})
		}
	})

	t.Run("revoked token is inactive", func(t *testing.T) {
		form := url.Values{"token": {token}}
		req, _ := http.NewRequest(http.MethodPost, hydraPublicURL+"/oauth2/revoke", strings.NewReader(form.Encode()))
		req.SetBasicAuth(fixtureClientID, fixtureClientSecret)
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("revoke status = %d", resp.StatusCode)
		}
		// NATS authenticates at connection establishment. Wait for the fixture's
		// deliberately short bounded connection lifetime before reconnecting.
		time.Sleep(300 * time.Millisecond)
		status, body := protected(t, "Bearer "+token)
		if status != http.StatusUnauthorized || strings.Contains(body, token) {
			t.Fatalf("response = %d %q", status, body)
		}
	})
}

func requestToken(t *testing.T, clientID, clientSecret, scope, audience string) string {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	client := &http.Client{Timeout: 2 * time.Second}
	for time.Now().Before(deadline) {
		form := url.Values{"grant_type": {"client_credentials"}, "scope": {scope}, "audience": {audience}}
		req, _ := http.NewRequest(http.MethodPost, hydraPublicURL+"/oauth2/token", strings.NewReader(form.Encode()))
		req.SetBasicAuth(clientID, clientSecret)
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		resp, err := client.Do(req)
		if err == nil {
			var result struct {
				AccessToken string `json:"access_token"`
			}
			decodeErr := json.NewDecoder(io.LimitReader(resp.Body, 16<<10)).Decode(&result)
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK && decodeErr == nil && result.AccessToken != "" {
				return result.AccessToken
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("timed out waiting for Hydra CLI client creation and token issuance")
	return ""
}

func protected(t *testing.T, authorization string) (int, string) {
	t.Helper()
	status, body, err := protectedResponse(authorization)
	if err != nil {
		t.Fatal(err)
	}
	return status, body
}

func protectedResponse(authorization string) (int, string, error) {
	req, _ := http.NewRequest(http.MethodPost, authCaddyURL+"/protected", strings.NewReader("hello"))
	if authorization != "" {
		req.Header.Set("Authorization", authorization)
	}
	resp, err := authIntegrationHTTPClient.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 2048))
	if err != nil {
		return 0, "", err
	}
	return resp.StatusCode, string(body), nil
}

func waitHTTP(t *testing.T, target string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := authIntegrationHTTPClient.Get(target)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode < 500 {
				return
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", target)
}

func waitProtected(t *testing.T, token string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		status, body, err := protectedResponse("Bearer " + token)
		if err == nil && status == http.StatusOK && body == "hydra authorized" {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("timed out waiting for the authenticated gateway path")
}
