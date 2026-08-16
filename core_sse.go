package natswebgateway

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/davidoram/nats-web-gateway/internal/credentials"
	"github.com/nats-io/nats.go"
)

type coreNATSSubscriber interface {
	Subscribe(string, nats.MsgHandler) (*nats.Subscription, error)
	FlushWithContext(context.Context) error
}

type coreSSEMessage struct {
	data []byte
	size int64
}

type coreSSEBuffer struct {
	mu       sync.Mutex
	messages chan coreSSEMessage
	overflow chan struct{}
	bytes    int64
	maxBytes int64
	once     sync.Once
}

func newCoreSSEBuffer(messageLimit int, byteLimit int64) *coreSSEBuffer {
	return &coreSSEBuffer{
		messages: make(chan coreSSEMessage, messageLimit),
		overflow: make(chan struct{}),
		maxBytes: byteLimit,
	}
}

func (buffer *coreSSEBuffer) enqueue(message *nats.Msg) {
	size := int64(len(message.Data))
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	if size > buffer.maxBytes || buffer.bytes > buffer.maxBytes-size || len(buffer.messages) == cap(buffer.messages) {
		buffer.once.Do(func() { close(buffer.overflow) })
		return
	}
	data := append([]byte(nil), message.Data...)
	select {
	case buffer.messages <- coreSSEMessage{data: data, size: size}:
		buffer.bytes += size
	default:
		buffer.once.Do(func() { close(buffer.overflow) })
	}
}

func (buffer *coreSSEBuffer) release(message coreSSEMessage) {
	buffer.mu.Lock()
	buffer.bytes -= message.size
	buffer.mu.Unlock()
}

func (h Handler) serveCoreSSE(w http.ResponseWriter, r *http.Request, candidate compiledRoute, subject string) error {
	select {
	case candidate.streams <- struct{}{}:
		defer func() { <-candidate.streams }()
	default:
		writeGatewayError(w, http.StatusTooManyRequests, "stream connection quota exceeded")
		return nil
	}

	if h.lifecycle == nil || (candidate.pool == nil && !h.Ready()) || (candidate.pool != nil && !h.lifecycle.isServing()) {
		writeGatewayError(w, http.StatusServiceUnavailable, "service unavailable")
		return nil
	}
	timeoutCtx, timeoutCancel := context.WithTimeout(r.Context(), time.Duration(candidate.config.Timeout))
	defer timeoutCancel()
	setupCtx, setupCancel := context.WithCancelCause(timeoutCtx)
	defer setupCancel(nil)
	connection := h.lifecycle.connection
	tracker := h.lifecycle.permissions
	var release func()
	if candidate.pool != nil {
		adapted, err := (credentials.Adapter{
			Mechanism:          candidate.config.SecurityContext.Mechanism,
			MaxCredentialBytes: candidate.config.SecurityContext.MaxCredentialBytes,
		}).Adapt(r)
		if err != nil {
			writeGatewayError(w, http.StatusUnauthorized, "unauthorized")
			return nil
		}
		lease, err := candidate.pool.acquire(setupCtx, adapted)
		if err != nil {
			writeStreamSetupError(w, err)
			return nil
		}
		connection, tracker, release = lease.connection, lease.tracker, lease.release
		defer release()
	}
	subscriber, ok := connection.(coreNATSSubscriber)
	if !ok {
		writeGatewayError(w, http.StatusServiceUnavailable, "service unavailable")
		return nil
	}
	messageByteLimit := min(candidate.config.CoreSSE.BufferBytes, candidate.config.MaxReplyBytes)
	buffer := newCoreSSEBuffer(candidate.config.CoreSSE.BufferMessages, messageByteLimit)
	unregisterPermission := tracker.register(subject, setupCancel)
	defer unregisterPermission()
	subscription, err := subscriber.Subscribe(subject, buffer.enqueue)
	if err != nil {
		writeStreamSetupError(w, err)
		return nil
	}
	defer subscription.Unsubscribe()
	if err := subscriber.FlushWithContext(setupCtx); err != nil {
		writeStreamSetupError(w, err)
		return nil
	}
	if err := context.Cause(setupCtx); err != nil {
		writeStreamSetupError(w, err)
		return nil
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeGatewayError(w, http.StatusInternalServerError, "streaming unsupported")
		return nil
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if _, err := fmt.Fprint(w, ": connected\n\n"); err != nil {
		return err
	}
	flusher.Flush()

	heartbeat := time.NewTicker(time.Duration(candidate.config.CoreSSE.HeartbeatInterval))
	defer heartbeat.Stop()
	duration := time.NewTimer(time.Duration(candidate.config.CoreSSE.MaxDuration))
	defer duration.Stop()
	for {
		select {
		case <-r.Context().Done():
			return nil
		case <-h.lifecycle.stopped:
			return nil
		case <-buffer.overflow:
			return writeSSEControl(w, flusher, "error", "slow consumer")
		case <-duration.C:
			return writeSSEControl(w, flusher, "close", "maximum duration reached")
		case <-heartbeat.C:
			if _, err := fmt.Fprint(w, ": heartbeat\n\n"); err != nil {
				return err
			}
			flusher.Flush()
		case message := <-buffer.messages:
			buffer.release(message)
			if int64(len(message.data)) > candidate.config.MaxReplyBytes || !utf8.Valid(message.data) {
				return writeSSEControl(w, flusher, "error", "invalid event payload")
			}
			if err := writeSSEData(w, message.data); err != nil {
				return err
			}
			flusher.Flush()
		}
	}
}

func writeStreamSetupError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, context.Canceled):
		return
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, nats.ErrTimeout):
		writeGatewayError(w, http.StatusGatewayTimeout, "stream setup timed out")
	case errors.Is(err, nats.ErrAuthorization), credentialFailure(err):
		writeGatewayError(w, http.StatusUnauthorized, "unauthorized")
	case errors.Is(err, nats.ErrPermissionViolation):
		writeGatewayError(w, http.StatusForbidden, "forbidden")
	default:
		writeGatewayError(w, http.StatusServiceUnavailable, "service unavailable")
	}
}

func writeSSEData(w http.ResponseWriter, data []byte) error {
	for line := range strings.SplitSeq(string(data), "\n") {
		if _, err := fmt.Fprintf(w, "data: %s\n", strings.TrimSuffix(line, "\r")); err != nil {
			return err
		}
	}
	_, err := fmt.Fprint(w, "\n")
	return err
}

func writeSSEControl(w http.ResponseWriter, flusher http.Flusher, event, data string) error {
	if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data); err != nil {
		return err
	}
	flusher.Flush()
	return nil
}
