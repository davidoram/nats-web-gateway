---
name: start-task
description: Start a selected repository task by validating eligibility, creating or resuming its dedicated task branch and sibling Git worktree, and opening that worktree in a new VS Code window. Use when the user asks to start, begin, pick up, or resume a named OSS task, or invokes $start-task. This skill prepares and hands off the worktree; implementation belongs to implement-task from that worktree.
---

# Start a task

Create the isolated workspace before inspecting implementation code or editing.

## Resolve and validate

1. Read `ARCHITECTURE.md` completely and treat it as binding.
2. Read `AGENTS.md`, `tasks/README.md`, and every task file.
3. Resolve the requested ID to exactly one file in `tasks/todo/`. If it is in
   `done/` or `cancelled/`, report that status and stop.
4. Read the entire selected task. Confirm all earlier required tasks and stated
   dependencies are in `tasks/done/`.
5. Inspect repository status, all worktrees, task branches, and open pull
   requests. Never duplicate or enter a dirty worktree owned by another session.

If no ID was supplied, hand off to `choose-task`; do not silently choose.

## Resume or create

Use branch `task/<lowercase-task-id>-<short-slug>` and a sibling directory whose
name is the repository name followed by `-<lowercase-task-id>-<short-slug>`.

- If the task already has a clean, inactive worktree, reuse it.
- If its worktree appears active, report the owner-conflict evidence and stop.
- Otherwise fetch the default branch, confirm its primary checkout is clean and
  current, then create the branch and sibling worktree from `origin/main` with an
  explicit validated path.

Never switch the primary checkout away from `main`, reuse an unrelated worktree,
or create the task branch from a stale feature branch.

## Open and hand off

Open the resolved worktree in a new VS Code window with `code -n <path>`. If GUI
launching is unavailable, give the user that exact command instead. State the
absolute worktree path, branch, task ID, and task outcome.

Stop after setup. Tell the user to continue in the new VS Code window with:

> Use `$implement-task OSS-NNN` in this worktree.

Do not implement, commit, push, or open a pull request from the setup session.
