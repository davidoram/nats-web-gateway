# ADR 0003: Delegate authentication and authorization to NATS

- Status: Accepted
- Date: 2026-07-27
- Applies to: Architecture §§1–4, 7, 9–10

## Context

NATS already authenticates client connections and authorizes their account and
subject access. Deployments can use static users, tokens, NKeys, decentralized
JWTs, TLS certificates, or
[Auth Callout](https://docs.nats.io/running-a-nats-service/configuration/securing_nats/auth_callout).
Auth Callout can delegate to an application-defined IAM backend, including
OAuth, and returns the user claims used by NATS to assign the connection's
account and permissions. The deployment may instead choose any applicable
[NATS authentication](https://docs.nats.io/running-a-nats-service/configuration/securing_nats/auth_intro)
and
[authorization](https://docs.nats.io/running-a-nats-service/configuration/securing_nats/authorization)
configuration.

Repeating OAuth/OIDC validation and route authorization in the gateway would
create two authorities whose identity, revocation, tenant, and permission
decisions can disagree. Using one shared gateway NATS identity would also erase
the end user's NATS authorization boundary.

## Decision

The gateway delegates authentication and authorization to NATS. For every
protected HTTP security context it establishes or selects an isolated NATS
connection authenticated as that context. Credential adapters convert an
explicit HTTP credential presentation into supported NATS client options, but
do not validate the user or decide which subjects are allowed. A successful
NATS connection is the authentication result; the connection's NATS account
and publish/subscribe permissions are the authorization result.

Deployments choose the NATS authentication mechanism. In an OAuth-backed
deployment, the gateway can present the bearer credential during NATS CONNECT
and Auth Callout can validate it against the deployment's previously registered
users and assign account and permissions. Other adapters may support
user/password, token, NKey/JWT, or TLS authentication when the HTTP transport
can preserve the mechanism's credential and proof-of-possession semantics.

Not every NATS mechanism is safely transferable from every HTTP client. A
client certificate terminated at Caddy or an NKey private signing operation,
for example, cannot be treated as an interchangeable bearer assertion.
Unsupported or ambiguous mappings are rejected. Credentials and live
connections are never shared across distinct security contexts, and caches are
bounded by lifetime and cardinality.

Declared HTTP routes and subject templates remain mandatory attack-surface
constraints. They may narrow what the gateway attempts, but they never grant
access that NATS denied and are not an independent user authorization policy.

The gateway does not infer authenticated end-user claims from caller headers or
from connection success alone. Downstream identity propagation is permitted
only when a NATS mechanism exposes authenticated attributes through a defined,
integrity-protected protocol. Otherwise services receive no asserted end-user
identity from the gateway and rely on NATS account/subject authorization.

## Consequences

- NATS is the single authentication and authorization authority.
- Revocation, account assignment, and subject permissions take effect through
  the configured NATS mechanism, including Auth Callout-backed IAM.
- Connection lifecycle and pooling become security-sensitive and must prevent
  cross-user reuse while bounding connection amplification.
- Supported credential adapters require mechanism-specific threat analysis and
  integration tests against a real NATS server.
- Generic signed identity envelopes are not generated from unverified HTTP
  claims; some mechanisms cannot provide downstream end-user identity context.

## Alternatives rejected

- **Validate OIDC and authorize routes in the gateway:** rejected because it
  creates a second authority that can diverge from NATS.
- **Use a shared gateway NATS principal:** rejected because it collapses
  per-user NATS account and subject permissions.
- **Treat all NATS credentials as bearer tokens:** rejected because it breaks
  proof-of-possession guarantees for NKeys and mutual TLS.
- **Copy caller identity into messages:** rejected because NATS connection
  authorization does not authenticate arbitrary message headers or payloads.
