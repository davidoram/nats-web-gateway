# ADR 0004: Standardize build and verification through Mage

- Status: Accepted
- Date: 2026-08-01
- Applies to: Architecture §§2, 8–12

## Context

The gateway needs one reproducible interface for building the system and running
its quality gates. Developers and CI must not maintain separate command sequences
that drift over time or produce different results. The interface must support
fast local iteration as well as the broader evidence required by the
architecture: formatting, static analysis, unit and integration tests, race
detection, coverage inspection, security checks, load or fuzz testing where
applicable, reproducible builds, and software bills of materials.

The project is implemented in Go but produces a custom Caddy binary containing
the gateway module. Some verification also requires real, pinned Caddy and NATS
processes. Raw `go` commands remain useful building blocks, but a growing list of
commands documented independently in CI, contributor documentation, and local
scripts would make it easy to omit a required check. Shell-specific orchestration
would also create avoidable differences between developer environments.

## Decision

The repository will use [Mage](https://magefile.org/) as its canonical build and
verification orchestrator. Mage and other Go-based development tools will be
versioned as Go tool dependencies and invoked with `go tool`, so contributors do
not need separately installed global tool versions.

The stable entry point is:

```text
go tool mage <target>
```

Mage targets orchestrate ordinary Go and external tools. They must not introduce
alternative compilation, test, or release semantics that exist only inside the
Mage implementation. Commands must be non-interactive, return a non-zero status
on failure, preserve useful diagnostic output, and work from the repository root.

The initial public target contract is:

| Target | Contract |
| --- | --- |
| `build` | Build the custom Caddy binary with the local gateway module and place it under `build/`. |
| `test` | Run the fast unit-test suite suitable for normal development. |
| `testRace` | Run all race-appropriate tests with the Go race detector. |
| `coverage` | Run tests with coverage and write machine-readable and HTML reports under `coverage/`. |
| `integration` | Run protocol-boundary tests against pinned real Caddy and NATS processes. |
| `lint` | Check formatting, generated-file consistency, `go vet`, and configured static analyzers. |
| `security` | Run the configured vulnerability and dependency checks. |
| `sbom` | Generate a software bill of materials for the build artifact. |
| `verify` | Run the checks required before opening or updating a pull request. |
| `ci` | Run the authoritative continuous-integration suite, including applicable integration and security checks. |
| `clean` | Remove only the repository's known generated build, coverage, and test artifacts. |

`verify` is the normal local quality gate. At minimum it includes `lint`, `test`,
`testRace`, and `coverage`. `ci` is the complete merge-gating entry point and may
add integration, security, compatibility, fuzz, or load targets as those suites
are introduced. Expensive suites remain independently callable so normal
development does not require running every release-level check after each edit.
The definition of `verify` or `ci` may only be weakened through an explicit,
reviewed change that explains the lost evidence.

CI configuration will call the same Mage targets used locally rather than
reimplementing their commands in workflow YAML. Contributor documentation may
show underlying commands for diagnosis, but it will direct normal build and test
work through the Mage targets.

The `build` target will use a pinned `xcaddy` tool to assemble Caddy with the
gateway module from the current checkout. Build inputs, the Go toolchain, tool
dependencies, and integration-service versions will be pinned in their native
configuration files. Builds will use deterministic paths and metadata,
including `-trimpath`, and release verification will demonstrate reproducibility
rather than merely asserting it. Exact versions are deliberately not recorded
in this ADR because they must be upgradable without changing the architectural
decision.

Generated artifacts will be confined to documented, ignored directories such as
`build/`, `coverage/`, and `dist/`. `clean` will enumerate those paths explicitly;
it must not delete arbitrary paths, follow user-provided recursive targets, or
remove dependency caches.

Tests must not depend on public internet services. Dependency and tool downloads
may occur during an explicit bootstrap or dependency-resolution phase, but test
behavior must use local fixtures or pinned local Caddy and NATS processes. The
same integration scenarios must be runnable locally and in CI. Test credentials
must be non-production fixtures, and Mage targets must not expose credentials,
tokens, sensitive payloads, or authenticated identity attributes in arguments,
logs, reports, or generated artifacts.

Pull requests will record the exact Mage targets run and their outcomes. Coverage
reports are evidence for review, not a percentage target to game; critical
security, failure, and concurrency paths still require direct assertions.

## Consequences

- Developers and CI have one discoverable command surface and execute the same
  build and verification logic.
- Tool versions are reviewable alongside module dependencies, and contributors
  do not need to maintain matching global installations.
- The project gains a small build-orchestration dependency and Magefile code that
  must itself remain simple and reviewable.
- Go tool dependencies participate in module version selection, so tool upgrades
  require the same compatibility review as other dependency changes.
- A first invocation may need to download the pinned tool and module graph;
  offline operation requires those dependencies to have been cached or supplied
  by the development environment.
- Integration and release targets may require a supported local container engine
  or explicitly provisioned binaries, but unit tests and static checks remain
  usable without running Caddy or NATS.
- Adding a mandatory quality gate requires changing the canonical Mage target
  first; CI-only mandatory checks are not permitted.

## Alternatives rejected

- **Make as the canonical interface:** widely available on Unix-like systems,
  but recipes commonly rely on platform-specific shell behavior and require a
  separate installation on some supported developer environments.
- **Bash and PowerShell scripts:** rejected because parallel implementations are
  likely to drift and make one platform a second-class verification path.
- **CI workflow YAML as the source of truth:** rejected because developers could
  not reliably reproduce the merge gate locally and CI behavior would diverge
  from documented commands.
- **Document raw tool commands only:** rejected because the required command set
  will grow and callers could easily execute an incomplete or incorrectly
  ordered subset.
- **A container-only build interface:** rejected as the sole mechanism because it
  adds latency and makes ordinary Go development and debugging less direct.
  Containers remain appropriate for pinned integration dependencies and release
  reproducibility checks.
- **A bespoke build CLI:** rejected because the project does not need to maintain
  a custom task-runner framework when a small Go-native orchestrator provides the
  required dependency graph and cross-platform execution.
