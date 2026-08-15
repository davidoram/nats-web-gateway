package credentials

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/nats-io/nats.go"
)

const DefaultMaxCredentialBytes = 8 * 1024

var (
	ErrCredentialMissing   = errors.New("credential is missing")
	ErrCredentialMalformed = errors.New("credential is malformed")
	ErrCredentialAmbiguous = errors.New("credential presentation is ambiguous")
	ErrProofUnavailable    = errors.New("proof of possession is unavailable")
)

// Mechanism identifies one explicit HTTP-to-NATS authentication mapping.
type Mechanism string

const (
	MechanismBearerToken  Mechanism = "bearer_token"
	MechanismUserPassword Mechanism = "user_password"
	MechanismNKey         Mechanism = "nkey"
	MechanismNKeyJWT      Mechanism = "nkey_jwt"
	MechanismTLS          Mechanism = "tls"
)

// Adapter converts exactly one configured credential mechanism into NATS
// client options. MaxCredentialBytes bounds each textual credential; zero uses
// DefaultMaxCredentialBytes.
type Adapter struct {
	Mechanism          Mechanism
	MaxCredentialBytes int
}

// Validate rejects unsupported or nonsensical adapter configuration.
func (adapter Adapter) Validate() error {
	switch adapter.Mechanism {
	case MechanismBearerToken, MechanismUserPassword, MechanismNKey, MechanismNKeyJWT, MechanismTLS:
	default:
		return fmt.Errorf("unsupported credential mechanism %q", adapter.Mechanism)
	}
	if adapter.MaxCredentialBytes < 0 {
		return errors.New("max credential bytes must not be negative")
	}
	return nil
}

// Options translates the configured presentation without authenticating it.
// The returned option must be used only for the request's security context.
func (adapter Adapter) Options(request *http.Request) ([]nats.Option, error) {
	if err := adapter.Validate(); err != nil {
		return nil, err
	}
	if request == nil {
		return nil, ErrCredentialMissing
	}
	limit := adapter.MaxCredentialBytes
	if limit == 0 {
		limit = DefaultMaxCredentialBytes
	}

	switch adapter.Mechanism {
	case MechanismBearerToken:
		if hasTrustedProof(request.Context()) {
			return nil, ErrCredentialAmbiguous
		}
		token, err := bearerToken(request, limit)
		if err != nil {
			return nil, err
		}
		return []nats.Option{nats.Token(token)}, nil
	case MechanismUserPassword:
		if hasTrustedProof(request.Context()) {
			return nil, ErrCredentialAmbiguous
		}
		username, password, err := basicCredentials(request, limit)
		if err != nil {
			return nil, err
		}
		return []nats.Option{nats.UserInfo(username, password)}, nil
	case MechanismNKey:
		if len(request.Header.Values("Authorization")) != 0 || hasNKeyJWTProof(request.Context()) || hasTLSProof(request.Context()) {
			return nil, ErrCredentialAmbiguous
		}
		proof, ok := request.Context().Value(nkeyContextKey{}).(NKeyProof)
		if !ok || !validTextCredential(proof.PublicKey, limit) || proof.Sign == nil {
			return nil, ErrProofUnavailable
		}
		return []nats.Option{nats.Nkey(proof.PublicKey, proof.Sign)}, nil
	case MechanismNKeyJWT:
		if len(request.Header.Values("Authorization")) != 0 || hasNKeyProof(request.Context()) || hasTLSProof(request.Context()) {
			return nil, ErrCredentialAmbiguous
		}
		proof, ok := request.Context().Value(nkeyJWTContextKey{}).(NKeyJWTProof)
		if !ok || proof.JWT == nil || proof.Sign == nil {
			return nil, ErrProofUnavailable
		}
		boundedJWT := func() (string, error) {
			jwt, err := proof.JWT()
			if err != nil {
				return "", ErrProofUnavailable
			}
			if !validTextCredential(jwt, limit) {
				return "", ErrCredentialMalformed
			}
			return jwt, nil
		}
		return []nats.Option{nats.UserJWT(boundedJWT, proof.Sign)}, nil
	case MechanismTLS:
		if len(request.Header.Values("Authorization")) != 0 || hasNKeyProof(request.Context()) || hasNKeyJWTProof(request.Context()) {
			return nil, ErrCredentialAmbiguous
		}
		certificate, ok := request.Context().Value(tlsCertificateContextKey{}).(tls.Certificate)
		if !ok || len(certificate.Certificate) == 0 || certificate.PrivateKey == nil {
			return nil, ErrProofUnavailable
		}
		config := &tls.Config{
			MinVersion:   tls.VersionTLS12,
			Certificates: []tls.Certificate{certificate},
		}
		return []nats.Option{nats.Secure(config)}, nil
	default:
		panic("validated credential mechanism has no adapter")
	}
}

