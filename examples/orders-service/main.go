// Command orders-service is a small stateful NATS request/reply service for the
// gateway configuration shown in docs/configuration.md.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log"
	"os"
	"regexp"
	"strings"
	"sync"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/micro"
)

const maxOrderPayloadBytes = 64 << 10

var (
	errInvalidOrder  = errors.New("order requires non-empty id and status")
	errOrderNotFound = errors.New("order not found")
	orderIDPattern   = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
)

type order struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

type orderStatus struct {
	Status string `json:"status"`
}

type orderStore struct {
	mu     sync.RWMutex
	orders map[string]order
}

func newOrderStore() *orderStore {
	return &orderStore{orders: make(map[string]order)}
}

func (store *orderStore) upsert(payload []byte) (order, error) {
	if len(payload) == 0 || len(payload) > maxOrderPayloadBytes {
		return order{}, errInvalidOrder
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	var value order
	if err := decoder.Decode(&value); err != nil {
		return order{}, errInvalidOrder
	}
	if err := ensureJSONEnd(decoder); err != nil || !orderIDPattern.MatchString(value.ID) || strings.TrimSpace(value.Status) == "" || len(value.ID) > 128 || len(value.Status) > 128 {
		return order{}, errInvalidOrder
	}
	store.mu.Lock()
	store.orders[value.ID] = value
	store.mu.Unlock()
	return value, nil
}

func (store *orderStore) status(id string) (orderStatus, error) {
	store.mu.RLock()
	value, exists := store.orders[id]
	store.mu.RUnlock()
	if !exists {
		return orderStatus{}, errOrderNotFound
	}
	return orderStatus{Status: value.Status}, nil
}

func ensureJSONEnd(decoder *json.Decoder) error {
	var extra any
	err := decoder.Decode(&extra)
	if errors.Is(err, io.EOF) {
		return nil
	}
	return errInvalidOrder
}

func (store *orderStore) handleCreate(request micro.Request) {
	value, err := store.upsert(request.Data())
	if err != nil {
		respondServiceError(request, "4001", "invalid order")
		return
	}
	respondJSON(request, value)
}

func (store *orderStore) handleGet(request micro.Request) {
	id, found := strings.CutPrefix(request.Subject(), "orders.get.")
	if !found || id == "" || strings.Contains(id, ".") {
		respondServiceError(request, "4001", "invalid order id")
		return
	}
	status, err := store.status(id)
	if errors.Is(err, errOrderNotFound) {
		respondServiceError(request, "4041", "order not found")
		return
	}
	respondJSON(request, status)
}

func respondJSON(request micro.Request, value any) {
	headers := micro.Headers{"Content-Type": {selectedContentType(request)}}
	if err := request.RespondJSON(value, micro.WithHeaders(headers)); err != nil {
		log.Printf("send order response: %v", err)
	}
}

func respondServiceError(request micro.Request, code, description string) {
	if err := request.Error(code, description, nil); err != nil {
		log.Printf("send order error response: %v", err)
	}
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

	store := newOrderStore()
	if err := registerOrderSubscriptions(nc, store); err != nil {
		log.Fatal(err)
	}

	if err := nc.Flush(); err != nil {
		log.Fatal(err)
	}
	log.Print("orders service listening on orders.create and orders.get.*")
	select {}
}

func registerOrderSubscriptions(nc *nats.Conn, store *orderStore) error {
	if _, err := nc.Subscribe("orders.create", func(message *nats.Msg) {
		store.handleCreate(microRequest{message: message, connection: nc})
	}); err != nil {
		return err
	}
	if _, err := nc.Subscribe("orders.get.*", func(message *nats.Msg) {
		store.handleGet(microRequest{message: message, connection: nc})
	}); err != nil {
		return err
	}
	return nil
}

// microRequest adapts a plain NATS subscription to the ADR-32 request API used
// by the example handlers.
type microRequest struct {
	message    *nats.Msg
	connection *nats.Conn
}

func (request microRequest) Respond(data []byte, options ...micro.RespondOpt) error {
	reply := nats.NewMsg(request.message.Reply)
	reply.Data = data
	for _, option := range options {
		option(reply)
	}
	return request.connection.PublishMsg(reply)
}

func (request microRequest) RespondJSON(value any, options ...micro.RespondOpt) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return request.Respond(data, options...)
}

func (request microRequest) Error(code, description string, data []byte, options ...micro.RespondOpt) error {
	headers := micro.Headers{
		micro.ErrorCodeHeader: {code},
		micro.ErrorHeader:     {description},
	}
	options = append(options, micro.WithHeaders(headers))
	return request.Respond(data, options...)
}

func (request microRequest) Data() []byte           { return request.message.Data }
func (request microRequest) Headers() micro.Headers { return micro.Headers(request.message.Header) }
func (request microRequest) Subject() string        { return request.message.Subject }
func (request microRequest) Reply() string          { return request.message.Reply }

func selectedContentType(request micro.Request) string {
	switch request.Headers().Get("Accept") {
	case "application/vnd.example.order+json":
		return "application/vnd.example.order+json"
	default:
		return "application/json"
	}
}
