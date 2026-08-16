package credentials

import (
	"crypto"
	"crypto/tls"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nats-io/nats.go"
)

func TestBearerTokenOptions(t *testing.T) {
	t.Parallel()
	request := httptest.NewRequest(http.MethodGet, "https://example.test/", nil)
	request.Header.Set("Authorization", "Bearer opaque-token")
	options := mustOptions(t, Adapter{Mechanism: MechanismBearerToken}, request)
	if options.Token != "opaque-token" || options.User != "" || options.Password != "" {
		t.Fatalf("NATS options set unexpected authentication fields")
	}
}

func TestUserPasswordOptions(t *testing.T) {
	t.Parallel()
	request := httptest.NewRequest(http.MethodGet, "https://example.test/", nil)
	request.SetBasicAuth("alice", "correct horse: battery staple")
	options := mustOptions(t, Adapter{Mechanism: MechanismUserPassword}, request)
	if options.User != "alice" || options.Password != "correct horse: battery staple" || options.Token != "" {
		t.Fatalf("NATS options set unexpected authentication fields")
	}
}

func TestNKeyJWTOptionsPreserveNonceSigning(t *testing.T) {
	t.Parallel()
	wantNonce := []byte("server-nonce")
	wantSignature := []byte("signed-server-nonce")
	proof := NKeyJWTProof{
		JWT: func() (string, error) { return "user-jwt", nil },
		Sign: func(nonce []byte) ([]byte, error) {
			if string(nonce) != string(wantNonce) {
				t.Fatalf("nonce = %q, want %q", nonce, wantNonce)
			}
			return wantSignature, nil
		},
	}
	request := httptest.NewRequest(http.MethodGet, "https://example.test/", nil)
	request = request.WithContext(WithNKeyJWTProof(request.Context(), proof))
	options := mustOptions(t, Adapter{Mechanism: MechanismNKeyJWT}, request)
	jwt, err := options.UserJWT()
	if err != nil || jwt != "user-jwt" {
		t.Fatalf("JWT callback = %q, %v", jwt, err)
	}
	signature, err := options.SignatureCB(wantNonce)
	if err != nil || string(signature) != string(wantSignature) {
		t.Fatalf("signature callback = %q, %v", signature, err)
	}
}

func TestNKeyJWTCallbackOutputFailsClosed(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		jwt  string
	}{
		{name: "empty", jwt: ""},
		{name: "oversized", jwt: "12345"},
		{name: "invalid UTF-8", jwt: string([]byte{0xff})},
		{name: "whitespace", jwt: "two tokens"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			request := httptest.NewRequest(http.MethodGet, "https://example.test/", nil)
			proof := NKeyJWTProof{
				JWT:  func() (string, error) { return test.jwt, nil },
				Sign: func([]byte) ([]byte, error) { return nil, nil },
			}
			request = request.WithContext(WithNKeyJWTProof(request.Context(), proof))
			adapterOptions, err := (Adapter{Mechanism: MechanismNKeyJWT, MaxCredentialBytes: 4}).Options(request)
			if err != nil {
				t.Fatalf("Options() error = %v", err)
			}
			options := nats.GetDefaultOptions()
			err = adapterOptions[0](&options)
			if !errors.Is(err, ErrCredentialMalformed) {
				t.Fatalf("apply NATS option error = %v, want malformed credential", err)
			}
			if test.jwt != "" && strings.Contains(err.Error(), test.jwt) {
				t.Fatal("error disclosed JWT material")
			}
		})
	}
}

func TestBearerTokenRejectsInvalidUTF8(t *testing.T) {
	t.Parallel()
	request := httptest.NewRequest(http.MethodGet, "https://example.test/", nil)
	credential := string([]byte{0xff})
	request.Header["Authorization"] = []string{"Bearer " + credential}
	_, err := (Adapter{Mechanism: MechanismBearerToken}).Options(request)
	if !errors.Is(err, ErrCredentialMalformed) {
		t.Fatalf("Options() error = %v, want malformed credential", err)
	}
	if strings.Contains(err.Error(), credential) {
		t.Fatal("error disclosed bearer credential material")
	}
}

