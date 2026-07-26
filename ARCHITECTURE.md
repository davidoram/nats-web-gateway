# Architecture

This document is the binding architectural authority for this repository. Every design, implementation, test, documentation change, and pull request must comply with it. A change to these rules requires a dedicated architecture pull request that explains the motivation, alternatives, compatibility impact, security impact, and migration plan.

## 1. Product boundary

NATS Web Gateway is a Caddy HTTP handler written in Go. It translates explicitly configured HTTP interactions into NATS primitives. It is a data-plane component, not an identity provider, NATS control plane, general-purpose API management platform, or arbitrary subject proxy.

The open-source edition must remain independently useful and production-capable. Commercial functionality may extend public interfaces but must not be required for correctness, baseline security, safe operation, or documented open-source features.

## 2. Architectural priorities

When requirements conflict, prefer in this order:

1. Security and tenant isolation
2. Correctness and explicit delivery semantics
3. Availability and bounded resource use
4. Compatibility and operability
5. Performance
6. Convenience

Never hide a semantic compromise behind a configuration default.

## 3. Component model

- Caddy owns HTTP/TLS lifecycle, routing integration, configuration loading, logging, and graceful reload behavior.
- The gateway handler owns HTTP-to-NATS translation, policy enforcement, identity envelopes, limits, metrics, and error mapping.
- NATS owns messaging, accounts, subject permissions, request/reply, and JetStream persistence.
- Application services own domain logic and must not depend on Caddy internals.
- Authentication adapters validate external credentials. Authorization decisions remain explicit gateway policy plus NATS permissions.

Dependencies must point inward toward small interfaces. Core translation and policy packages must be testable without running Caddy or a networked NATS server.

## 4. Configuration

- All exposed routes and subject templates must be declared; arbitrary caller-supplied subjects are forbidden.
- Configuration must validate before serving traffic and fail closed on ambiguity.
- Route templates must define allowed HTTP methods, subject construction, timeouts, payload limits, response behavior, and streaming mode.
- Reloads must support overlapping Caddy module instances without shared mutable global state.
- Secrets must be supplied through appropriate secret sources and never serialized into logs, metrics, errors, or generated documentation.

## 5. HTTP request/reply semantics

- Cancellation and deadlines must propagate from HTTP to NATS operations.
- Request bodies and response payloads must have configurable hard limits.
- Header forwarding must use allowlists. Hop-by-hop, credential, and internal identity headers must never pass through implicitly.
- ADR-32 service errors must map deterministically to HTTP responses while retaining safe diagnostic context.
- A timeout, no-responder condition, cancellation, permission denial, malformed reply, and internal failure must remain distinguishable.
- Retries are disabled by default and may only be enabled where duplicate execution is explicitly safe.

## 6. Streaming semantics

- Server-Sent Events are the initial HTTP streaming transport.
- Core NATS live subscriptions and JetStream-backed resumable streams are distinct modes and must never imply equivalent delivery guarantees.
- Every stream must enforce bounded buffers, maximum connection duration, idle handling, slow-consumer policy, cancellation, and connection quotas.
- Subject wildcards and template parameters require explicit policy validation.
- JetStream acknowledgement and resume behavior must be documented and tested before release.

## 7. Identity and authorization

- OIDC/OAuth authentication at the HTTP boundary and NATS Auth Callout are separate concerns.
- The gateway must never trust identity claims copied by an untrusted HTTP or browser client into application headers or payloads.
- Downstream identity context must use a versioned, integrity-protected envelope generated only after authentication and authorization.
- The envelope must support issuer, subject, audience, tenant, issue/expiry time, request ID, and explicitly allowlisted claims.
- Key rotation, clock skew, replay exposure, disclosure minimization, and downstream verification must be designed and tested.
- NATS connection identities must have least-privilege publish/subscribe permissions. Gateway policy cannot be the only isolation boundary.
- Authentication or policy failure must fail closed.

## 8. Go engineering rules

- Use supported stable Go versions and idiomatic standard-library patterns.
- Accept `context.Context` at operation boundaries; never store request contexts in long-lived structs.
- Make ownership of goroutines, subscriptions, connections, timers, and channels explicit.
- Every started goroutine needs a defined stop condition and testable cleanup path.
- Avoid package globals and hidden initialization side effects except Caddy module registration.
- Keep interfaces small and consumer-owned. Wrap external packages only at meaningful architectural boundaries.
- Errors must preserve causes and add actionable context without secrets.
- Public APIs require Go documentation and compatibility consideration.

## 9. Verification requirements

Every behavioral change must include appropriate tests. The expected layers are:

- unit tests for translation, validation, policy, identity, and error mapping
- integration tests with real Caddy and NATS processes for protocol boundaries
- race-enabled tests for concurrency-sensitive code
- fuzz or property tests for parsers, templates, headers, and untrusted input
- load/backpressure tests for streaming and connection lifecycle changes

Pull requests must report commands run and coverage impact. Coverage is evidence, not a target to game; critical security and concurrency paths require direct assertions. No test may depend on public internet services.

## 10. Observability and privacy

- Use structured Caddy logging and OpenTelemetry-compatible concepts.
- Propagate or create request and trace identifiers without accepting spoofable internal identifiers.
- Metrics must avoid unbounded labels such as raw subjects, paths, tenants, or user IDs.
- Logs must not contain tokens, cookies, credentials, full identity envelopes, or sensitive payloads.
- Health and readiness must distinguish process health from NATS connectivity and configuration validity.

## 11. Compatibility and releases

- Configuration and public Go APIs follow semantic versioning once released.
- Breaking configuration changes require migration documentation and a deprecation period when feasible.
- Pin dependencies deliberately and review Caddy/NATS upgrade behavior.
- Release artifacts must be reproducible, checksummed, vulnerability-scanned, and accompanied by a software bill of materials.

## 12. Pull-request architecture declaration

Every PR must state:

- which architecture sections apply
- how the change complies
- security and privacy effects
- delivery and failure semantics
- tests and coverage evidence
- operational or migration impact
- any deliberate exceptions, which require an architecture change first
