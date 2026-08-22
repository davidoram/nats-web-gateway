// Command auth-callout is a test-only NATS Auth Callout service backed by
// OAuth2 token introspection. It deliberately treats bearer tokens as opaque.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nkeys"
)

const authSubject = "$SYS.REQ.USER.AUTH"

type service struct {
	client                                                    *http.Client
	introspectionURL, clientID, clientSecret, audience, scope string
	signer                                                    nkeys.KeyPair
}

type introspection struct {
	Active   bool   `json:"active"`
	Audience any    `json:"aud"`
	Scope    string `json:"scope"`
	Expires  int64  `json:"exp"`
}

func main() {
	signer, err := nkeys.FromSeed([]byte(mustEnv("AUTH_CALLOUT_ISSUER_SEED")))
	if err != nil {
		fatal("load signing key", err)
	}
	s := &service{client: &http.Client{Timeout: 750 * time.Millisecond}, introspectionURL: mustEnv("HYDRA_INTROSPECTION_URL"), clientID: mustEnv("HYDRA_CLIENT_ID"), clientSecret: mustEnv("HYDRA_CLIENT_SECRET"), audience: mustEnv("HYDRA_AUDIENCE"), scope: mustEnv("HYDRA_SCOPE"), signer: signer}
	nc, err := nats.Connect(mustEnv("NATS_URL"), nats.UserInfo(mustEnv("NATS_USER"), mustEnv("NATS_PASSWORD")), nats.Timeout(5*time.Second))
	if err != nil {
		fatal("connect to NATS", err)
	}
	defer nc.Close()
	if _, err := nc.Subscribe(authSubject, s.authorize); err != nil {
		fatal("subscribe", err)
	}
	if err := nc.FlushTimeout(5 * time.Second); err != nil {
		fatal("flush subscription", err)
	}
	if err := os.WriteFile("/tmp/auth-callout-ready", []byte("ready\n"), 0o600); err != nil {
		fatal("write readiness marker", err)
	}
	select {}
}

func (s *service) authorize(msg *nats.Msg) {
	request, err := jwt.DecodeAuthorizationRequestClaims(string(msg.Data))
	if err != nil {
		return
	}
	response := jwt.NewAuthorizationResponseClaims(request.UserNkey)
	response.Audience = request.Server.ID
	response.Expires = time.Now().Add(5 * time.Second).Unix()
	if err := s.validateToken(context.Background(), request.ConnectOptions.Token); err != nil {
		response.Error = "authentication rejected"
	} else {
		user := jwt.NewUserClaims(request.UserNkey)
		user.Audience = "$G"
		user.Expires = time.Now().Add(time.Minute).Unix()
		user.Permissions.Pub.Allow.Add("auth.echo")
		user.Permissions.Sub.Allow.Add("_INBOX.>")
		response.Jwt, err = user.Encode(s.signer)
		if err != nil {
			response.Error = "authorization response unavailable"
		}
	}
	encoded, err := response.Encode(s.signer)
	if err == nil {
		_ = msg.Respond([]byte(encoded))
	}
}

func (s *service) validateToken(ctx context.Context, token string) error {
	if token == "" {
		return errors.New("missing token")
	}
	form := url.Values{"token": {token}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.introspectionURL, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.SetBasicAuth(s.clientID, s.clientSecret)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := s.client.Do(req)
	if err != nil {
		return errors.New("introspection unavailable")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return errors.New("introspection rejected")
	}
	var result introspection
	if err := json.NewDecoder(io.LimitReader(resp.Body, 16<<10)).Decode(&result); err != nil {
		return errors.New("invalid introspection response")
	}
	if !result.Active || (result.Expires != 0 && result.Expires <= time.Now().Unix()) || !containsAudience(result.Audience, s.audience) || !containsWord(result.Scope, s.scope) {
		return errors.New("inactive or insufficient token")
	}
	return nil
}

func containsAudience(value any, wanted string) bool {
	switch v := value.(type) {
	case string:
		return v == wanted
	case []any:
		for _, item := range v {
			if item == wanted {
				return true
			}
		}
	}
	return false
}
func containsWord(value, wanted string) bool {
	for _, word := range strings.Fields(value) {
		if word == wanted {
			return true
		}
	}
	return false
}
func mustEnv(name string) string {
	value := os.Getenv(name)
	if value == "" {
		fatal("missing environment variable", errors.New(name))
	}
	return value
}
func fatal(message string, err error) {
	_, _ = fmt.Fprintf(os.Stderr, "%s: %v\n", message, err)
	os.Exit(1)
}