func TestNKeyOptionsPreserveNonceSigning(t *testing.T) {
	t.Parallel()
	proof := NKeyProof{
		PublicKey: "UAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
		Sign: func(nonce []byte) ([]byte, error) {
			return append([]byte("signed:"), nonce...), nil
		},
	}
	request := httptest.NewRequest(http.MethodGet, "https://example.test/", nil)
	request = request.WithContext(WithNKeyProof(request.Context(), proof))
	options := mustOptions(t, Adapter{Mechanism: MechanismNKey}, request)
	if options.Nkey != proof.PublicKey || options.SignatureCB == nil {
		t.Fatal("NKey option did not retain the public key and nonce signer")
	}
}

func TestTLSOptionsRequirePrivateKeyProof(t *testing.T) {
	t.Parallel()
	certificate := tls.Certificate{Certificate: [][]byte{{1, 2, 3}}, PrivateKey: stubSigner{}}
	request := httptest.NewRequest(http.MethodGet, "https://example.test/", nil)
	request = request.WithContext(WithTLSCertificate(request.Context(), certificate))
	options := mustOptions(t, Adapter{Mechanism: MechanismTLS}, request)
	if !options.Secure || options.TLSConfig == nil || options.TLSConfig.MinVersion != tls.VersionTLS12 || len(options.TLSConfig.Certificates) != 1 {
		t.Fatalf("TLS NATS options do not contain the proof-bearing certificate")
	}
}

func TestAdaptDerivesStableDistinctContextKeysAndPreservesProof(t *testing.T) {
	t.Parallel()
	bearer := httptest.NewRequest(http.MethodGet, "https://example.test/", nil)
	bearer.Header.Set("Authorization", "Bearer opaque-token")
	first, err := (Adapter{Mechanism: MechanismBearerToken}).Adapt(bearer)
	if err != nil {
		t.Fatal(err)
	}
	second, err := (Adapter{Mechanism: MechanismBearerToken}).Adapt(bearer)
	if err != nil || first.Key != second.Key {
		t.Fatalf("same credential keys differ: %x/%x (%v)", first.Key, second.Key, err)
	}
	key, generation, err := first.RefreshIdentity()
	if err != nil || key != first.Key || generation != 0 || first.IdentityChanged(generation) {
		t.Fatalf("static identity = %x/%d changed=%t (%v)", key, generation, first.IdentityChanged(generation), err)
	}
	basicRequest := httptest.NewRequest(http.MethodGet, "https://example.test/", nil)
	basicRequest.SetBasicAuth("opaque-token", "password")
	basicContext, err := (Adapter{Mechanism: MechanismUserPassword}).Adapt(basicRequest)
	if err != nil || basicContext.Key == first.Key {
		t.Fatalf("mechanism-scoped key = %x, bearer = %x (%v)", basicContext.Key, first.Key, err)
	}

	nkeyRequest := httptest.NewRequest(http.MethodGet, "https://example.test/", nil)
	nkeyRequest = nkeyRequest.WithContext(WithNKeyProof(nkeyRequest.Context(), NKeyProof{
		PublicKey: "UAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
		Sign:      func(nonce []byte) ([]byte, error) { return nonce, nil },
	}))
	if _, err := (Adapter{Mechanism: MechanismNKey}).Adapt(nkeyRequest); err != nil {
		t.Fatalf("adapt NKey: %v", err)
	}

	jwtRequest := httptest.NewRequest(http.MethodGet, "https://example.test/", nil)
	jwtRequest = jwtRequest.WithContext(WithNKeyJWTProof(jwtRequest.Context(), NKeyJWTProof{
		JWT:  func() (string, error) { return "user-jwt", nil },
		Sign: func(nonce []byte) ([]byte, error) { return nonce, nil },
	}))
	if _, err := (Adapter{Mechanism: MechanismNKeyJWT}).Adapt(jwtRequest); err != nil {
		t.Fatalf("adapt NKey JWT: %v", err)
	}

	tlsRequest := httptest.NewRequest(http.MethodGet, "https://example.test/", nil)
	tlsRequest = tlsRequest.WithContext(WithTLSCertificate(tlsRequest.Context(), tls.Certificate{Certificate: [][]byte{{1, 2, 3}}, PrivateKey: stubSigner{}}))
	if _, err := (Adapter{Mechanism: MechanismTLS}).Adapt(tlsRequest); err != nil {
		t.Fatalf("adapt TLS: %v", err)
	}
}

