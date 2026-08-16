package natswebgateway

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp"
	natstest "github.com/nats-io/nats-server/v2/test"
	"github.com/nats-io/nats.go"
)

func TestHandlerCoreSSEStreamsEnforcesQuotaAndCancels(t *testing.T) {
	server := natstest.RunRandClientPortServer()
	t.Cleanup(server.Shutdown)
	publisher, err := nats.Connect(server.ClientURL())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(publisher.Close)
	baselineSubscriptions := server.NumSubscriptions()

	route := validRoute("events", "/events", "events.live", http.MethodGet)
	route.StreamMode = streamModeCoreSSE
	route.CoreSSE = &CoreSSE{
		BufferMessages: 2, BufferBytes: 128, HeartbeatInterval: caddy.Duration(20 * time.Millisecond),
		MaxDuration: caddy.Duration(time.Second), MaxConnections: 1,
	}
	handler := validHandler(route)
	handler.NATS.URLs = []string{server.ClientURL()}
	if err := handler.Provision(caddy.Context{}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = handler.Cleanup() })
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = handler.ServeHTTP(w, r, caddyhttp.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) error {
			http.NotFound(w, r)
			return nil
		}))
	}))
	t.Cleanup(httpServer.Close)

	ctx, cancel := context.WithCancel(context.Background())
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, httpServer.URL+"/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(response.Body)
	if line, readErr := reader.ReadString('\n'); readErr != nil || line != ": connected\n" {
		t.Fatalf("prelude = %q, %v", line, readErr)
	}
	_, _ = reader.ReadString('\n')

	quotaResponse, err := http.Get(httpServer.URL + "/events")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, quotaResponse.Body)
	quotaResponse.Body.Close()
	if quotaResponse.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("quota status = %d", quotaResponse.StatusCode)
	}

	if err := publisher.Publish("events.live", []byte("hello\nworld")); err != nil {
		t.Fatal(err)
	}
	if err := publisher.Flush(); err != nil {
		t.Fatal(err)
	}
	var event strings.Builder
	for {
		line, readErr := reader.ReadString('\n')
		if readErr != nil {
			t.Fatal(readErr)
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
		t.Fatalf("event = %q", event.String())
	}
	cancel()
	response.Body.Close()
	deadline := time.Now().Add(time.Second)
	for server.NumSubscriptions() != baselineSubscriptions && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := server.NumSubscriptions(); got != baselineSubscriptions {
		t.Fatalf("subscriptions after cancellation = %d, want baseline %d", got, baselineSubscriptions)
	}
}

func TestHandlerCoreSSEHeartbeatsAndMaximumDuration(t *testing.T) {
	server := natstest.RunRandClientPortServer()
	t.Cleanup(server.Shutdown)
	route := validRoute("events", "/events", "events.live", http.MethodGet)
	route.StreamMode = streamModeCoreSSE
	route.CoreSSE = &CoreSSE{
		BufferMessages: 1, BufferBytes: 16, HeartbeatInterval: caddy.Duration(10 * time.Millisecond),
		MaxDuration: caddy.Duration(50 * time.Millisecond), MaxConnections: 1,
	}
	handler := validHandler(route)
	handler.NATS.URLs = []string{server.ClientURL()}
	if err := handler.Provision(caddy.Context{}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = handler.Cleanup() })
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/events", nil)
	if err := handler.ServeHTTP(recorder, request, caddyhttp.HandlerFunc(func(http.ResponseWriter, *http.Request) error { return nil })); err != nil {
		t.Fatal(err)
	}
	body := recorder.Body.String()
	if !strings.Contains(body, ": heartbeat\n\n") || !strings.HasSuffix(body, "event: close\ndata: maximum duration reached\n\n") {
		t.Fatalf("stream body = %q", body)
	}
}

func TestCoreSSEBufferEnforcesMessageAndByteLimits(t *testing.T) {
	buffer := newCoreSSEBuffer(1, 4)
	buffer.enqueue(&nats.Msg{Data: []byte("four")})
	buffer.enqueue(&nats.Msg{Data: []byte("next")})
	select {
	case <-buffer.overflow:
	default:
		t.Fatal("message overflow was not signaled")
	}
	message := <-buffer.messages
	buffer.release(message)
	if buffer.bytes != 0 {
		t.Fatalf("buffered bytes = %d, want 0", buffer.bytes)
	}

	oversized := newCoreSSEBuffer(2, 3)
	oversized.enqueue(&nats.Msg{Data: []byte("four")})
	select {
	case <-oversized.overflow:
	default:
		t.Fatal("byte overflow was not signaled")
	}
}

func TestCoreSSEConfigurationValidation(t *testing.T) {
	valid := func() Route {
		route := validRoute("events", "/events", "events.live", http.MethodGet)
		route.StreamMode = streamModeCoreSSE
		route.CoreSSE = &CoreSSE{
			BufferMessages: 8, BufferBytes: 4096, HeartbeatInterval: caddy.Duration(time.Second),
			MaxDuration: caddy.Duration(time.Minute), MaxConnections: 4,
		}
		return route
	}
	if err := valid().validate(); err != nil {
		t.Fatalf("valid core SSE route: %v", err)
	}
	tests := []struct {
		name string
		edit func(*Route)
	}{
		{name: "missing policy", edit: func(route *Route) { route.CoreSSE = nil }},
		{name: "message bound", edit: func(route *Route) { route.CoreSSE.BufferMessages = 0 }},
		{name: "byte bound", edit: func(route *Route) { route.CoreSSE.BufferBytes = 0 }},
		{name: "heartbeat", edit: func(route *Route) { route.CoreSSE.HeartbeatInterval = 0 }},
		{name: "duration", edit: func(route *Route) { route.CoreSSE.MaxDuration = 0 }},
		{name: "heartbeat exceeds duration", edit: func(route *Route) { route.CoreSSE.HeartbeatInterval = route.CoreSSE.MaxDuration }},
		{name: "quota", edit: func(route *Route) { route.CoreSSE.MaxConnections = 0 }},
		{name: "jetstream unavailable", edit: func(route *Route) { route.StreamMode = streamModeJetStreamSSE; route.CoreSSE = nil }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			route := valid()
			test.edit(&route)
			if err := route.validate(); err == nil {
				t.Fatal("validate succeeded")
			}
		})
	}
}

func TestCaddyfileParsesCoreSSEPolicy(t *testing.T) {
	var handler Handler
	err := handler.UnmarshalCaddyfile(caddyfile.NewTestDispenser(`nats_web_gateway {
  nats_urls nats://127.0.0.1:4222
  connect_timeout 5s
  reconnect_wait 1s
  max_reconnects -1
  drain_timeout 30s
  route events {
    path /events
    methods GET
    subject events.live
    timeout 2s
    max_request_body_bytes 1024
    max_reply_bytes 2048
    response_mode binary
    response_content_type application/octet-stream
    stream_mode core_sse
    core_sse_buffer_messages 16
    core_sse_buffer_bytes 32768
    core_sse_heartbeat_interval 10s
    core_sse_max_duration 5m
    core_sse_max_connections 20
  }
}`))
	if err != nil {
		t.Fatal(err)
	}
	got := handler.Routes[0].CoreSSE
	if got == nil || got.BufferMessages != 16 || got.BufferBytes != 32768 || time.Duration(got.HeartbeatInterval) != 10*time.Second || time.Duration(got.MaxDuration) != 5*time.Minute || got.MaxConnections != 20 {
		t.Fatalf("core SSE policy = %+v", got)
	}
}
