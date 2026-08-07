// Command adr32-example runs the non-production NATS service used by the local
// integration environment.
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/micro"
)

const (
	defaultNATSURL = "nats://nats:4222"
	serviceName    = "Echo"
	serviceVersion = "0.1.0"
)

func main() {
	if err := run(); err != nil {
		log.Printf("ADR-32 example service failed: %v", err)
		os.Exit(1)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	nc, err := nats.Connect(envOrDefault("NATS_URL", defaultNATSURL),
		nats.UserInfo(envOrDefault("NATS_USER", "adr32_service"), envOrDefault("NATS_PASSWORD", "local-service-only")),
		nats.Name("local-adr32-example"),
		nats.Timeout(5*time.Second),
		nats.MaxReconnects(-1),
		nats.ReconnectWait(250*time.Millisecond),
	)
	if err != nil {
		return fmt.Errorf("connect to NATS: %w", err)
	}
	defer nc.Close()

	service, err := micro.AddService(nc, micro.Config{
		Name:        serviceName,
		Version:     serviceVersion,
		Description: "Non-production ADR-32 echo fixture",
	})
	if err != nil {
		return fmt.Errorf("add ADR-32 service: %w", err)
	}
	defer func() {
		if err := service.Stop(); err != nil {
			log.Printf("stop ADR-32 service: %v", err)
		}
	}()

	if err := service.AddEndpoint("echo", micro.HandlerFunc(handleEcho), micro.WithEndpointSubject("demo.echo")); err != nil {
		return fmt.Errorf("add echo endpoint: %w", err)
	}
	if err := nc.FlushTimeout(5 * time.Second); err != nil {
		return fmt.Errorf("flush service subscriptions: %w", err)
	}
	readinessFile := envOrDefault("READINESS_FILE", "/tmp/ready")
	if err := os.WriteFile(readinessFile, []byte("ready\n"), 0o644); err != nil {
		return fmt.Errorf("write readiness file: %w", err)
	}
	defer os.Remove(readinessFile)

	log.Printf("ADR-32 example service ready: name=%s version=%s subject=demo.echo", serviceName, serviceVersion)
	<-ctx.Done()
	return nil
}

func handleEcho(request micro.Request) {
	if string(request.Data()) == "error" {
		if err := request.Error("4001", "fixture requested an error", nil); err != nil {
			log.Printf("send fixture error response: %v", err)
		}
		return
	}
	if err := request.Respond(request.Data()); err != nil {
		log.Printf("send echo response: %v", err)
	}
}

func envOrDefault(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
