//go:build integration

package integration_test

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"slices"
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
	discovery := connectDiscoveryClient(t)
	t.Cleanup(discovery.Close)
	waitForEchoService(t, discovery, 30*time.Second)

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

	t.Run("Caddy streams ephemeral Core NATS messages as SSE", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, envOrDefault("CADDY_URL", defaultCaddyURL)+"/events", nil)
		if err != nil {
			t.Fatal(err)
		}
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK || response.Header.Get("Content-Type") != "text/event-stream" {
			t.Fatalf("SSE response = %d %q", response.StatusCode, response.Header.Get("Content-Type"))
		}
		reader := bufio.NewReader(response.Body)
		if line, err := reader.ReadString('\n'); err != nil || line != ": connected\n" {
			t.Fatalf("SSE prelude = %q, %v", line, err)
		}
		if _, err := reader.ReadString('\n'); err != nil {
			t.Fatal(err)
		}
		if err := discovery.Publish("demo.events", []byte("hello\nworld")); err != nil {
			t.Fatal(err)
		}
		if err := discovery.FlushTimeout(time.Second); err != nil {
			t.Fatal(err)
		}
		var event strings.Builder
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				t.Fatal(err)
			}
			if strings.HasPrefix(line, ":") {
				continue
			}
			event.WriteString(line)
			if line == "\n" {
				break
			}
		}
		if event.String() != "data: hello\ndata: world\n\n" {
			t.Fatalf("SSE event = %q", event.String())
		}
	})

	t.Run("Caddy maps protocol failures deterministically", func(t *testing.T) {
		tests := []struct {
			name, method, path, body string
			wantStatus               int
		}{
			{name: "mapped ADR-32 error", method: http.MethodPost, path: "/echo/order-42?view=summary", body: "error", wantStatus: http.StatusBadRequest},
			{name: "no responders", method: http.MethodGet, path: "/errors/no-responders", wantStatus: http.StatusServiceUnavailable},
			{name: "publish permission", method: http.MethodGet, path: "/errors/permission", wantStatus: http.StatusForbidden},
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
		response, err := discovery.Request("$SRV.PING.Echo", nil, 5*time.Second)
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

	t.Run("Pets APIs work end to end and publish service statistics", func(t *testing.T) {
		testPetsHTTP(t, discovery)
	})

	t.Run("ADR-32 compatibility across scopes and instance lifecycle", func(t *testing.T) {
		testADR32Compatibility(t)
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

func testPetsHTTP(t *testing.T, nc *nats.Conn) {
	t.Helper()
	baseURL := envOrDefault("CADDY_URL", defaultCaddyURL)
	tests := []struct {
		name, method, path, body, wantBody string
		wantStatus                         int
	}{
		{name: "REST create", method: http.MethodPost, path: "/pets", body: `{"id":"rest-pet","name":"Milo"}`, wantStatus: 200, wantBody: `{"id":"rest-pet","name":"Milo"}`},
		{name: "REST list", method: http.MethodGet, path: "/pets", wantStatus: 200, wantBody: `[{"id":"rest-pet","name":"Milo"}]`},
		{name: "REST get", method: http.MethodGet, path: "/pets/rest-pet", wantStatus: 200, wantBody: `{"id":"rest-pet","name":"Milo"}`},
		{name: "REST update", method: http.MethodPut, path: "/pets/rest-pet", body: `{"name":"Mochi"}`, wantStatus: 200, wantBody: `{"id":"rest-pet","name":"Mochi"}`},
		{name: "REST delete", method: http.MethodDelete, path: "/pets/rest-pet", wantStatus: 200, wantBody: `{"id":"rest-pet","name":"Mochi"}`},
		{name: "REST missing", method: http.MethodGet, path: "/pets/rest-pet", wantStatus: 404},
		{name: "REST invalid ID", method: http.MethodPost, path: "/pets", body: `{"id":"pet.1","name":"Unreachable"}`, wantStatus: 400},
		{name: "RPC create", method: http.MethodPost, path: "/rpc/pets.CreatePet", body: `{"id":"rpc-pet","name":"Luna"}`, wantStatus: 200, wantBody: `{"id":"rpc-pet","name":"Luna"}`},
		{name: "RPC get", method: http.MethodPost, path: "/rpc/pets.GetPet", body: `{"id":"rpc-pet"}`, wantStatus: 200, wantBody: `{"id":"rpc-pet","name":"Luna"}`},
		{name: "RPC update", method: http.MethodPost, path: "/rpc/pets.UpdatePet", body: `{"id":"rpc-pet","name":"Nova"}`, wantStatus: 200, wantBody: `{"id":"rpc-pet","name":"Nova"}`},
		{name: "RPC list", method: http.MethodPost, path: "/rpc/pets.ListPets", body: `{}`, wantStatus: 200, wantBody: `[{"id":"rpc-pet","name":"Nova"}]`},
		{name: "RPC delete", method: http.MethodPost, path: "/rpc/pets.DeletePet", body: `{"id":"rpc-pet"}`, wantStatus: 200, wantBody: `{"id":"rpc-pet","name":"Nova"}`},
		{name: "RPC invalid", method: http.MethodPost, path: "/rpc/pets.CreatePet", body: `{"id":""}`, wantStatus: 400},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request, err := http.NewRequest(test.method, baseURL+test.path, strings.NewReader(test.body))
			if err != nil {
				t.Fatal(err)
			}
			request.Header.Set("Content-Type", "application/json")
			response, err := http.DefaultClient.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			body, err := io.ReadAll(io.LimitReader(response.Body, 65537))
			if err != nil {
				t.Fatal(err)
			}
			if response.StatusCode != test.wantStatus || response.Header.Get("Content-Type") != "application/json" {
				t.Fatalf("response = %d %q %q", response.StatusCode, response.Header.Get("Content-Type"), body)
			}
			if test.wantBody != "" && string(body) != test.wantBody {
				t.Fatalf("body = %q, want %q", body, test.wantBody)
			}
			if test.wantBody == "" && !strings.Contains(string(body), `"error"`) {
				t.Fatalf("error body = %q", body)
			}
		})
	}

	for _, name := range []string{"PetsREST", "PetsRPC"} {
		pingMessages := requestMany(t, nc, "$SRV.PING."+name, 300*time.Millisecond)
		infoMessages := requestMany(t, nc, "$SRV.INFO."+name, 300*time.Millisecond)
		if len(pingMessages) != 1 || len(infoMessages) != 1 {
			t.Fatalf("%s discovery responses: PING=%d INFO=%d, want 1 each", name, len(pingMessages), len(infoMessages))
		}
		var ping micro.Ping
		var info micro.Info
		decodeDiscovery(t, pingMessages[0], &ping)
		decodeDiscovery(t, infoMessages[0], &info)
		if ping.Type != micro.PingResponseType || ping.ID == "" || ping.Name != name || ping.Version != serviceVersionForTest ||
			info.Type != micro.InfoResponseType || info.ID != ping.ID || info.Name != name || info.Version != serviceVersionForTest ||
			!mapsEqual(info.Metadata, map[string]string{"example": "pets", "state": "ephemeral"}) || len(info.Endpoints) != 5 {
			t.Fatalf("invalid %s PING/INFO: ping=%+v info=%+v", name, ping, info)
		}
		for _, endpoint := range info.Endpoints {
			wantStyle := strings.ToLower(strings.TrimPrefix(name, "Pets"))
			if endpoint.Name == "" || endpoint.Subject == "" || endpoint.QueueGroup != "pets" ||
				!mapsEqual(endpoint.Metadata, map[string]string{"style": wantStyle, "payload": "application/json"}) {
				t.Fatalf("invalid %s endpoint INFO: %+v", name, endpoint)
			}
		}

		messages := requestMany(t, nc, "$SRV.STATS."+name, 300*time.Millisecond)
		if len(messages) != 1 {
			t.Fatalf("%s stats responses = %d, want 1", name, len(messages))
		}
		var stats micro.Stats
		decodeDiscovery(t, messages[0], &stats)
		if stats.Type != micro.StatsResponseType || stats.Name != name || stats.Version != serviceVersionForTest || stats.Started.IsZero() {
			t.Fatalf("invalid %s stats: %+v", name, stats)
		}
		var requests, errorsCount int
		var sawLastError bool
		for _, endpoint := range stats.Endpoints {
			requests += endpoint.NumRequests
			errorsCount += endpoint.NumErrors
			sawLastError = sawLastError || endpoint.LastError != ""
			if endpoint.NumRequests > 0 && (endpoint.ProcessingTime <= 0 || endpoint.AverageProcessingTime <= 0) {
				t.Fatalf("%s endpoint timing not recorded: %+v", name, endpoint)
			}
		}
		wantRequests, wantErrors := 6, 1
		if name == "PetsREST" {
			wantRequests, wantErrors = 7, 2
		}
		if requests != wantRequests || errorsCount != wantErrors || !sawLastError || stats.ID != ping.ID {
			t.Fatalf("%s aggregate stats requests=%d errors=%d last_error=%v ID=%q", name, requests, errorsCount, sawLastError, stats.ID)
		}
	}
}

const serviceVersionForTest = "1.0.0"

func testADR32Compatibility(t *testing.T) {
	t.Helper()
	connections := make([]*nats.Conn, 0, 3)
	for range 3 {
		connections = append(connections, connectDiscoveryClient(t))
	}
	for _, connection := range connections {
		t.Cleanup(connection.Close)
	}

	var handled atomic.Int64
	services := make([]micro.Service, 0, 2)
	for index := range 2 {
		service, err := micro.AddService(connections[index], micro.Config{
			Name: "CompatibilityFixture", Version: "2.3.4", Description: "ADR-32 compatibility fixture",
			Metadata: map[string]string{"owner": "integration", "schema": "v1"}, QueueGroup: "compat-workers",
		})
		if err != nil {
			t.Fatalf("add compatibility service %d: %v", index, err)
		}
		t.Cleanup(func() { _ = service.Stop() })
		if err := service.AddEndpoint("work", micro.HandlerFunc(func(request micro.Request) {
			handled.Add(1)
			if string(request.Data()) == "error" {
				_ = request.Error("4999", "intentional compatibility error", nil)
				return
			}
			_ = request.Respond([]byte(`{"ok":true}`))
		}), micro.WithEndpointSubject("compat.work"), micro.WithEndpointMetadata(map[string]string{"kind": "compatibility"})); err != nil {
			t.Fatalf("add compatibility endpoint %d: %v", index, err)
		}
		services = append(services, service)
		if err := connections[index].FlushTimeout(2 * time.Second); err != nil {
			t.Fatalf("flush compatibility service %d: %v", index, err)
		}
	}

	client := connections[2]
	allPings := collectPings(t, client, "$SRV.PING", 2)
	ids := []string{allPings[0].ID, allPings[1].ID}
	if ids[0] == ids[1] || ids[0] == "" || ids[1] == "" {
		t.Fatalf("instance IDs are not non-empty and unique: %q", ids)
	}
	slices.Sort(ids)
	for _, ping := range allPings {
		assertIdentity(t, ping.ServiceIdentity, ids)
		if ping.Type != micro.PingResponseType {
			t.Fatalf("PING type = %q", ping.Type)
		}
	}
	collectPings(t, client, "$SRV.PING.CompatibilityFixture", 2)
	for _, id := range ids {
		responses := collectPings(t, client, "$SRV.PING.CompatibilityFixture."+id, 1)
		if responses[0].ID != id {
			t.Fatalf("instance PING ID = %q, want %q", responses[0].ID, id)
		}
	}

	infos := collectInfo(t, client, "$SRV.INFO.CompatibilityFixture", 2)
	for _, info := range infos {
		assertIdentity(t, info.ServiceIdentity, ids)
		if info.Type != micro.InfoResponseType || info.Description != "ADR-32 compatibility fixture" ||
			!mapsEqual(info.Metadata, map[string]string{"owner": "integration", "schema": "v1"}) || len(info.Endpoints) != 1 {
			t.Fatalf("invalid INFO response: %+v", info)
		}
		endpoint := info.Endpoints[0]
		if endpoint.Name != "work" || endpoint.Subject != "compat.work" || endpoint.QueueGroup != "compat-workers" ||
			!mapsEqual(endpoint.Metadata, map[string]string{"kind": "compatibility"}) {
			t.Fatalf("invalid endpoint INFO: %+v", endpoint)
		}
	}
	collectInfo(t, client, "$SRV.INFO", 2)
	for _, id := range ids {
		collectInfo(t, client, "$SRV.INFO.CompatibilityFixture."+id, 1)
	}

	for i := 0; i < 5; i++ {
		if responses := requestMany(t, client, "compat.work", 150*time.Millisecond); len(responses) != 1 {
			t.Fatalf("successful endpoint request %d replies = %d, want exactly 1", i, len(responses))
		}
	}
	for i := 0; i < 2; i++ {
		responses := requestManyWithData(t, client, "compat.work", []byte("error"), 150*time.Millisecond)
		if len(responses) != 1 || responses[0].Header.Get(micro.ErrorCodeHeader) != "4999" {
			t.Fatalf("error endpoint request %d responses = %d, header = %q", i, len(responses), firstHeader(responses, micro.ErrorCodeHeader))
		}
	}
	if got := handled.Load(); got != 7 {
		t.Fatalf("handler calls = %d, want exactly 7", got)
	}

	stats := collectStats(t, client, "$SRV.STATS.CompatibilityFixture", 2)
	var requests, failures int
	var sawLastError bool
	for _, response := range stats {
		assertIdentity(t, response.ServiceIdentity, ids)
		if response.Type != micro.StatsResponseType || response.Started.IsZero() || len(response.Endpoints) != 1 {
			t.Fatalf("invalid STATS response: %+v", response)
		}
		endpoint := response.Endpoints[0]
		requests += endpoint.NumRequests
		failures += endpoint.NumErrors
		if endpoint.NumRequests > 0 && (endpoint.ProcessingTime <= 0 || endpoint.AverageProcessingTime <= 0) {
			t.Fatalf("invalid processing times: %+v", endpoint)
		}
		if strings.Contains(endpoint.LastError, "intentional compatibility error") {
			sawLastError = true
		}
	}
	if requests != 7 || failures != 2 || !sawLastError {
		t.Fatalf("aggregate stats requests=%d errors=%d last_error=%v", requests, failures, sawLastError)
	}
	collectStats(t, client, "$SRV.STATS", 2)
	for _, id := range ids {
		collectStats(t, client, "$SRV.STATS.CompatibilityFixture."+id, 1)
	}

	stoppedID := services[0].Info().ID
	if err := services[0].Stop(); err != nil {
		t.Fatalf("stop first compatibility instance: %v", err)
	}
	if err := connections[0].FlushTimeout(2 * time.Second); err != nil {
		t.Fatalf("flush stopped compatibility instance: %v", err)
	}
	remaining := collectPings(t, client, "$SRV.PING.CompatibilityFixture", 1)
	if remaining[0].ID == stoppedID {
		t.Fatalf("stopped instance %q remained discoverable", stoppedID)
	}
	if responses := requestMany(t, client, "compat.work", 150*time.Millisecond); len(responses) != 1 {
		t.Fatalf("healthy remaining instance replies = %d, want 1", len(responses))
	}
}

func connectDiscoveryClient(t *testing.T) *nats.Conn {
	t.Helper()
	nc, err := nats.Connect(envOrDefault("NATS_URL", defaultNATSURL),
		nats.UserInfo("discovery_test", "local-discovery-only"), nats.Name("ADR-32 compatibility test"), nats.Timeout(2*time.Second))
	if err != nil {
		t.Fatalf("connect ADR-32 compatibility client: %v", err)
	}
	return nc
}

func requestMany(t *testing.T, nc *nats.Conn, subject string, quiet time.Duration) []*nats.Msg {
	t.Helper()
	return requestManyWithData(t, nc, subject, nil, quiet)
}

func requestManyWithData(t *testing.T, nc *nats.Conn, subject string, data []byte, quiet time.Duration) []*nats.Msg {
	t.Helper()
	inbox := nats.NewInbox()
	subscription, err := nc.SubscribeSync(inbox)
	if err != nil {
		t.Fatalf("subscribe discovery inbox: %v", err)
	}
	defer subscription.Unsubscribe()
	if err := nc.PublishRequest(subject, inbox, data); err != nil {
		t.Fatalf("publish request to %s: %v", subject, err)
	}
	if err := nc.FlushTimeout(2 * time.Second); err != nil {
		t.Fatalf("flush request to %s: %v", subject, err)
	}
	first, err := subscription.NextMsg(2 * time.Second)
	if err != nil {
		t.Fatalf("wait for first response from %s: %v", subject, err)
	}
	responses := []*nats.Msg{first}
	for {
		response, err := subscription.NextMsg(quiet)
		if errors.Is(err, nats.ErrTimeout) {
			return responses
		}
		if err != nil {
			t.Fatalf("collect responses from %s: %v", subject, err)
		}
		responses = append(responses, response)
	}
}

func collectPings(t *testing.T, nc *nats.Conn, subject string, want int) []micro.Ping {
	t.Helper()
	var responses []micro.Ping
	for _, message := range requestMany(t, nc, subject, 300*time.Millisecond) {
		var response micro.Ping
		decodeDiscovery(t, message, &response)
		if response.Name == "CompatibilityFixture" {
			responses = append(responses, response)
		}
	}
	if len(responses) != want {
		t.Fatalf("%s compatibility PING responses = %d, want %d", subject, len(responses), want)
	}
	return responses
}

func collectInfo(t *testing.T, nc *nats.Conn, subject string, want int) []micro.Info {
	t.Helper()
	var responses []micro.Info
	for _, message := range requestMany(t, nc, subject, 300*time.Millisecond) {
		var response micro.Info
		decodeDiscovery(t, message, &response)
		if response.Name == "CompatibilityFixture" {
			responses = append(responses, response)
		}
	}
	if len(responses) != want {
		t.Fatalf("%s compatibility INFO responses = %d, want %d", subject, len(responses), want)
	}
	return responses
}

func collectStats(t *testing.T, nc *nats.Conn, subject string, want int) []micro.Stats {
	t.Helper()
	var responses []micro.Stats
	for _, message := range requestMany(t, nc, subject, 300*time.Millisecond) {
		var response micro.Stats
		decodeDiscovery(t, message, &response)
		if response.Name == "CompatibilityFixture" {
			responses = append(responses, response)
		}
	}
	if len(responses) != want {
		t.Fatalf("%s compatibility STATS responses = %d, want %d", subject, len(responses), want)
	}
	return responses
}

func decodeDiscovery(t *testing.T, message *nats.Msg, target any) {
	t.Helper()
	if err := json.Unmarshal(message.Data, target); err != nil {
		t.Fatalf("decode discovery response %q: %v", message.Data, err)
	}
}

func assertIdentity(t *testing.T, identity micro.ServiceIdentity, wantIDs []string) {
	t.Helper()
	if identity.Name != "CompatibilityFixture" || identity.Version != "2.3.4" ||
		!mapsEqual(identity.Metadata, map[string]string{"owner": "integration", "schema": "v1"}) ||
		!slices.Contains(wantIDs, identity.ID) {
		t.Fatalf("invalid service identity: %+v", identity)
	}
}

func mapsEqual(left, right map[string]string) bool {
	if len(left) != len(right) {
		return false
	}
	for key, value := range left {
		if right[key] != value {
			return false
		}
	}
	return true
}

func firstHeader(messages []*nats.Msg, name string) string {
	if len(messages) == 0 {
		return ""
	}
	return messages[0].Header.Get(name)
}

func waitForEchoService(t *testing.T, nc *nats.Conn, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		response, err := nc.Request("$SRV.PING.Echo", nil, 500*time.Millisecond)
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
