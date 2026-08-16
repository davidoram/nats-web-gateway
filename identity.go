package natswebgateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"unicode/utf8"

	"github.com/nats-io/nats.go"
)

const (
	downstreamIdentitySourceNATSUserID = "nats_user_id"
	natsUserInfoSubject                = "$SYS.REQ.USER.INFO"
	maxNATSUserInfoReplyBytes          = 64 * 1024
)

var (
	errDownstreamIdentityUnavailable = errors.New("authenticated downstream identity is unavailable")
	errDownstreamIdentityInvalid     = errors.New("authenticated downstream identity is invalid")
)

type natsUserInfoResponse struct {
	Data *struct {
		User string `json:"user"`
	} `json:"data"`
	Error json.RawMessage `json:"error,omitempty"`
}

func resolveDownstreamIdentity(ctx context.Context, connection natsConnection, config DownstreamIdentity) (string, error) {
	request := nats.NewMsg(natsUserInfoSubject)
	reply, err := connection.RequestMsgWithContext(ctx, request)
	if err != nil {
		return "", fmt.Errorf("query NATS authenticated user: %w", err)
	}
	if reply == nil || len(reply.Data) == 0 || len(reply.Data) > maxNATSUserInfoReplyBytes {
		return "", errDownstreamIdentityUnavailable
	}
	var response natsUserInfoResponse
	if err := json.Unmarshal(reply.Data, &response); err != nil || response.Data == nil || len(response.Error) != 0 {
		return "", errDownstreamIdentityUnavailable
	}
	value := response.Data.User
	if value == "" {
		return "", errDownstreamIdentityUnavailable
	}
	if len(value) > config.MaxValueBytes || !utf8.ValidString(value) {
		return "", errDownstreamIdentityInvalid
	}
	for _, character := range value {
		if character == '\r' || character == '\n' || character == 0 || character < 0x20 || character == 0x7f {
			return "", errDownstreamIdentityInvalid
		}
	}
	return value, nil
}