func TestAdaptNKeyJWTRefreshesBoundedIdentity(t *testing.T) {
	t.Parallel()
	jwt := "jwt-a"
	request := httptest.NewRequest(http.MethodGet, "https://example.test/", nil)
	request = request.WithContext(WithNKeyJWTProof(request.Context(), NKeyJWTProof{
		JWT:  func() (string, error) { return jwt, nil },
		Sign: func(nonce []byte) ([]byte, error) { return nonce, nil },
	}))
	security, err := (Adapter{Mechanism: MechanismNKeyJWT, MaxCredentialBytes: 8}).Adapt(request)
	if err != nil {
		t.Fatal(err)
	}
	firstKey, firstGeneration, err := security.RefreshIdentity()
	if err != nil || firstKey != security.Key || firstGeneration != 0 {
		t.Fatalf("initial identity = %x/%d (%v)", firstKey, firstGeneration, err)
	}
	options := nats.GetDefaultOptions()
	if err := security.Options[0](&options); err != nil {
		t.Fatal(err)
	}
	jwt = "jwt-b"
	secondKey, secondGeneration, err := security.RefreshIdentity()
	if err != nil || secondKey == firstKey || secondGeneration != firstGeneration+1 || !security.IdentityChanged(firstGeneration) {
		t.Fatalf("refreshed identity = %x/%d changed=%t (%v)", secondKey, secondGeneration, security.IdentityChanged(firstGeneration), err)
	}
	observed, callbackErr := options.UserJWT()
	observedKey, observedGeneration := security.ObservedIdentity()
	if callbackErr != nil || observed != "jwt-b" || observedKey != secondKey || observedGeneration != secondGeneration {
		t.Fatalf("observed JWT/identity = %q/%x/%d (%v)", observed, observedKey, observedGeneration, callbackErr)
	}
	jwt = "oversized"
	if _, _, err := security.RefreshIdentity(); !errors.Is(err, ErrCredentialMalformed) {
		t.Fatalf("oversized refresh error = %v", err)
	}
}

func TestAdaptersFailClosed(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		adapter   Adapter
		configure func(*http.Request) *http.Request
		want      error
	}{
		{name: "missing bearer", adapter: Adapter{Mechanism: MechanismBearerToken}, want: ErrCredentialMissing},
		{name: "wrong bearer scheme", adapter: Adapter{Mechanism: MechanismBearerToken}, configure: header("Authorization", "Token secret"), want: ErrCredentialMalformed},
		{name: "empty bearer", adapter: Adapter{Mechanism: MechanismBearerToken}, configure: header("Authorization", "Bearer "), want: ErrCredentialMalformed},
		{name: "spaced bearer", adapter: Adapter{Mechanism: MechanismBearerToken}, configure: header("Authorization", "Bearer two tokens"), want: ErrCredentialMalformed},
		{name: "oversized bearer", adapter: Adapter{Mechanism: MechanismBearerToken, MaxCredentialBytes: 4}, configure: header("Authorization", "Bearer 12345"), want: ErrCredentialMalformed},
		{name: "missing basic", adapter: Adapter{Mechanism: MechanismUserPassword}, want: ErrCredentialMissing},
		{name: "empty basic password", adapter: Adapter{Mechanism: MechanismUserPassword}, configure: basic("alice", ""), want: ErrCredentialMalformed},
		{name: "NKey without signer", adapter: Adapter{Mechanism: MechanismNKey}, want: ErrProofUnavailable},
		{name: "NKey JWT without signer", adapter: Adapter{Mechanism: MechanismNKeyJWT}, want: ErrProofUnavailable},
		{name: "NKey JWT with header", adapter: Adapter{Mechanism: MechanismNKeyJWT}, configure: header("Authorization", "Bearer jwt-is-not-proof"), want: ErrCredentialAmbiguous},
		{name: "TLS peer certificate only", adapter: Adapter{Mechanism: MechanismTLS}, configure: peerCertificateOnly, want: ErrProofUnavailable},
		{name: "TLS with header", adapter: Adapter{Mechanism: MechanismTLS}, configure: header("Authorization", "Bearer not-a-client-key"), want: ErrCredentialAmbiguous},
		{name: "bearer with trusted NKey proof", adapter: Adapter{Mechanism: MechanismBearerToken}, configure: bearerAndNKeyProof, want: ErrCredentialAmbiguous},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			request := httptest.NewRequest(http.MethodGet, "https://example.test/", nil)
			if test.configure != nil {
				request = test.configure(request)
			}
			_, err := test.adapter.Options(request)
			if !errors.Is(err, test.want) {
				t.Fatalf("Options() error = %v, want %v", err, test.want)
			}
			for _, secret := range []string{"secret", "12345", "jwt-is-not-proof", "not-a-client-key"} {
				if strings.Contains(err.Error(), secret) {
					t.Fatalf("error disclosed credential material")
				}
			}
		})
	}
}

