package natswebgateway

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/nats-io/nats.go"
)

func TestSelectRepresentation(t *testing.T) {
	t.Parallel()
	response := Response{
		Mode:            responseModeBinary,
		ContentType:     "image/png",
		Representations: []string{"image/webp", "application/octet-stream"},
		NegotiateAccept: true,
	}
	tests := []struct {
		name, accept, want string
		wantError          bool
	}{
		{name: "absent uses operator preference", want: "image/png"},
		{name: "exact", accept: "image/webp", want: "image/webp"},
		{name: "quality", accept: "image/png;q=0.4, image/webp;q=0.9", want: "image/webp"},
		{name: "media wildcard", accept: "image/*", want: "image/png"},
		{name: "global wildcard", accept: "*/*", want: "image/png"},
		{name: "specific exclusion overrides wildcard", accept: "image/*;q=0.8, image/png;q=0", want: "image/webp"},
		{name: "declaration breaks quality tie", accept: "image/png, image/webp", want: "image/png"},
		{name: "undeclared", accept: "text/html", wantError: true},
		{name: "all excluded", accept: "*/*;q=0", wantError: true},
		{name: "malformed quality", accept: "image/png;q=2", wantError: true},
		{name: "overprecise quality", accept: "image/png;q=0.1234", wantError: true},
		{name: "scientific quality", accept: "image/png;q=1e-1", wantError: true},
		{name: "invalid wildcard", accept: "*/png", wantError: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := selectRepresentation(test.accept, response)
			if (err != nil) != test.wantError || got != test.want {
				t.Fatalf("selectRepresentation(%q) = %q, %v; want %q, error=%t", test.accept, got, err, test.want, test.wantError)
			}
		})
	}
}

func TestValidateReply(t *testing.T) {
	t.Parallel()
	response := Response{Mode: responseModeJSON, ContentType: "application/json", ServiceErrorStatuses: map[string]int{"4001": http.StatusConflict}}
	tests := []struct {
		name       string
		reply      *nats.Msg
		wantStatus int
		wantError  bool
	}{
		{name: "valid JSON", reply: &nats.Msg{Data: []byte(`{"ok":true}`)}, wantStatus: http.StatusOK},
		{name: "malformed JSON", reply: &nats.Msg{Data: []byte(`{"ok":`)}, wantError: true},
		{name: "mapped ADR-32 error", reply: replyWithHeaders("4001", "order conflict"), wantStatus: http.StatusConflict},
		{name: "unmapped ADR-32 error", reply: replyWithHeaders("4999", "private detail"), wantStatus: http.StatusBadGateway},
		{name: "missing ADR-32 description", reply: replyWithHeaders("4001", ""), wantError: true},
		{name: "malformed ADR-32 code", reply: replyWithHeaders("04", "bad"), wantError: true},
		{name: "wrong content type", reply: &nats.Msg{Header: nats.Header{"Content-Type": {"text/html"}}, Data: []byte(`{}`)}, wantError: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			status, _, err := validateReply(test.reply, response, "application/json")
			if status != test.wantStatus || (err != nil) != test.wantError {
				t.Fatalf("validateReply() = %d, %v; want %d, error=%t", status, err, test.wantStatus, test.wantError)
			}
		})
	}
}

func TestWriteGatewayErrorIsStableAndNonSensitive(t *testing.T) {
	recorder := httptest.NewRecorder()
	writeGatewayError(recorder, http.StatusBadGateway, "upstream service error")
	if recorder.Code != http.StatusBadGateway || recorder.Header().Get("Content-Type") != "application/json" || recorder.Body.String() != "{\"error\":\"upstream service error\"}\n" {
		t.Fatalf("gateway error response = %d %v %q", recorder.Code, recorder.Header(), recorder.Body.String())
	}
}

func replyWithHeaders(code, description string) *nats.Msg {
	header := nats.Header{}
	header.Set("Nats-Service-Error-Code", code)
	if description != "" {
		header.Set("Nats-Service-Error", description)
	}
	return &nats.Msg{Header: header}
}

func FuzzParseAccept(f *testing.F) {
	for _, seed := range []string{"application/json", "image/*;q=0.5", "*/*", "text/html;q=2", "\""} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, value string) { _, _ = parseAccept(value) })
}
