package translation

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/nats-io/nats.go"
)

type requesterFunc func(context.Context, *nats.Msg) (*nats.Msg, error)

func (f requesterFunc) RequestMsgWithContext(ctx context.Context, msg *nats.Msg) (*nats.Msg, error) {
	return f(ctx, msg)
}

func TestExecuteCopiesAllowlistedHeadersAndBoundsPayloads(t *testing.T) {
	requester := requesterFunc(func(_ context.Context, message *nats.Msg) (*nats.Msg, error) {
		if string(message.Data) != "hello" || message.Header.Get("X-Request-Id") != "safe" || message.Header.Get("Authorization") != "" {
			t.Fatalf("message = %+v", message)
		}
		return &nats.Msg{Data: []byte("reply")}, nil
	})
	reply, err := Execute(context.Background(), requester, Request{Subject: "demo.echo", Body: strings.NewReader("hello"), Header: http.Header{"X-Request-Id": {"safe"}, "Authorization": {"secret"}}}, []string{"X-Request-Id"}, 5, 5)
	if err != nil || string(reply.Data) != "reply" {
		t.Fatalf("Execute() = %v, %v", reply, err)
	}
}

func TestExecuteLimitsRequestAndReply(t *testing.T) {
	requester := requesterFunc(func(context.Context, *nats.Msg) (*nats.Msg, error) { return &nats.Msg{Data: []byte("large")}, nil })
	_, err := Execute(context.Background(), requester, Request{Body: strings.NewReader("large")}, nil, 4, 10)
	if !errors.Is(err, ErrRequestTooLarge) {
		t.Fatalf("error = %v", err)
	}
	_, err = Execute(context.Background(), requester, Request{Body: strings.NewReader("ok")}, nil, 4, 4)
	if !errors.Is(err, ErrReplyTooLarge) {
		t.Fatalf("error = %v", err)
	}
}

func TestExecutePropagatesContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	requester := requesterFunc(func(got context.Context, _ *nats.Msg) (*nats.Msg, error) {
		cancel()
		<-got.Done()
		return nil, got.Err()
	})
	_, err := Execute(ctx, requester, Request{}, nil, 1, 1)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v", err)
	}
}
