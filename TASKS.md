# Ordered Tasks

Tasks are executed in numeric order unless dependencies permit an explicitly approved exception. A task is complete only after its pull request is reviewed and merged. The task-delivery PR must change its selected item to `[x]` and add the PR link.

## Foundation

- [x] **OSS-001 — Record the initial threat model and protocol decisions.** Document trust boundaries, assets, attacker capabilities, HTTP/NATS failure mappings, Core NATS versus JetStream guarantees, and the decision to delegate authentication and authorization to NATS. Add architecture decision records for irreversible choices. ([PR #1](https://github.com/davidoram/nats-web-gateway/pull/1))
- [ ] **OSS-002 — Scaffold the Go module and quality gates.** Establish package boundaries, Caddy module registration, formatting, linting, unit tests, race tests, coverage reporting, dependency scanning, SBOM generation, and reproducible local commands.
- [ ] **OSS-003 — Build the local integration environment.** Provide pinned Caddy and NATS development configurations, container orchestration, test credentials, readiness checks, and a minimal ADR-32 example service.
- [ ] **OSS-004 — Define and validate gateway configuration.** Implement JSON configuration, Caddyfile adaptation, route definitions, subject templates, header allowlists, timeouts, limits, and fail-closed validation.

## HTTP request/reply

- [ ] **OSS-005 — Implement the NATS connection lifecycle.** Provision least-privilege connections, reconnect behavior, health state, draining, overlapping Caddy reloads, and deterministic cleanup.
- [ ] **OSS-006 — Implement HTTP-to-NATS request/reply.** Translate method, path parameters, query values, allowlisted headers, and bounded bodies; propagate cancellation and deadlines; return bounded responses.
- [ ] **OSS-007 — Implement deterministic error mapping.** Cover no responders, timeout, cancellation, permission failure, malformed replies, payload limits, and ADR-32 `Nats-Service-Error` headers.
- [ ] **OSS-008 — Add ADR-32 discovery and compatibility tests.** Verify service endpoint conventions, status/discovery interoperability, queue behavior, metadata, and error semantics against a real NATS service.

## Identity and policy

- [ ] **OSS-009 — Implement HTTP-to-NATS credential adapters.** Present explicitly supported HTTP credentials as NATS client authentication options without validating identity in the gateway; cover Auth Callout bearer-token, user/password, NKey/JWT, and TLS mappings where their proof-of-possession semantics can be preserved; reject unsupported or ambiguous mappings.
- [ ] **OSS-010 — Enforce per-security-context NATS authorization.** Create and isolate NATS connections by authenticated security context, preserve account and subject permissions, prevent credential or connection reuse across contexts, bound connection cardinality and lifetime, and map authentication and permission failures safely.
- [ ] **OSS-011 — Define trustworthy downstream identity context.** Determine which authenticated identity attributes NATS exposes for each supported mechanism, forward only protocol-authenticated and integrity-protected context, prohibit caller-asserted identity, and document mechanisms for which end-user identity propagation is unavailable.

## HTTP streaming

- [ ] **OSS-012 — Implement Core NATS to SSE streaming.** Add live subscriptions with bounded buffers, heartbeats, cancellation, connection duration, slow-consumer behavior, quotas, and end-to-end tests.
- [ ] **OSS-013 — Implement JetStream to resumable SSE.** Define consumer ownership, start positions, `Last-Event-ID`, acknowledgement policy, redelivery, disconnect behavior, retention constraints, and safe cleanup.
- [ ] **OSS-014 — Add streaming load and resilience tests.** Exercise slow clients, reconnects, server restarts, Caddy reloads, buffer exhaustion, cancellation races, and resource-leak detection.

## Production readiness

- [ ] **OSS-015 — Add metrics, tracing, health, and safe logging.** Integrate structured logs, bounded-cardinality metrics, trace propagation, readiness semantics, and redaction tests.
- [ ] **OSS-016 — Publish the five-minute developer experience.** Provide a Docker image, example application, example service, Caddyfile, curl/browser walkthrough, troubleshooting guide, and security guidance.
- [ ] **OSS-017 — Harden and release v0.1.0.** Complete dependency and vulnerability review, fuzzing, compatibility matrix, benchmarks, SBOM, checksums, release automation, and documented known limitations.
