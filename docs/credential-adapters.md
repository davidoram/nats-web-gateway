# Credential adapters

Credential adapters translate one explicitly configured HTTP credential
presentation into NATS client options. They do not validate a bearer token,
password, JWT, certificate identity, or authorization claim. NATS connection
establishment is the authentication result; the resulting NATS account and
subject permissions are the authorization result.

The adapter package deliberately keeps credential presentation separate from
connection ownership. A protected route binds the configured adapter to a
bounded security-context connection pool. The gateway derives a one-way,
mechanism-scoped in-memory key from each presentation and reuses a connection
only for an exact key match; the key and credentials are never logged or
exposed. NATS connection success, not the key, is the authentication result.

## Supported mappings

| Adapter | Accepted HTTP-side presentation | NATS option | Proof and termination semantics |
| --- | --- | --- | --- |
| `bearer_token` | Exactly one `Authorization: Bearer <opaque-token>` value | CONNECT token | Bearer possession terminates at Caddy. The gateway forwards the opaque value to NATS, commonly for Auth Callout validation, without parsing or validating it. Replay resistance, expiry, and revocation belong to the selected NATS/Auth Callout mechanism. |
| `user_password` | Exactly one valid HTTP Basic authorization value with non-empty username and password | CONNECT user/password | Password knowledge terminates at Caddy. The gateway decodes but does not validate the pair, and NATS authenticates it. HTTP and NATS transport encryption are required in production. |
| `nkey` | A trusted transport integration supplies the public user NKey and a live nonce-signing callback | CONNECT NKey and signature | Proof is preserved because the private key does not become a public-key header or bearer assertion: the callback signs the fresh NATS server nonce. A caller-controlled header containing an NKey is rejected as insufficient proof. |
| `nkey_jwt` | A trusted transport integration supplies the user JWT callback and its corresponding live NKey nonce signer | CONNECT user JWT and signature | The JWT is presented unchanged and the signer proves possession against the NATS nonce. The gateway does not inspect claims. A JWT header without the signer is rejected. |
| `tls` | A trusted transport integration supplies a usable `tls.Certificate` with its private-key handle | NATS client TLS certificate | The NATS connection performs a new TLS private-key operation. A certificate merely observed in `request.TLS.PeerCertificates` after Caddy terminates mTLS is not transferable proof and is rejected. |

Text credentials default to an 8 KiB per-value limit. Empty values, control
characters, whitespace inside bearer/NKey values, multiple authorization
values, combined presentations, unsupported mechanisms, missing signing
callbacks, and missing TLS private keys fail closed. HTTP Basic passwords retain
valid spaces and colons. Adapter errors identify only the failure class and
never include credential values.

## Trust boundary requirements

- Configure one adapter mechanism for a protected security context; never probe
  mechanisms in sequence or fall back to the operator connection after failure.
- Construct NKey/JWT and TLS proof contexts only inside trusted gateway or Caddy
  integration code. Caller headers, payloads, query values, and certificate
  fingerprints are not proof-of-possession handles.
- Do not log, trace, meter, serialize, or include credentials or authenticated
  identity attributes in errors. Do not derive downstream identity from adapter
  selection or successful connection establishment.
- Require HTTPS for credential-bearing HTTP routes and encrypted NATS transport
  whenever credentials cross a network. TLS policy and trust stores remain
  deployment configuration, not identity decisions made by an adapter.
- Never share the resulting option or connection across security contexts.
  Configure explicit cardinality, idle, and maximum-lifetime bounds on every
  protected route. Exhaustion fails closed instead of using the operator
  connection.

The real-NATS integration suite verifies bearer token, HTTP Basic, and NKey
nonce authentication. JWT and TLS option construction is tested directly so
their proof-bearing callbacks and private-key handles remain intact without
placing long-lived secrets in repository fixtures.

The NKey/JWT callback remains bounded and live for NATS reconnects rather than
being replaced with a snapshot. When it rotates to a different JWT, the
connection pool detects the changed credential identity, retires the prior
entry, and prevents the refreshed JWT from inheriting that entry's connection.
