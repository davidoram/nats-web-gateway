---
name: implement-task
description: Implement a selected OSS task inside its existing dedicated Git worktree, including planning, code, tests, comprehensive verification, senior Go self-review, commits, push, and a pull request to main. Use when the user asks to implement, complete, deliver, or publish a task from a task worktree, or invokes $implement-task. Do not create the worktree here; use start-task first.
---

# Implement a task

Deliver one task completely from its already-created worktree.

## Establish scope

1. Read `ARCHITECTURE.md` completely and treat it as binding.
2. Read `AGENTS.md`, `tasks/README.md`, every task file, the selected task, and
   relevant ADRs and documentation.
3. Confirm the current working directory is the selected task's registered
   worktree and the branch matches `task/<task-id>-<slug>`. Stop if running in
   the primary checkout, on `main`, or in a worktree for another task.
4. Inspect status and existing changes. Preserve user changes and stop on an
   unsafe overlap.
5. Translate the task into explicit acceptance criteria and map the work to
   applicable architecture sections, dependencies, failure modes, security
   boundaries, compatibility risks, and required verification.

## Implement

Make the smallest coherent change that fully achieves the task. Include all
necessary code, unit and integration tests, scripts, documentation, examples,
migrations, and operational guidance. Do not combine another backlog task.

Preserve cancellation, bounded resource use, explicit delivery semantics,
least privilege, error causes, credential isolation, safe logging, and stable
public behavior. Never weaken architecture, tests, or security to pass a check.

## Verify

Run repository-provided commands first. Where applicable, run and record:

- formatting and generated-file consistency checks;
- configured linters and `go vet ./...`;
- `go test ./...` and `go test -race ./...`;
- coverage collection with package/function inspection;
- integration tests for changed Caddy/NATS boundaries; and
- fuzz, load, security, compatibility, dependency, or SBOM checks required by
  the task and architecture.

Investigate every failure. Explicitly identify checks that are inapplicable or
blocked rather than claiming they passed.

## Review

Review the complete diff from the merge base as a senior Go engineer. Check the
task outcome, architecture compliance, APIs, cancellation, concurrency, cleanup,
reload/reconnect behavior, partial failure, security boundaries, tenant
isolation, injection, limits, redaction, observability cardinality, tests,
documentation, and operator impact. Fix actionable findings and rerun affected
verification until no blocking finding remains.

## Publish

1. Confirm the diff contains only the selected task and the worktree is clean
   after intentional commits whose messages include the task ID.
2. Push the branch without force.
3. Open a draft PR to `main` using `.github/pull_request_template.md`.
4. Put exact verification, coverage, architecture, security, delivery,
   migration, review, and residual-risk evidence in the PR body.
5. Add the PR link to the task file as completion evidence and move that file
   from `tasks/todo/` to `tasks/done/` on the task branch. Commit and push this
   closeout so the status move lands atomically when the implementation PR is
   merged. The default branch remains `todo` until then.
6. Mark the PR ready only when implementation, verification, review, and task
   closeout are complete.

Never merge or enable auto-merge. Give the user the PR link, a concise manual
review checklist, and ask them to review and merge it. Keep the branch and
worktree until `close-task` confirms the merge.
