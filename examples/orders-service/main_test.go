package main

import (
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	natstest "github.com/nats-io/nats-server/v2/test"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/micro"
)

func TestOrderStoreCreatesAndOverwritesOrders(t *testing.T) {
	store := newOrderStore()
	created, err := store.upsert([]byte(`{"id":"order-42","status":"pending"}`))
	if err != nil || created.ID != "order-42" || created.Status != "pending" {
		t.Fatalf("create = %+v, %v", created, err)
	}
	updated, err := store.upsert([]byte(`{"id":"order-42","status":"shipped"}`))
	if err != nil || updated.Status != "shipped" {
		t.Fatalf("overwrite = %+v, %v", updated, err)
	}
	status, err := store.status("order-42")
	if err != nil || status.Status != "shipped" {
		t.Fatalf("status = %+v, %v", status, err)
	}
}

func TestOrderStoreRejectsInvalidPayloads(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, payload string
	}{
		{name: "empty"},
		{name: "malformed", payload: `{"id":`},
		{name: "missing id", payload: `{"status":"pending"}`},
		{name: "missing status", payload: `{"id":"order-42"}`},
		{name: "unreachable id", payload: `{"id":"order.42","status":"pending"}`},
		{name: "blank status", payload: `{"id":"order-42","status":"  "}`},
		{name: "unknown field", payload: `{"id":"order-42","status":"pending","owner":"alice"}`},
		{name: "multiple values", payload: `{"id":"order-42","status":"pending"} {}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := newOrderStore().upsert([]byte(test.payload))
			if !errors.Is(err, errInvalidOrder) {
				t.Fatalf("upsert(%q) error = %v", test.payload, err)
			}
		})
	}
	oversized := []byte(strings.Repeat("x", maxOrderPayloadBytes+1))
	if _, err := newOrderStore().upsert(oversized); !errors.Is(err, errInvalidOrder) {
		t.Fatalf("oversized error = %v", err)
	}
}

func TestOrderHandlersReturnStoredStatusAndErrors(t *testing.T) {
	store := newOrderStore()
	create := &fakeRequest{
		data:    []byte(`{"id":"order-42","status":"pending"}`),
		headers: micro.Headers{"Accept": {"application/vnd.example.order+json"}},
	}
	store.handleCreate(create)
	if create.serviceCode != "" || create.response == nil || create.response.Header.Get("Content-Type") != "application/vnd.example.order+json" {
		t.Fatalf("create response/error = %+v/%q", create.response, create.serviceCode)
	}
	var created order
	if err := json.Unmarshal(create.response.Data, &created); err != nil || created.ID != "order-42" || created.Status != "pending" {
		t.Fatalf("created response = %+v, %v", created, err)
	}

	get := &fakeRequest{subject: "orders.get.order-42", headers: make(micro.Headers)}
	store.handleGet(get)
	if get.serviceCode != "" || get.response == nil || string(get.response.Data) != `{"status":"pending"}` {
		t.Fatalf("get response/error = %+v/%q", get.response, get.serviceCode)
	}

	missing := &fakeRequest{subject: "orders.get.missing", headers: make(micro.Headers)}
	store.handleGet(missing)
	if missing.serviceCode != "4041" || missing.serviceDescription != "order not found" {
		t.Fatalf("missing error = %q/%q", missing.serviceCode, missing.serviceDescription)
	}

	invalid := &fakeRequest{data: []byte(`{}`), headers: make(micro.Headers)}
	store.handleCreate(invalid)
	if invalid.serviceCode != "4001" {
		t.Fatalf("invalid create code = %q", invalid.serviceCode)
	}
}

func TestOrderStoreSupportsConcurrentRequests(t *testing.T) {
	store := newOrderStore()
	if _, err := store.upsert([]byte(`{"id":"order-42","status":"initial"}`)); err != nil {
		t.Fatal(err)
	}
	var requests sync.WaitGroup
	for index := range 100 {
		requests.Add(2)
		go func() {
			defer requests.Done()
			payload := `{"id":"order-42","status":"status-` + string(rune('a'+index%26)) + `"}`
			if _, err := store.upsert([]byte(payload)); err != nil {
				t.Errorf("upsert: %v", err)
			}
		}()
		go func() {
			defer requests.Done()
			if _, err := store.status("order-42"); err != nil {
				t.Errorf("status: %v", err)
			}
		}()
	}
	requests.Wait()
}

func TestOrderServiceRoundTripsOverNATS(t *testing.T) {
	server := natstest.RunRandClientPortServer()
	t.Cleanup(server.Shutdown)
	nc, err := nats.Connect(server.ClientURL())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(nc.Close)
	if err := registerOrderSubscriptions(nc, newOrderStore()); err != nil {
		t.Fatal(err)
	}
	if err := nc.Flush(); err != nil {
		t.Fatal(err)
	}

	create := nats.NewMsg("orders.create")
	create.Header.Set("Accept", "application/vnd.example.order+json")
	create.Data = []byte(`{"id":"order-42","status":"pending"}`)
	response, err := nc.RequestMsg(create, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if response.Header.Get("Content-Type") != "application/vnd.example.order+json" || string(response.Data) != `{"id":"order-42","status":"pending"}` {
		t.Fatalf("create response = %v %q", response.Header, response.Data)
	}

	response, err = nc.Request("orders.get.order-42", nil, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if response.Header.Get("Content-Type") != "application/json" || string(response.Data) != `{"status":"pending"}` {
		t.Fatalf("get response = %v %q", response.Header, response.Data)
	}

	response, err = nc.Request("orders.get.missing", nil, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if response.Header.Get(micro.ErrorCodeHeader) != "4041" || response.Header.Get(micro.ErrorHeader) != "order not found" {
		t.Fatalf("missing response = %v", response.Header)
	}
}

type fakeRequest struct {
	data                            []byte
	headers                         micro.Headers
	subject                         string
	response                        *nats.Msg
	serviceCode, serviceDescription string
}

func (request *fakeRequest) Respond(data []byte, options ...micro.RespondOpt) error {
	request.response = &nats.Msg{Header: make(nats.Header), Data: data}
	for _, option := range options {
		option(request.response)
	}
	return nil
}

func (request *fakeRequest) RespondJSON(value any, options ...micro.RespondOpt) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return request.Respond(data, options...)
}

func (request *fakeRequest) Error(code, description string, _ []byte, _ ...micro.RespondOpt) error {
	request.serviceCode, request.serviceDescription = code, description
	return nil
}

func (request *fakeRequest) Data() []byte           { return request.data }
func (request *fakeRequest) Headers() micro.Headers { return request.headers }
func (request *fakeRequest) Subject() string        { return request.subject }
func (request *fakeRequest) Reply() string          { return "_INBOX.test" }
