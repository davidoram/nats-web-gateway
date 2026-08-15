//go:build integration

package credentials

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	natstest "github.com/nats-io/nats-server/v2/test"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nkeys"
)

func TestAdaptersAuthenticateAgainstNATSServer(t *testing.T) {
	t.Run("bearer token CONNECT field", func(t *testing.T) {
		natsServer := natstest.RunServer(&server.Options{Host: "127.0.0.1", Port: -1, Authorization: "opaque-token", NoLog: true})
		defer natsServer.Shutdown()
		request := httptest.NewRequest(http.MethodGet, "https://example.test/", nil)
		request.Header.Set("Authorization", "Bearer opaque-token")
		connectWithAdapter(t, natsServer.ClientURL(), Adapter{Mechanism: MechanismBearerToken}, request)
	})

	t.Run("HTTP Basic user and password", func(t *testing.T) {
		natsServer := natstest.RunServer(&server.Options{Host: "127.0.0.1", Port: -1, Users: []*server.User{{Username: "alice", Password: "correct-horse"}}, NoLog: true})
		defer natsServer.Shutdown()
		request := httptest.NewRequest(http.MethodGet, "https://example.test/", nil)
		request.SetBasicAuth("alice", "correct-horse")
		connectWithAdapter(t, natsServer.ClientURL(), Adapter{Mechanism: MechanismUserPassword}, request)
	})

	t.Run("NKey nonce proof", func(t *testing.T) {
		keyPair, err := nkeys.CreateUser()
		if err != nil {
			t.Fatal(err)
		}
		defer keyPair.Wipe()
		publicKey, err := keyPair.PublicKey()
		if err != nil {
			t.Fatal(err)
		}
		natsServer := natstest.RunServer(&server.Options{Host: "127.0.0.1", Port: -1, Nkeys: []*server.NkeyUser{{Nkey: publicKey}}, NoLog: true})
		defer natsServer.Shutdown()
		request := httptest.NewRequest(http.MethodGet, "https://example.test/", nil)
		request = request.WithContext(WithNKeyProof(request.Context(), NKeyProof{PublicKey: publicKey, Sign: keyPair.Sign}))
		connectWithAdapter(t, natsServer.ClientURL(), Adapter{Mechanism: MechanismNKey}, request)
	})
}

func connectWithAdapter(t *testing.T, url string, adapter Adapter, request *http.Request) {
	t.Helper()
	options, err := adapter.Options(request)
	if err != nil {
		t.Fatalf("adapt credentials: %v", err)
	}
	options = append(options, nats.Timeout(2*time.Second), nats.NoReconnect())
	connection, err := nats.Connect(url, options...)
	if err != nil {
		t.Fatalf("connect with adapted credential: %v", err)
	}
	connection.Close()
}
