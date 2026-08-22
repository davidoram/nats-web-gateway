package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestValidateToken(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, body string
		status     int
		wantErr    bool
	}{
		{"active", `{"active":true,"aud":["nats-gateway"],"scope":"gateway:invoke","exp":4102444800}`, 200, false},
		{"inactive", `{"active":false}`, 200, true}, {"wrong audience", `{"active":true,"aud":["other"],"scope":"gateway:invoke"}`, 200, true},
		{"wrong scope", `{"active":true,"aud":["nats-gateway"],"scope":"other"}`, 200, true}, {"expired", `{"active":true,"aud":"nats-gateway","scope":"gateway:invoke","exp":1}`, 200, true},
		{"malformed", `{`, 200, true}, {"failure", `{}`, 503, true},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if u, p, ok := r.BasicAuth(); !ok || u != "id" || p != "secret" {
					t.Error("missing client authentication")
				}
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			s := service{client: &http.Client{Timeout: time.Second}, introspectionURL: server.URL, clientID: "id", clientSecret: "secret", audience: "nats-gateway", scope: "gateway:invoke"}
			err := s.validateToken(t.Context(), "opaque-token")
			if (err != nil) != tc.wantErr {
				t.Fatalf("error = %v, wantErr %v", err, tc.wantErr)
			}
			if err != nil && strings.Contains(err.Error(), "opaque-token") {
				t.Fatal("token leaked in error")
			}
		})
	}
}

func TestValidateTokenUnavailableAndMissing(t *testing.T) {
	s := service{client: &http.Client{Timeout: 10 * time.Millisecond}, introspectionURL: "http://127.0.0.1:1", clientID: "id", clientSecret: "secret", audience: "a", scope: "s"}
	if err := s.validateToken(t.Context(), ""); err == nil {
		t.Fatal("missing token accepted")
	}
	if err := s.validateToken(t.Context(), "opaque-secret"); err == nil || strings.Contains(err.Error(), "opaque-secret") {
		t.Fatalf("unavailable error = %v", err)
	}
}
