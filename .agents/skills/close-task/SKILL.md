---
name: close-task
description: Close a delivered OSS task after its pull request is merged by verifying completion evidence and default-branch state, then safely removing the clean task worktree and deleting merged branches. Use when the user asks to close, finish, clean up, or finalize a task or invokes $close-task. If the PR is still open, stop and ask the user to review and merge it manually; repository agents never merge PRs.
---

# Close a task

Treat a merged implementation PR as a hard precondition for cleanup.

## Resolve and verify

1. Read `ARCHITECTURE.md` completely and treat it as binding.
2. Read `AGENTS.md`, `tasks/README.md`, every task file, and the selected task.
3. Resolve the task's branch, registered worktree, and pull request. Verify the
   PR targets `main` and that its head matches the task branch.
4. Inspect PR state and required checks.

If the PR is open, do not merge it, enable auto-merge, move files, remove the
worktree, or delete branches. Give the user the PR link and ask them to review
and merge manually, then stop.

If the PR is closed without merge, preserve the worktree and branch and report
the condition. If no PR exists, hand off to `implement-task`.

## Confirm merged state

After the PR reports merged:

1. Fetch and fast-forward the primary `main` checkout without discarding local
   changes.
2. Confirm the selected file exists in `tasks/done/`, no same-ID file remains in
   `tasks/todo/` or `tasks/cancelled/`, and completion evidence links the merged
   PR. If not, stop and report the inconsistent closeout; do not invent evidence.
3. Confirm the task branch is fully merged into `origin/main`.
4. Confirm the task worktree is clean and contains no untracked or unpushed work.

## Tear down

Operate from outside the task worktree. Remove it with `git worktree remove`
without `--force`, delete the merged local branch with `git branch -d`, and prune
stale worktree records. Delete the remote task branch only when the user has
explicitly requested branch cleanup; never force-delete it.

Report the task ID, merged PR, verified `done` file, removed worktree, and branch
cleanup. Never claim closure when any verification failed.
