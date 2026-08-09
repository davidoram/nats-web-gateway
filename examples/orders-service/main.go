// Command orders-service is a small NATS request/reply service for the gateway
// configuration shown in docs/configuration.md.
package main

import (
	"encoding/json"
	"log"
	"os"
	"strings"

	"github.com/nats-io/nats.go"
)

type order struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

func main() {
	url := os.Getenv("NATS_URL")
	if url == "" {
		url = nats.DefaultURL
	}
	nc, err := nats.Connect(url)
	if err != nil {
		log.Fatal(err)
	}
	defer nc.Drain() //nolint:errcheck // process exit is already in progress

	_, err = nc.Subscribe("orders.get.*.*", func(message *nats.Msg) {
		tokens := strings.Split(message.Subject, ".")
		payload, marshalErr := json.Marshal(order{ID: tokens[2], Status: tokens[3]})
		if marshalErr != nil {
			log.Printf("marshal reply: %v", marshalErr)
			return
		}
		reply := nats.NewMsg(message.Reply)
		reply.Header.Set("Content-Type", selectedContentType(message))
		reply.Data = payload
		if err := nc.PublishMsg(reply); err != nil {
			log.Printf("publish reply: %v", err)
		}
	})
	if err != nil {
		log.Fatal(err)
	}

	_, err = nc.Subscribe("orders.create", func(message *nats.Msg) {
		// The gateway already enforced the configured body limit. Services must
		// still validate their domain payloads and permissions normally.
		reply := nats.NewMsg(message.Reply)
		reply.Header.Set("Content-Type", selectedContentType(message))
		reply.Data = message.Data
		if err := nc.PublishMsg(reply); err != nil {
			log.Printf("publish reply: %v", err)
		}
	})
	if err != nil {
		log.Fatal(err)
	}

	if err := nc.Flush(); err != nil {
		log.Fatal(err)
	}
	log.Print("orders service listening on orders.get.*.* and orders.create")
	select {}
}

func selectedContentType(message *nats.Msg) string {
	switch message.Header.Get("Accept") {
	case "application/vnd.example.order+json":
		return "application/vnd.example.order+json"
	default:
		return "application/json"
	}
}
