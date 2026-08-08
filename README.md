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

Declared JSON and Caddyfile route schemas, mapping options, limits, and
fail-closed validation rules are documented in
[docs/configuration.md](docs/configuration.md).

The gateway provisions an instance-owned, least-privilege NATS connection,
tracks reconnect readiness, and drains deterministically across overlapping
Caddy reloads. Configure credentials through Caddy placeholders backed by a
secret source; never embed production credentials in checked-in configuration.

## Request/reply example

The [Go orders service](examples/orders-service/main.go) demonstrates a small
API behind declared request/reply routes: a path value and a query value build
a validated subject, JSON request bodies are bounded, selected HTTP headers are
forwarded to NATS, and selected reply headers and bounded payloads return to the
HTTP caller. The matching Caddyfile and JSON configurations, plus curl examples,
are in [the configuration guide](docs/configuration.md#requestreply-example).

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

The first invocation may download pinned Go modules, tools, and container images.
Tests themselves do not use public network services. `go tool mage integration`
builds Linux binaries for the host architecture, starts the pinned local Caddy,
NATS, and ADR-32 example-service containers from `compose.yml`, waits for the
real protocol boundaries, runs the integration-tagged tests, and removes the
environment. Docker Compose or Podman Compose is required.

### Local integration environment

The environment binds only to loopback:

| Component | Address | Purpose |
| --- | --- | --- |
| Caddy | `http://127.0.0.1:18080/health` | Loads the local gateway module and exercises its handler chain. |
| NATS client | `nats://127.0.0.1:14222` | Pinned NATS server used for request/reply tests. |
| NATS monitoring | `http://127.0.0.1:18222/healthz` | NATS readiness endpoint. |
| ADR-32 example | `demo.echo` | Echoes payloads and returns ADR-32 error code `4001` for payload `error`. |

For interactive development, first build the integration binaries with
`go tool mage integration` or `go tool mage ci`, then use the same checked-in
orchestration directly:

```text
podman-compose --file compose.yml up --detach
podman-compose --file compose.yml down --volumes --remove-orphans
```

Docker users can replace `podman-compose` with `docker compose`.

The credentials in `integration/local/nats-server.conf` are deliberately weak,
checked-in fixtures. They are restricted to the example subjects, must never be
reused outside this loopback-only environment, and must not be treated as a
deployment configuration. The gateway fixture can publish `demo.echo` and
ADR-32 discovery requests and can subscribe only to its NATS inbox. The example
service can consume those requests and publish only their replies.

The root package is the thin Caddy module boundary. Transport-independent route
enforcement, credential presentation, and HTTP/NATS translation live under
`internal/routes`, `internal/credentials`, and `internal/translation`
respectively so they remain testable without Caddy or network processes.

## Licence

Apache License 2.0. See [LICENSE](LICENSE).
