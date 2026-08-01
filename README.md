# NATS Web Gateway

An open-source Caddy module that exposes explicitly configured NATS services and streams to HTTP clients.

The project will initially provide:

- HTTP request/reply routes backed by NATS subjects
- NATS Service API (ADR-32) compatible behavior
- Server-Sent Events for bounded, policy-controlled subscriptions
- authentication and authorization delegated to NATS, including Auth Callout-backed OAuth deployments
- per-security-context NATS connections that preserve NATS account and subject permissions
- production-oriented limits, observability, cancellation, and graceful shutdown

The project is at an early implementation stage. See
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

## Build and verification

Go 1.26.5 or newer is required. Development tools are pinned in `go.mod` and run
without global installation through the canonical Mage interface:

```text
go tool mage build       # build/custom Caddy binary
go tool mage test        # fast unit tests
go tool mage testRace    # race-enabled tests
go tool mage coverage    # coverage/coverage.out and coverage.html
go tool mage integration # protocol-boundary suite
go tool mage lint        # formatting, module, vet, and static analysis
go tool mage security    # module integrity and vulnerability checks
go tool mage sbom        # dist/nats-web-gateway.cdx.json
go tool mage verify      # normal pre-PR gate
go tool mage ci          # authoritative merge gate
go tool mage clean       # remove only build/, coverage/, and dist/
```

The first invocation may download pinned Go modules and tools. Tests themselves
do not use public network services. OSS-003 will add the pinned local Caddy and
NATS processes used by the integration target; until then it runs all tests with
the reserved `integration` build tag.

The root package is the thin Caddy module boundary. Transport-independent route
enforcement, credential presentation, and HTTP/NATS translation live under
`internal/routes`, `internal/credentials`, and `internal/translation`
respectively so they remain testable without Caddy or network processes.

## Licence

Apache License 2.0. See [LICENSE](LICENSE).