func TestAdapterRejectsAmbiguousAuthorizationValues(t *testing.T) {
	t.Parallel()
	for _, mechanism := range []Mechanism{MechanismBearerToken, MechanismUserPassword} {
		request := httptest.NewRequest(http.MethodGet, "https://example.test/", nil)
		request.Header.Add("Authorization", "Bearer first")
		request.Header.Add("Authorization", "Bearer second")
		_, err := (Adapter{Mechanism: mechanism}).Options(request)
		if !errors.Is(err, ErrCredentialAmbiguous) {
			t.Fatalf("%s Options() error = %v, want ambiguity", mechanism, err)
		}
	}
}

func TestAdapterValidate(t *testing.T) {
	t.Parallel()
	if err := (Adapter{Mechanism: "oauth"}).Validate(); err == nil {
		t.Fatal("unsupported mechanism accepted")
	}
	if err := (Adapter{Mechanism: MechanismBearerToken, MaxCredentialBytes: -1}).Validate(); err == nil {
		t.Fatal("negative credential limit accepted")
	}
}

func FuzzBearerTokenNeverLeaksInput(f *testing.F) {
	f.Add("Bearer opaque-token")
	f.Add("Bearer two tokens")
	f.Add("Basic YWxpY2U6c2VjcmV0")
	f.Fuzz(func(t *testing.T, authorization string) {
		request := httptest.NewRequest(http.MethodGet, "https://example.test/", nil)
		request.Header["Authorization"] = []string{authorization}
		_, err := (Adapter{Mechanism: MechanismBearerToken, MaxCredentialBytes: 64}).Options(request)
		if err != nil && authorization != "" && strings.Contains(err.Error(), authorization) {
			t.Fatal("adapter error disclosed Authorization value")
		}
	})
}

func mustOptions(t *testing.T, adapter Adapter, request *http.Request) nats.Options {
	t.Helper()
	adapterOptions, err := adapter.Options(request)
	if err != nil {
		t.Fatalf("Options() error = %v", err)
	}
	options := nats.GetDefaultOptions()
	for _, option := range adapterOptions {
		if err := option(&options); err != nil {
			t.Fatalf("apply NATS option: %v", err)
		}
	}
	return options
}

func header(name, value string) func(*http.Request) *http.Request {
	return func(request *http.Request) *http.Request {
		request.Header.Set(name, value)
		return request
	}
}

func basic(username, password string) func(*http.Request) *http.Request {
	return func(request *http.Request) *http.Request {
		request.SetBasicAuth(username, password)
		return request
	}
}

func peerCertificateOnly(request *http.Request) *http.Request {
	request.TLS = &tls.ConnectionState{PeerCertificates: nil}
	return request
}

func bearerAndNKeyProof(request *http.Request) *http.Request {
	request.Header.Set("Authorization", "Bearer opaque-token")
	proof := NKeyProof{PublicKey: "user-key", Sign: func([]byte) ([]byte, error) { return nil, nil }}
	return request.WithContext(WithNKeyProof(request.Context(), proof))
}

type stubSigner struct{}

func (stubSigner) Public() crypto.PublicKey { return nil }
func (stubSigner) Sign(io.Reader, []byte, crypto.SignerOpts) ([]byte, error) {
	return nil, errors.New("not used")
}

var _ crypto.Signer = stubSigner{}
