// Command pets-service runs the non-production REST-style and RPC-style Pets
// services used by the example and integration environment.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"os/signal"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/micro"
)

const (
	defaultNATSURL = "nats://nats:4222"
	serviceVersion = "1.0.0"
	maxPetJSONSize = 64 << 10

	invalidPetCode = "4001"
	missingPetCode = "4041"
)

var (
	errInvalidPet = errors.New("pet id and name are required")
	errMissingPet = errors.New("pet not found")
	petIDPattern  = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
)

type pet struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type petStore struct {
	mu   sync.RWMutex
	pets map[string]pet
}

func newPetStore() *petStore {
	return &petStore{pets: make(map[string]pet)}
}

func (store *petStore) create(value pet) (pet, error) {
	if err := validatePet(value); err != nil {
		return pet{}, err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if _, exists := store.pets[value.ID]; exists {
		return pet{}, errors.New("pet already exists")
	}
	store.pets[value.ID] = value
	return value, nil
}

func (store *petStore) get(id string) (pet, error) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	value, exists := store.pets[id]
	if !exists {
		return pet{}, errMissingPet
	}
	return value, nil
}

func (store *petStore) update(id string, value pet) (pet, error) {
	value.ID = id
	if err := validatePet(value); err != nil {
		return pet{}, err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if _, exists := store.pets[id]; !exists {
		return pet{}, errMissingPet
	}
	store.pets[id] = value
	return value, nil
}

func (store *petStore) delete(id string) (pet, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	value, exists := store.pets[id]
	if !exists {
		return pet{}, errMissingPet
	}
	delete(store.pets, id)
	return value, nil
}

func (store *petStore) list() []pet {
	store.mu.RLock()
	defer store.mu.RUnlock()
	values := make([]pet, 0, len(store.pets))
	for _, value := range store.pets {
		values = append(values, value)
	}
	// The examples use stable IDs in tests; insertion sort keeps output
	// deterministic without exposing map iteration order.
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j].ID < values[j-1].ID; j-- {
			values[j], values[j-1] = values[j-1], values[j]
		}
	}
	return values
}

func validatePet(value pet) error {
	if !petIDPattern.MatchString(value.ID) || value.Name == "" || len(value.ID) > 64 || len(value.Name) > 256 {
		return errInvalidPet
	}
	return nil
}

