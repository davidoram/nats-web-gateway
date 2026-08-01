---
name: choose-task
description: Review the repository task board and recommend the next eligible task without starting work. Use when the user asks what to work on next, wants task options, needs backlog orientation, or invokes $choose-task. Do not use when the user has already selected a task and wants its worktree created; use start-task then.
---

# Choose a task

Recommend work from the task board without changing working files, branches, or
GitHub. Fetching remote references for current information is allowed.

## Gather

1. Read `ARCHITECTURE.md` completely and treat it as binding.
2. Read `AGENTS.md`, `tasks/README.md`, and every file under `tasks/`.
3. Inspect `git status --short --branch`, `git worktree list`, local and remote
   task branches, and open pull requests.
4. Fetch first when permitted so eligibility is based on current remote state.
   If fetching fails, state that the recommendation uses local information.

## Determine eligibility

Consider only files in `tasks/todo/`. Preserve task-ID order unless an explicitly
documented dependency permits an exception. Exclude a task when:

- an earlier required task is not in `tasks/done/`;
- a stated dependency is incomplete;
- its ID already appears in a live worktree, branch, or open pull request; or
- the task cannot comply with `ARCHITECTURE.md` without an architecture change.

Treat a dirty task worktree as occupied. Do not recommend it to another session.

## Recommend

Lead with one clear recommendation. Include its outcome, satisfied dependencies,
principal risks, applicable architecture sections, and likely verification. Add
at most two runners-up when useful. Identify in-flight or blocked work briefly.

If the user asked for options, stop after presenting them. Never create a branch,
worktree, commit, or pull request. End with the exact handoff:

> To begin it, ask me to use `$start-task OSS-NNN`.
