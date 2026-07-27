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
- The gateway handler owns HTTP-to-NATS translation, declared-route enforcement, credential presentation, connection isolation, limits, metrics, and error mapping.
- NATS owns messaging, accounts, subject permissions, request/reply, and JetStream persistence.
- Application services own domain logic and must not depend on Caddy internals.
- Credential adapters translate supported HTTP credential presentations into NATS client authentication options without validating identity or deciding access. NATS authenticates each client identity and NATS accounts and subject permissions are the authorization authority.

Dependencies must point inward toward small interfaces. Core translation,
route-enforcement, and credential-adapter packages must be testable without
running Caddy or a networked NATS server.

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

- Authentication and authorization are delegated to NATS. The gateway is not an identity provider, OAuth/OIDC verifier, user registry, or independent authorization policy engine.
- Each protected HTTP security context must use a corresponding NATS connection authenticated as that context; a shared gateway connection must not collapse distinct end-user authorization domains.
- Credential adapters may present bearer tokens, user/password credentials, NKey/JWT material, TLS credentials, or other explicitly supported NATS client options. They transport or transform credential presentation only; successful NATS connection establishment is the authentication result.
- NATS may authenticate and authorize through Auth Callout backed by OAuth or another IAM system, static configuration, decentralized JWTs, NKeys, TLS certificates, or any other deployment-selected NATS mechanism compatible with the adapter.
- NATS accounts and publish/subscribe permissions are the authorization authority. Declared gateway routes and subject templates reduce exposed surface but must not grant access beyond the permissions of the authenticated NATS identity.
- The gateway must never trust identity claims copied by an untrusted HTTP or browser client into application headers or payloads, and must not manufacture a claim that NATS has not authenticated and exposed through a trustworthy protocol.
- Credential-to-NATS mappings must document their proof-of-possession and termination semantics. Methods that cannot be mapped safely from the HTTP request must be unavailable and fail closed.
- Authentication, connection authorization, or permission failure must fail closed. Credential material must never be pooled across security contexts or serialized into logs, metrics, errors, or generated documentation.

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

- unit tests for translation, validation, routes, credential adapters, connection isolation, and error mapping
- integration tests with real Caddy and NATS processes for protocol boundaries
- race-enabled tests for concurrency-sensitive code
- fuzz or property tests for parsers, templates, headers, and untrusted input
- load/backpressure tests for streaming and connection lifecycle changes

Pull requests must report commands run and coverage impact. Coverage is evidence, not a target to game; critical security and concurrency paths require direct assertions. No test may depend on public internet services.

## 10. Observability and privacy

- Use structured Caddy logging and OpenTelemetry-compatible concepts.
- Propagate or create request and trace identifiers without accepting spoofable internal identifiers.
- Metrics must avoid unbounded labels such as raw subjects, paths, tenants, or user IDs.
- Logs must not contain tokens, cookies, credentials, authenticated identity attributes, or sensitive payloads.
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
