# OSS-021 — Immediately revoke pooled security-context connections

## Identity and policy

Allow a deployment-selected NATS Auth Callout or identity system, including Ory
Hydra integrations, to notify the Caddy gateway that an authenticated security
context has been revoked so every matching pooled NATS connection is invalidated
immediately rather than remaining usable until `max_lifetime` expires.

This is a new privileged control-plane boundary. Add an architecture decision
record before implementation that defines the revocation protocol, trust model,
authentication, authorization, delivery semantics, replay protection, and
multi-instance behavior. The design must remain provider-neutral: Hydra may be
an integration fixture, but the gateway must not become an OAuth2/OIDC verifier,
identity provider, or independent authorization authority.

Define a revocation identifier that can be correlated with an in-memory pooled
security context without exposing bearer tokens, passwords, private keys,
authenticated identity attributes, raw credential digests, or internal
connection identifiers. Do not accept caller-selected NATS subjects, accounts,
permissions, or replacement credentials through the revocation interface.

The revocation interface must:

- require an explicitly configured, strongly authenticated caller and least-
  privilege authorization, with production-safe transport security;
- reject malformed, oversized, ambiguous, expired, incorrectly authenticated,
  and replayed notifications before mutating connection state;
- use bounded request bodies, concurrency, processing time, replay state, and
  revocation state;
- avoid revealing whether a credential, user, tenant, pool entry, or connection
  currently exists;
- redact revocation identifiers, credentials, identity attributes, and
  authorization payloads from logs, metrics, errors, traces, and responses;
- atomically prevent matching contexts from being newly acquired while closing
  or draining all matching pooled connections owned by the handler;
- define what happens to in-flight HTTP/NATS operations and preserve an explicit,
  safe HTTP failure classification for interrupted requests;
- be race-safe with simultaneous acquire, release, reconnect, expiry, duplicate
  notifications, shutdown, and overlapping Caddy reloads; and
- acknowledge success only under documented delivery semantics, including what
  the caller must do when multiple independently running gateway instances exist.

Do not introduce shared mutable package globals. If immediate revocation across
multiple gateway processes requires fan-out, define a bounded deployment model
and failure semantics rather than implying that an HTTP call to one instance
revokes connections owned by every instance. Preserve the existing bounded
`max_lifetime` behavior as a fail-safe when a notification is lost or delayed.

## Integration coverage

Extend the OSS-020 Hydra/Auth Callout fixture, or add an equivalent provider-
neutral fixture, to prove:

1. a valid bearer credential is accepted and its NATS connection is pooled;
2. the identity system revokes that credential and sends an authenticated
   revocation notification to the gateway;
3. the matching pooled connection is closed without waiting for its configured
   maximum lifetime;
4. a validly formed subsequent HTTP request using the revoked credential is
   denied and cannot fall back to an operator, static, cached, or broader NATS
   identity; and
5. unrelated security contexts and in-flight requests receive the documented
   behavior and are not disconnected accidentally.

Provide direct tests for notification authentication and authorization,
tampering, replay, duplicate delivery, unknown identifiers, cardinality and
body limits, acquisition and reconnect races, overlapping reloads, multiple
gateway instances, partial fan-out failure, cleanup, and absence of sensitive
data in diagnostics. Include race-enabled tests and deterministic real-boundary
integration coverage in the canonical Mage `integration` and `ci` targets.

Document configuration, key or certificate rotation, operator procedures,
failure recovery, audit-safe observability, the remaining `max_lifetime`
fail-safe, and the exact distinction between identity-system revocation and
NATS authorization decisions.

## Dependencies

- OSS-010 — Enforce per-security-context NATS authorization.
- OSS-020 — Test NATS Auth Callout with Ory Hydra.
