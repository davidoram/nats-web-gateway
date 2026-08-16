package credentials

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"github.com/nats-io/nats.go"
)

// Context contains the opaque cache identity and NATS options for one adapted
// HTTP security context. Key is a one-way digest and is safe only for in-memory
// equality; it must not be logged or otherwise exposed.
type Context struct {
	Key      [sha256.Size]byte
	Options  []nats.Option
	identity *identityTracker
}

type identityTracker struct {
	mu         sync.Mutex
	key        [sha256.Size]byte
	generation uint64
	refresh    func() (string, error)
	mechanism  Mechanism
}

// RefreshIdentity refreshes callback-backed credentials before pool lookup.
func (security Context) RefreshIdentity() ([sha256.Size]byte, uint64, error) {
	if security.identity == nil {
		return security.Key, 0, nil
	}
	if security.identity.refresh != nil {
		credential, err := security.identity.refresh()
		if err != nil {
			return [sha256.Size]byte{}, 0, err
		}
		security.identity.observe(credential)
	}
	key, generation := security.identity.snapshot()
	return key, generation, nil
}

// ObservedIdentity reports the last credential identity used by a NATS option.
func (security Context) ObservedIdentity() ([sha256.Size]byte, uint64) {
	if security.identity == nil {
		return security.Key, 0
	}
	return security.identity.snapshot()
}

// IdentityChanged reports whether a callback-backed credential rotated after
// the supplied generation authenticated a pooled connection.
func (security Context) IdentityChanged(generation uint64) bool {
	_, current := security.ObservedIdentity()
	return current != generation
}

func (tracker *identityTracker) observe(credential string) {
	key := credentialKey(tracker.mechanism, credential)
	tracker.mu.Lock()
	if tracker.key != key {
		tracker.key = key
		tracker.generation++
	}
	tracker.mu.Unlock()
}

func (tracker *identityTracker) snapshot() ([sha256.Size]byte, uint64) {
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	return tracker.key, tracker.generation
}

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
	securityContext, err := adapter.adapt(request, false)
	if err != nil {
		return nil, err
	}
	return securityContext.Options, nil
}

// Adapt translates one credential presentation and derives a mechanism-scoped,
// one-way identity for connection isolation. It does not authenticate the
// credential; successful NATS connection establishment remains authoritative.
func (adapter Adapter) Adapt(request *http.Request) (Context, error) {
	return adapter.adapt(request, true)
}

func (adapter Adapter) adapt(request *http.Request, identify bool) (Context, error) {
	if err := adapter.Validate(); err != nil {
		return Context{}, err
	}
	if request == nil {
		return Context{}, ErrCredentialMissing
	}
	limit := adapter.MaxCredentialBytes
	if limit == 0 {
		limit = DefaultMaxCredentialBytes
	}

	switch adapter.Mechanism {
	case MechanismBearerToken:
		if hasTrustedProof(request.Context()) {
			return Context{}, ErrCredentialAmbiguous
		}
		token, err := bearerToken(request, limit)
		if err != nil {
			return Context{}, err
		}
		return adaptedContext(adapter.Mechanism, token, nats.Token(token)), nil
	case MechanismUserPassword:
		if hasTrustedProof(request.Context()) {
			return Context{}, ErrCredentialAmbiguous
		}
		username, password, err := basicCredentials(request, limit)
		if err != nil {
			return Context{}, err
		}
		return adaptedContext(adapter.Mechanism, username+"\x00"+password, nats.UserInfo(username, password)), nil
	case MechanismNKey:
		if len(request.Header.Values("Authorization")) != 0 || hasNKeyJWTProof(request.Context()) || hasTLSProof(request.Context()) {
			return Context{}, ErrCredentialAmbiguous
		}
		proof, ok := request.Context().Value(nkeyContextKey{}).(NKeyProof)
		if !ok || !validTextCredential(proof.PublicKey, limit) || proof.Sign == nil {
			return Context{}, ErrProofUnavailable
		}
		return adaptedContext(adapter.Mechanism, proof.PublicKey, nats.Nkey(proof.PublicKey, proof.Sign)), nil
	case MechanismNKeyJWT:
		if len(request.Header.Values("Authorization")) != 0 || hasNKeyProof(request.Context()) || hasTLSProof(request.Context()) {
			return Context{}, ErrCredentialAmbiguous
		}
		proof, ok := request.Context().Value(nkeyJWTContextKey{}).(NKeyJWTProof)
		if !ok || proof.JWT == nil || proof.Sign == nil {
			return Context{}, ErrProofUnavailable
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
		if !identify {
			return Context{Options: []nats.Option{nats.UserJWT(boundedJWT, proof.Sign)}}, nil
		}
		jwt, err := boundedJWT()
		if err != nil {
			return Context{}, err
		}
		tracker := &identityTracker{key: credentialKey(adapter.Mechanism, jwt), refresh: boundedJWT, mechanism: adapter.Mechanism}
		refreshingJWT := func() (string, error) {
			refreshed, refreshErr := boundedJWT()
			if refreshErr != nil {
				return "", refreshErr
			}
			tracker.observe(refreshed)
			return refreshed, nil
		}
		return Context{Key: tracker.key, Options: []nats.Option{nats.UserJWT(refreshingJWT, proof.Sign)}, identity: tracker}, nil
	case MechanismTLS:
		if len(request.Header.Values("Authorization")) != 0 || hasNKeyProof(request.Context()) || hasNKeyJWTProof(request.Context()) {
			return Context{}, ErrCredentialAmbiguous
		}
		certificate, ok := request.Context().Value(tlsCertificateContextKey{}).(tls.Certificate)
		if !ok || len(certificate.Certificate) == 0 || certificate.PrivateKey == nil {
			return Context{}, ErrProofUnavailable
		}
		config := &tls.Config{
			MinVersion:   tls.VersionTLS12,
			Certificates: []tls.Certificate{certificate},
		}
		fingerprint := sha256.Sum256(certificate.Certificate[0])
		return adaptedContext(adapter.Mechanism, string(fingerprint[:]), nats.Secure(config)), nil
	default:
		panic("validated credential mechanism has no adapter")
	}
}

func adaptedContext(mechanism Mechanism, credential string, options ...nats.Option) Context {
	key := credentialKey(mechanism, credential)
	return Context{Key: key, Options: options}
}

func credentialKey(mechanism Mechanism, credential string) [sha256.Size]byte {
	return sha256.Sum256(append(append([]byte(mechanism), 0), credential...))
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
