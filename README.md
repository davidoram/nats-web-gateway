# NATS Web Gateway

An open-source Caddy module that exposes explicitly configured NATS services and streams to HTTP clients.

The project will initially provide:

- HTTP request/reply routes backed by NATS subjects
- NATS Service API (ADR-32) compatible behavior
- Server-Sent Events for bounded, policy-controlled subscriptions
- authentication and authorization delegated to NATS, including Auth Callout-backed OAuth deployments
- per-security-context NATS connections that preserve NATS account and subject permissions
- production-oriented limits, observability, cancellation, and graceful shutdown

The project is at the architecture and implementation-planning stage. See
[ARCHITECTURE.md](ARCHITECTURE.md) for binding design rules,
[THREAT_MODEL.md](THREAT_MODEL.md) for trust boundaries and protocol security
decisions, [docs/adr](docs/adr) for accepted architecture decisions, and the
[tasks directory](tasks/README.md) for the ordered backlog.

## Development workflow

Invoke the repository skill with `$deliver-next-task`. It presents eligible tasks, creates an isolated worktree, implements and verifies the selected task, opens a pull request, performs a senior Go review, and hands the PR to a human for final review and merge.

## Licence

Apache License 2.0. See [LICENSE](LICENSE).
