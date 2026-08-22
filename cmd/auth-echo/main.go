// Command auth-echo is the least-privilege application fixture for OSS-020.
package main

import (
	"os"
	"time"

	"github.com/nats-io/nats.go"
)

func main() {
	nc, err := nats.Connect(os.Getenv("NATS_URL"), nats.UserInfo(os.Getenv("NATS_USER"), os.Getenv("NATS_PASSWORD")), nats.Timeout(5*time.Second))
	if err != nil {
		panic(err)
	}
	defer nc.Close()
	if _, err := nc.Subscribe("auth.echo", func(msg *nats.Msg) {
		if msg.Header.Get("Authorization") != "" {
			_ = msg.Respond([]byte("authorization header leaked"))
			return
		}
		_ = msg.Respond([]byte("hydra authorized"))
	}); err != nil {
		panic(err)
	}
	if err := nc.FlushTimeout(5 * time.Second); err != nil {
		panic(err)
	}
	if err := os.WriteFile("/tmp/auth-echo-ready", []byte("ready\n"), 0o600); err != nil {
		panic(err)
	}
	select {}
}
