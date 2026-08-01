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

Use the repository task lifecycle skills:

- `$choose-task` recommends the next eligible task without changing the repository.
- `$start-task OSS-NNN` creates or resumes an isolated task worktree and opens it
  in a new VS Code window.
- `$implement-task OSS-NNN` implements and verifies the task from that worktree,
  performs a senior Go review, and opens a pull request.
- `$close-task OSS-NNN` verifies the human-reviewed merge and safely removes the
  worktree and merged branch.

Repository agents never merge pull requests. A human reviews and merges each PR
before `$close-task` performs cleanup.

## Licence

Apache License 2.0. See [LICENSE](LICENSE).
