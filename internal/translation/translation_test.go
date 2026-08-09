package translation

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

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
	reply, err := Execute(context.Background(), requester, Request{Subject: "demo.echo", Body: io.NopCloser(strings.NewReader("hello")), Header: http.Header{"X-Request-Id": {"safe"}, "Authorization": {"secret"}}}, []string{"X-Request-Id"}, 5, 5)
	if err != nil || string(reply.Data) != "reply" {
		t.Fatalf("Execute() = %v, %v", reply, err)
	}
}

func TestExecuteLimitsRequestAndReply(t *testing.T) {
	requester := requesterFunc(func(context.Context, *nats.Msg) (*nats.Msg, error) { return &nats.Msg{Data: []byte("large")}, nil })
	_, err := Execute(context.Background(), requester, Request{Body: io.NopCloser(strings.NewReader("large"))}, nil, 4, 10)
	if !errors.Is(err, ErrRequestTooLarge) {
		t.Fatalf("error = %v", err)
	}
	_, err = Execute(context.Background(), requester, Request{Body: io.NopCloser(strings.NewReader("ok"))}, nil, 4, 4)
	if !errors.Is(err, ErrReplyTooLarge) {
		t.Fatalf("error = %v", err)
	}
}

func TestExecuteDeadlineInterruptsBodyRead(t *testing.T) {
	body := newBlockingBody()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	result := make(chan error, 1)
	go func() {
		_, err := Execute(ctx, requesterFunc(func(context.Context, *nats.Msg) (*nats.Msg, error) {
			t.Error("NATS request executed after body cancellation")
			return nil, nil
		}), Request{Body: body}, nil, 1024, 1024)
		result <- err
	}()
	<-body.started
	select {
	case err := <-result:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("Execute() error = %v, want deadline exceeded", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Execute() did not stop the body read after its deadline")
	}
}

type blockingBody struct {
	started chan struct{}
	closed  chan struct{}
	once    sync.Once
}

func newBlockingBody() *blockingBody {
	return &blockingBody{started: make(chan struct{}), closed: make(chan struct{})}
}

func (body *blockingBody) Read([]byte) (int, error) {
	body.once.Do(func() { close(body.started) })
	<-body.closed
	return 0, errors.New("body closed")
}

func (body *blockingBody) Close() error {
	select {
	case <-body.closed:
	default:
		close(body.closed)
	}
	return nil
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

func TestExecuteRejectsNilReply(t *testing.T) {
	requester := requesterFunc(func(context.Context, *nats.Msg) (*nats.Msg, error) { return nil, nil })
	_, err := Execute(context.Background(), requester, Request{}, nil, 1, 1)
	if !errors.Is(err, ErrMalformedReply) {
		t.Fatalf("error = %v, want malformed reply", err)
	}
}

func TestExecutePreservesCancellationCause(t *testing.T) {
	ctx, cancel := context.WithCancelCause(context.Background())
	wantErr := errors.New("request permission denied")
	requester := requesterFunc(func(ctx context.Context, _ *nats.Msg) (*nats.Msg, error) {
		cancel(wantErr)
		return nil, ctx.Err()
	})
	_, err := Execute(ctx, requester, Request{}, nil, 1, 1)
	if !errors.Is(err, wantErr) {
		t.Fatalf("error = %v, want cancellation cause", err)
	}
}
