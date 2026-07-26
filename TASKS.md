# Ordered Tasks

Tasks are executed in numeric order unless dependencies permit an explicitly approved exception. A task is complete only after its pull request is reviewed and merged. The task-delivery PR must change its selected item to `[x]` and add the PR link.

## Foundation

- [ ] **OSS-001 — Record the initial threat model and protocol decisions.** Document trust boundaries, assets, attacker capabilities, HTTP/NATS failure mappings, Core NATS versus JetStream guarantees, and the signed identity-envelope decision. Add architecture decision records for irreversible choices.
- [ ] **OSS-002 — Scaffold the Go module and quality gates.** Establish package boundaries, Caddy module registration, formatting, linting, unit tests, race tests, coverage reporting, dependency scanning, SBOM generation, and reproducible local commands.
- [ ] **OSS-003 — Build the local integration environment.** Provide pinned Caddy and NATS development configurations, container orchestration, test credentials, readiness checks, and a minimal ADR-32 example service.
- [ ] **OSS-004 — Define and validate gateway configuration.** Implement JSON configuration, Caddyfile adaptation, route definitions, subject templates, header allowlists, timeouts, limits, and fail-closed validation.

## HTTP request/reply

- [ ] **OSS-005 — Implement the NATS connection lifecycle.** Provision least-privilege connections, reconnect behavior, health state, draining, overlapping Caddy reloads, and deterministic cleanup.
- [ ] **OSS-006 — Implement HTTP-to-NATS request/reply.** Translate method, path parameters, query values, allowlisted headers, and bounded bodies; propagate cancellation and deadlines; return bounded responses.
- [ ] **OSS-007 — Implement deterministic error mapping.** Cover no responders, timeout, cancellation, permission failure, malformed replies, payload limits, and ADR-32 `Nats-Service-Error` headers.
- [ ] **OSS-008 — Add ADR-32 discovery and compatibility tests.** Verify service endpoint conventions, status/discovery interoperability, queue behavior, metadata, and error semantics against a real NATS service.

## Identity and policy

- [ ] **OSS-009 — Add OIDC JWT validation.** Support issuer, audience, JWKS caching and rotation, expiry, clock skew, algorithm restrictions, safe failure modes, and test identity providers.
- [ ] **OSS-010 — Implement route authorization policy.** Map authenticated claims to declared routes and subject templates, enforce least privilege, and produce auditable denial reasons without disclosing sensitive details.
- [ ] **OSS-011 — Implement the trusted identity envelope.** Define a versioned signed format, key loading and rotation, downstream verification library, claim minimization, audience binding, request correlation, and replay analysis.

## HTTP streaming

- [ ] **OSS-012 — Implement Core NATS to SSE streaming.** Add live subscriptions with bounded buffers, heartbeats, cancellation, connection duration, slow-consumer behavior, quotas, and end-to-end tests.
- [ ] **OSS-013 — Implement JetStream to resumable SSE.** Define consumer ownership, start positions, `Last-Event-ID`, acknowledgement policy, redelivery, disconnect behavior, retention constraints, and safe cleanup.
- [ ] **OSS-014 — Add streaming load and resilience tests.** Exercise slow clients, reconnects, server restarts, Caddy reloads, buffer exhaustion, cancellation races, and resource-leak detection.

## Production readiness

- [ ] **OSS-015 — Add metrics, tracing, health, and safe logging.** Integrate structured logs, bounded-cardinality metrics, trace propagation, readiness semantics, and redaction tests.
- [ ] **OSS-016 — Publish the five-minute developer experience.** Provide a Docker image, example application, example service, Caddyfile, curl/browser walkthrough, troubleshooting guide, and security guidance.
- [ ] **OSS-017 — Harden and release v0.1.0.** Complete dependency and vulnerability review, fuzzing, compatibility matrix, benchmarks, SBOM, checksums, release automation, and documented known limitations.