// NKeyJWTProof holds callbacks that retain the NKey signing operation. The JWT
// alone is insufficient: Sign must sign the nonce supplied by the NATS server.
type NKeyJWTProof struct {
	JWT  nats.UserJWTHandler
	Sign nats.SignatureHandler
}

// NKeyProof holds a public user NKey and the corresponding nonce signer.
type NKeyProof struct {
	PublicKey string
	Sign      nats.SignatureHandler
}

type nkeyContextKey struct{}
type nkeyJWTContextKey struct{}
type tlsCertificateContextKey struct{}

// WithNKeyProof attaches an NKey signer supplied by a trusted HTTP transport
// integration. A caller-provided public-key header is not proof of possession.
func WithNKeyProof(ctx context.Context, proof NKeyProof) context.Context {
	return context.WithValue(ctx, nkeyContextKey{}, proof)
}

// WithNKeyJWTProof attaches proof supplied by a trusted HTTP transport
// integration. It must never be constructed from caller-controlled identity
// headers or from a JWT without the corresponding signer.
func WithNKeyJWTProof(ctx context.Context, proof NKeyJWTProof) context.Context {
	return context.WithValue(ctx, nkeyJWTContextKey{}, proof)
}

// WithTLSCertificate attaches a client certificate and private-key handle
// supplied by a trusted transport integration. A certificate observed in
// request.TLS.PeerCertificates is deliberately insufficient because Caddy TLS
// termination does not transfer the caller's private-key proof to NATS.
func WithTLSCertificate(ctx context.Context, certificate tls.Certificate) context.Context {
	return context.WithValue(ctx, tlsCertificateContextKey{}, certificate)
}

func hasTrustedProof(ctx context.Context) bool {
	return hasNKeyProof(ctx) || hasNKeyJWTProof(ctx) || hasTLSProof(ctx)
}

func hasNKeyProof(ctx context.Context) bool {
	_, ok := ctx.Value(nkeyContextKey{}).(NKeyProof)
	return ok
}

func hasNKeyJWTProof(ctx context.Context) bool {
	_, ok := ctx.Value(nkeyJWTContextKey{}).(NKeyJWTProof)
	return ok
}

func hasTLSProof(ctx context.Context) bool {
	_, ok := ctx.Value(tlsCertificateContextKey{}).(tls.Certificate)
	return ok
}

func bearerToken(request *http.Request, limit int) (string, error) {
	values := request.Header.Values("Authorization")
	if len(values) == 0 {
		return "", ErrCredentialMissing
	}
	if len(values) != 1 || strings.Contains(values[0], ",") {
		return "", ErrCredentialAmbiguous
	}
	scheme, credential, ok := strings.Cut(values[0], " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") || !validTextCredential(credential, limit) {
		return "", ErrCredentialMalformed
	}
	return credential, nil
}

func basicCredentials(request *http.Request, limit int) (string, string, error) {
	values := request.Header.Values("Authorization")
	if len(values) == 0 {
		return "", "", ErrCredentialMissing
	}
	if len(values) != 1 || strings.Contains(values[0], ",") {
		return "", "", ErrCredentialAmbiguous
	}
	username, password, ok := request.BasicAuth()
	if !ok || !validBasicCredential(username, limit) || !validBasicCredential(password, limit) {
		return "", "", ErrCredentialMalformed
	}
	return username, password, nil
}

func validBasicCredential(value string, limit int) bool {
	if value == "" || len(value) > limit || !utf8.ValidString(value) {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
}

func validTextCredential(value string, limit int) bool {
	if value == "" || len(value) > limit || !utf8.ValidString(value) || strings.TrimSpace(value) != value {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) || unicode.IsSpace(character) {
			return false
		}
	}
	return true
}
