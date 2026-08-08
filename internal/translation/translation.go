// Package translation contains bounded HTTP-to-NATS request/reply translation.
package translation

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/nats-io/nats.go"
)

var (
	ErrRequestTooLarge = errors.New("request body exceeds configured limit")
	ErrReplyTooLarge   = errors.New("reply exceeds configured limit")
)

type Requester interface {
	RequestMsgWithContext(context.Context, *nats.Msg) (*nats.Msg, error)
}

type Request struct {
	Subject string
	Header  http.Header
	Body    io.Reader
}

func Execute(ctx context.Context, requester Requester, request Request, requestHeaders []string, maxRequest, maxReply int64) (*nats.Msg, error) {
	body, err := readBounded(request.Body, maxRequest, ErrRequestTooLarge)
	if err != nil {
		return nil, err
	}
	message := nats.NewMsg(request.Subject)
	message.Data = body
	for _, name := range requestHeaders {
		for _, value := range request.Header.Values(name) {
			message.Header.Add(name, value)
		}
	}
	reply, err := requester.RequestMsgWithContext(ctx, message)
	if err != nil {
		return nil, fmt.Errorf("request NATS service: %w", err)
	}
	if int64(len(reply.Data)) > maxReply {
		return nil, ErrReplyTooLarge
	}
	return reply, nil
}

func readBounded(reader io.Reader, limit int64, limitErr error) ([]byte, error) {
	if reader == nil {
		return nil, nil
	}
	limited := io.LimitReader(reader, limit+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	if int64(len(data)) > limit {
		return nil, limitErr
	}
	return data, nil
}
