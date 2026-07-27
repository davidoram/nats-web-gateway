# ADR 0001: Use declared routes and deterministic failure mapping

- Status: Accepted
- Date: 2026-07-27
- Applies to: Architecture §§1–5, 7, 9–10

## Context

An HTTP gateway could accept a caller-provided NATS subject and act as a generic
proxy. That would make the HTTP boundary a subject-enumeration and privilege-
escalation surface, couple clients to internal topology, and make safe NATS
permissions difficult to audit. HTTP and NATS also expose different failure
models: no responders, deadline expiry, cancellation, permission denial,
malformed replies, connectivity failure, and ADR-32 service errors are not
interchangeable.

## Decision

Every exposed operation is a validated configuration-time route. A route fixes
its HTTP methods, subject template, accepted parameter grammar, header allowlist,
timeouts, payload limits, response behavior, and streaming mode. Caller values
may fill only explicitly declared and validated template positions. They can
never supply an arbitrary subject or wildcard.

Each HTTP security context uses a corresponding NATS connection identity. NATS
accounts and least-privilege publish and subscribe permissions make the
authorization decision. Declared routes constrain the gateway's exposed attack
surface but do not grant authority or replace NATS permissions. Ambiguous or
overlapping configuration fails before serving.

The gateway owns deterministic HTTP failure mapping. It distinguishes invalid
input, authentication, authorization/permission denial, no responders, timeout,
client cancellation, connectivity failure, ADR-32 service errors, malformed or
oversized replies, and internal failure. The normative initial mapping is in
`THREAT_MODEL.md`. Protocol-derived status and headers are validated before use,
and public diagnostics never disclose raw subjects, credentials, or sensitive
payloads.

ADR-32 defines `Nats-Service-Error-Code` as a numeric application error code,
not an HTTP status. Each route therefore declares any permitted application
code-to-HTTP-status mappings. HTTP statuses are restricted to `400`–`599`.
Valid but unmapped service errors become `502 Bad Gateway`; arbitrary upstream
codes or descriptions are never copied directly to the HTTP response. See the
[NATS Service API specification](https://github.com/nats-io/nats-architecture-and-design/blob/main/adr/ADR-32.md#error-handling).

Requests are published at most once by default. A timeout or cancellation after
publication is an ambiguous application outcome, not proof of non-execution.
Automatic retries are disabled; a later opt-in requires explicit duplicate-safe
semantics and architecture review.

## Consequences

- Configuration is more verbose, but exposure and delivery behavior are
  reviewable before deployment.
- Adding a new operation requires an operator change rather than allowing
  runtime subject selection.
- Services and clients receive stable failure classes across NATS library
  implementation details.
- Least-privilege NATS permissions must be provisioned and tested as the
  authorization authority.
- Implementations require bounded parsing and direct tests for every failure
  class, including malformed ADR-32 metadata and cancellation races.

## Alternatives rejected

- **Caller-supplied subjects:** rejected because validation cannot reliably
  recover a safe authorization boundary from an arbitrary subject namespace.
- **Independent gateway authorization policy:** rejected because it duplicates
  NATS authorization, risks inconsistent decisions, and can collapse NATS
  account and subject-permission boundaries.
- **Map every upstream failure to 500 or 503:** rejected because it hides
  actionable delivery semantics and encourages unsafe retries.
- **Retry timeouts automatically:** rejected because request execution can
  already have occurred and duplicate safety is application-specific.
