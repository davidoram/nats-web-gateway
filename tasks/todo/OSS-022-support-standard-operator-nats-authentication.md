# OSS-022 — Support standard operator NATS authentication methods

## Configuration and identity

Allow the gateway-owned NATS connection used by routes without a
`security_context` to authenticate with any standard authentication mechanism
supported by the pinned Go NATS client and compatible NATS server:

- no credentials, for explicitly selected development or `no_auth_user`
  deployments;
- username and password;
- token;
- a user NKey with nonce-signing proof;
- a decentralized user JWT with its NKey signing proof, including standard NATS
  credentials files; and
- a TLS client certificate and private key.

This task applies only to the gateway's operator-owned connection. It must not
change the HTTP credential adapters or per-caller connection isolation provided
by OSS-009 and OSS-010. NATS remains the authentication and authorization
authority, and a failed operator authentication attempt must never fall back to
another configured identity, an end-user security context, or an anonymous
connection.

## Configuration model

Replace the username/password-specific operator authentication surface with an
explicit, extensible authentication configuration. Define equivalent JSON and
Caddyfile syntax for every supported mechanism. Exactly one operator
authentication mechanism may be selected; missing required fields, fields from
another mechanism, ambiguous combinations, and credentials embedded in NATS
server URLs must fail validation before traffic is served.

Preserve backward compatibility for the existing `nats.username`,
`nats.password`, `nats_user`, and `nats_password` settings. Document their
mapping to the new model, precedence rules, conflicts with new settings, and a
migration path. Do not silently change the identity or permissions used by an
existing configuration.

Treat TLS server verification separately from TLS client authentication.
Support secure configuration of trusted root CAs and server-name verification
for `tls` and `wss` connections without implying that a root CA authenticates
the gateway to NATS. Client-certificate authentication must require both the
certificate and its matching private key.

## Secret handling and lifecycle

Passwords, tokens, NKey seeds, user JWT credentials, and private keys are
secrets. Accept them only through production-appropriate Caddy secret sources or
protected files; do not require operators to place literal secret material in
JSON or a Caddyfile. Define and validate file ownership and permission
expectations where the gateway reads credential files. Never serialize secret
values, secret-derived identifiers, private keys, seeds, JWTs, or credentials
file contents into adapted JSON, logs, metrics, traces, errors, health output,
generated documentation, or pull-request fixtures.

Preserve proof of possession:

- NKey authentication must sign the fresh NATS server nonce rather than expose
  a seed or treat a public NKey as sufficient proof;
- user JWT authentication must present the JWT and sign the server nonce with
  the corresponding NKey; and
- TLS authentication must retain the private-key operation required by the TLS
  handshake.

Define reconnect and reload behavior for every mechanism. Callback- or
file-backed credentials must remain available for reconnects without retaining
avoidable plaintext copies. Specify whether changed credential sources are
re-read on reconnect or only on Caddy reload, how rotation becomes active, and
how malformed, missing, mismatched, expired, or unreadable rotated material
affects readiness. Overlapping Caddy instances must own independent credential
state and connections.

The operator connection must continue to use least-privilege NATS permissions
derived from its deployment configuration. Authentication success must not
grant subjects beyond the NATS account and user permissions. Routes with a
`security_context` must continue to use their caller-specific pools and must
never use the operator connection as fallback.

## Validation and failure behavior

Provide deterministic validation and runtime behavior for:

- unsupported or unknown authentication mechanisms;
- incomplete and mutually exclusive authentication fields;
- empty, malformed, oversized, or incorrectly encoded credential material;
- unreadable files, unsafe file permissions, and certificate/key mismatches;
- NKey seed, public-key, JWT, and signing-key mismatches;
- expired or not-yet-valid certificates and invalid trust chains;
- authentication and authorization failures during initial connection and
  reconnect;
- credential rotation during a reconnecting state or overlapping Caddy reload;
  and
- configurations in which every route is protected and no operator connection
  is opened until an unprotected route is introduced by a later reload.

Initial authentication failure for a required operator connection must reject
provisioning. Loss of authentication during reconnect must make the handler
unready without exposing sensitive server diagnostics. A successful reconnect
with the same configured identity restores readiness. Authentication changes
must not retry HTTP application requests or alter their at-most-once delivery
semantics.

## Tests and documentation

Add unit tests for configuration parsing, Caddyfile/JSON equivalence,
mutual-exclusion rules, secret redaction, option construction, proof callbacks,
rotation, reconnect, reload, cleanup, and backward compatibility.

Add deterministic integration tests against pinned real NATS servers for no
credentials, username/password, token, NKey, decentralized JWT credentials,
and mutual TLS. For every authenticated mechanism, prove successful use of the
least-privilege declared route and denial of an unauthorized subject. Include
negative tests for incorrect credentials, proof mismatches, invalid trust,
expired certificates where fixtures permit, and forbidden fallback. Exercise
reconnect and overlapping reload behavior under the race detector.

Update `docs/configuration.md`, runnable examples, and operator guidance with:

- a selection guide for each mechanism;
- complete JSON and Caddyfile examples that contain no literal production
  secrets;
- secret provisioning and file-permission guidance;
- certificate, NKey, JWT, token, and password rotation procedures;
- readiness and failure semantics; and
- migration examples from the existing username/password fields.

Run and report `go tool mage verify`, `go tool mage integration`,
`go tool mage security`, and `go tool mage ci`.

## Dependencies

- OSS-005 — Implement the NATS connection lifecycle.
- OSS-009 — Implement HTTP-to-NATS credential adapters.
- OSS-010 — Enforce per-security-context NATS authorization.
- OSS-019 — Document every configuration setting.