func main() {
	if err := run(); err != nil {
		log.Printf("pets services failed: %v", err)
		os.Exit(1)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	nc, err := nats.Connect(envOrDefault("NATS_URL", defaultNATSURL),
		nats.UserInfo(envOrDefault("NATS_USER", "pets_service"), envOrDefault("NATS_PASSWORD", "local-pets-only")),
		nats.Name("local-pets-example"), nats.Timeout(5*time.Second), nats.MaxReconnects(-1),
		nats.ReconnectWait(250*time.Millisecond))
	if err != nil {
		return fmt.Errorf("connect to NATS: %w", err)
	}
	defer nc.Close()

	store := newPetStore()
	services, err := addPetServices(nc, store)
	if err != nil {
		return err
	}
	defer func() {
		for _, service := range services {
			if err := service.Stop(); err != nil {
				log.Printf("stop Pets service: %v", err)
			}
		}
	}()
	if err := nc.FlushTimeout(5 * time.Second); err != nil {
		return fmt.Errorf("flush Pets service subscriptions: %w", err)
	}
	readiness := envOrDefault("READINESS_FILE", "/tmp/pets-service-ready")
	if err := os.WriteFile(readiness, []byte("ready\n"), 0o644); err != nil {
		return fmt.Errorf("write readiness marker: %w", err)
	}
	defer os.Remove(readiness)

	log.Printf("Pets services ready: REST=PetsREST RPC=PetsRPC")
	<-ctx.Done()
	return nil
}

func addPetServices(nc *nats.Conn, store *petStore) ([]micro.Service, error) {
	configs := []struct {
		name, description string
		endpoints         []petEndpoint
	}{
		{name: "PetsREST", description: "Non-production REST-style Pets API", endpoints: restEndpoints(store)},
		{name: "PetsRPC", description: "Non-production RPC-style Pets API", endpoints: rpcEndpoints(store)},
	}
	services := make([]micro.Service, 0, len(configs))
	for _, config := range configs {
		service, err := micro.AddService(nc, micro.Config{
			Name: config.name, Version: serviceVersion, Description: config.description,
			Metadata: map[string]string{"example": "pets", "state": "ephemeral"}, QueueGroup: "pets",
		})
		if err != nil {
			stopServices(services)
			return nil, fmt.Errorf("add %s service: %w", config.name, err)
		}
		services = append(services, service)
		for _, endpoint := range config.endpoints {
			if err := service.AddEndpoint(endpoint.name, endpoint.handler,
				micro.WithEndpointSubject(endpoint.subject),
				micro.WithEndpointMetadata(map[string]string{"style": endpoint.style, "payload": "application/json"})); err != nil {
				stopServices(services)
				return nil, fmt.Errorf("add %s endpoint %s: %w", config.name, endpoint.name, err)
			}
		}
	}
	return services, nil
}

func stopServices(services []micro.Service) {
	for _, service := range services {
		_ = service.Stop()
	}
}

type petEndpoint struct {
	name, subject, style string
	handler              micro.Handler
}

func restEndpoints(store *petStore) []petEndpoint {
	return []petEndpoint{
		{name: "create_pet", subject: "pets.rest.create", style: "rest", handler: jsonHandler(func(request micro.Request) (any, error) {
			var value pet
			if err := decodeJSON(request.Data(), &value); err != nil {
				return nil, err
			}
			return store.create(value)
		})},
		{name: "list_pets", subject: "pets.rest.list", style: "rest", handler: jsonHandler(func(micro.Request) (any, error) { return store.list(), nil })},
		{name: "get_pet", subject: "pets.rest.get.*", style: "rest", handler: jsonHandler(func(request micro.Request) (any, error) {
			return store.get(lastSubjectToken(request.Subject()))
		})},
		{name: "update_pet", subject: "pets.rest.update.*", style: "rest", handler: jsonHandler(func(request micro.Request) (any, error) {
			var value pet
			if err := decodeJSON(request.Data(), &value); err != nil {
				return nil, err
			}
			return store.update(lastSubjectToken(request.Subject()), value)
		})},
		{name: "delete_pet", subject: "pets.rest.delete.*", style: "rest", handler: jsonHandler(func(request micro.Request) (any, error) {
			return store.delete(lastSubjectToken(request.Subject()))
		})},
	}
}

func rpcEndpoints(store *petStore) []petEndpoint {
	return []petEndpoint{
		{name: "CreatePet", subject: "pets.rpc.CreatePet", style: "rpc", handler: restEndpoints(store)[0].handler},
		{name: "ListPets", subject: "pets.rpc.ListPets", style: "rpc", handler: restEndpoints(store)[1].handler},
		{name: "GetPet", subject: "pets.rpc.GetPet", style: "rpc", handler: jsonHandler(func(request micro.Request) (any, error) {
			id, err := decodeID(request.Data())
			if err != nil {
				return nil, err
			}
			return store.get(id)
		})},
		{name: "UpdatePet", subject: "pets.rpc.UpdatePet", style: "rpc", handler: jsonHandler(func(request micro.Request) (any, error) {
			var value pet
			if err := decodeJSON(request.Data(), &value); err != nil {
				return nil, err
			}
			return store.update(value.ID, value)
		})},
		{name: "DeletePet", subject: "pets.rpc.DeletePet", style: "rpc", handler: jsonHandler(func(request micro.Request) (any, error) {
			id, err := decodeID(request.Data())
			if err != nil {
				return nil, err
			}
			return store.delete(id)
		})},
	}
}

func jsonHandler(handle func(micro.Request) (any, error)) micro.Handler {
	return micro.HandlerFunc(func(request micro.Request) {
		value, err := handle(request)
		if err != nil {
			code := invalidPetCode
			if errors.Is(err, errMissingPet) {
				code = missingPetCode
			}
			if responseErr := request.Error(code, err.Error(), nil); responseErr != nil {
				log.Printf("send Pets error response: %v", responseErr)
			}
			return
		}
		payload, err := json.Marshal(value)
		if err != nil {
			log.Printf("encode Pets response: %v", err)
			return
		}
		if err := request.Respond(payload); err != nil {
			log.Printf("send Pets response: %v", err)
		}
	})
}

func decodeJSON(data []byte, target any) error {
	if len(data) == 0 || len(data) > maxPetJSONSize || !json.Valid(data) {
		return errInvalidPet
	}
	if err := json.Unmarshal(data, target); err != nil {
		return errInvalidPet
	}
	return nil
}

func decodeID(data []byte) (string, error) {
	var request struct {
		ID string `json:"id"`
	}
	if err := decodeJSON(data, &request); err != nil || !petIDPattern.MatchString(request.ID) || len(request.ID) > 64 {
		return "", errInvalidPet
	}
	return request.ID, nil
}

func lastSubjectToken(subject string) string {
	if index := strings.LastIndexByte(subject, '.'); index >= 0 {
		return subject[index+1:]
	}
	return subject
}

func envOrDefault(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
