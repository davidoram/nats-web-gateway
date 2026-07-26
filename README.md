# NATS Web Gateway

An open-source Caddy module that exposes explicitly configured NATS services and streams to HTTP clients.

The project will initially provide:

- HTTP request/reply routes backed by NATS subjects
- NATS Service API (ADR-32) compatible behavior
- Server-Sent Events for bounded, policy-controlled subscriptions
- OIDC authentication at the HTTP boundary
- trusted, tamper-resistant end-user identity context for downstream services
- production-oriented limits, observability, cancellation, and graceful shutdown

The project is at the architecture and implementation-planning stage. See [ARCHITECTURE.md](ARCHITECTURE.md) for binding design rules and [TASKS.md](TASKS.md) for the ordered backlog.

## Development workflow

Invoke the repository skill with `$deliver-next-task`. It presents eligible tasks, creates an isolated worktree, implements and verifies the selected task, opens a pull request, performs a senior Go review, and hands the PR to a human for final review and merge.

## Licence

Apache License 2.0. See [LICENSE](LICENSE).
