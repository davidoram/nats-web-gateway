package natswebgateway

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp"
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
