---
name: deliver-next-task
description: Deliver one ordered repository task through selection, isolated Git worktree implementation, comprehensive verification, senior Go review, and a GitHub pull request. Use when the user asks to choose, start, implement, review, or ship the next task from TASKS.md in this repository.
---

# Deliver the next task

## Mandatory inputs

1. Read `ARCHITECTURE.md` completely and treat it as binding.
2. Read `AGENTS.md`, `TASKS.md`, the current branch, worktree list, and repository status.
3. Stop if architecture is absent, contradictory, or cannot support the requested task without amendment.

## Select

1. Identify incomplete tasks whose predecessors and stated dependencies are complete.
2. Present a short numbered choice with outcome, dependencies, principal risk, and likely verification.
3. Ask the user to select a task. Do not choose on the user's behalf unless they explicitly delegate the choice.
4. Restate the selected task and acceptance criteria before mutating Git or GitHub.

## Isolate and plan

1. Fetch the default branch and confirm it is clean and current.
2. Create a branch named `task/<lowercase-task-id>-<short-slug>` from the current default branch.
3. Create a sibling worktree with an explicit, validated path. Never reuse or delete an unrelated worktree.
4. Perform all task work inside that worktree.
5. Analyse relevant code, tests, ADRs, dependencies, failure modes, security boundaries, and compatibility constraints.
6. Write a concise implementation plan and map it to applicable architecture sections. Resolve material ambiguity with the user before implementation.

## Implement

1. Make the smallest coherent change that fully satisfies the task.
2. Include required code, tests, scripts, documentation, examples, migrations, and operational guidance.
3. Preserve cancellation, bounded resource use, error causes, least privilege, and safe logging.
4. Update `TASKS.md` in the same PR: mark only the selected task complete and include the PR URL once known.
5. Do not make unrelated cleanup changes.

## Verify

Run repository-provided commands first. At minimum, where applicable, run:

- formatting and generated-file consistency checks
- `go vet ./...` and configured linters
- `go test ./...`
- `go test -race ./...`
- coverage collection with package/function inspection, not only a percentage
- integration tests across changed Caddy/NATS boundaries
- fuzz, load, security, or compatibility tests required by the architecture and task

Investigate failures; never suppress or weaken a check without explicit architectural justification. Record exact commands and outcomes.

## Review as a senior Go engineer

Review the complete diff from the merge base. Check:

- architecture and task acceptance criteria
- correctness under cancellation, concurrency, reload, reconnect, partial failure, and cleanup
- goroutine, channel, timer, subscription, and connection ownership
- error wrapping, API design, configuration compatibility, and dependency choices
- authentication, authorization, claim trust, injection, limits, redaction, and tenant isolation
- meaningful tests for success, failure, boundary, race, and regression cases
- observability cardinality, privacy, documentation, and operator experience

Fix all actionable findings, rerun affected verification, and repeat review until no unresolved blocking finding remains. Report residual risks honestly.

## Publish

1. Inspect the final diff and repository status.
2. Commit intentionally with the task ID in the message.
3. Push the task branch.
4. Open a draft PR using `.github/pull_request_template.md`, then mark it ready only after all required checks and review pass.
5. Include task outcome, architecture mapping, security and delivery semantics, verification evidence, coverage impact, review findings, and residual risks.
6. Never merge, enable auto-merge, delete the branch, or remove the worktree before the user has reviewed the PR.

## Handoff

Give the user the PR link and a concise manual-review checklist. Explicitly ask them to inspect and merge the PR. After they confirm merge, verify the merged state before offering to remove the worktree and branch.
