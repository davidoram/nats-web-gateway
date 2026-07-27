# Repository instructions for Codex

1. Read `ARCHITECTURE.md` completely before planning or changing anything. It is binding.
2. Read `TASKS.md` and preserve task identifiers, ordering, dependencies, and completion evidence.
3. Use `$deliver-next-task` when asked to select, implement, review, or deliver a backlog task.
4. Work in a dedicated Git worktree and task branch. Never implement directly on `main`.
5. Open the Git worktree / task branch in a new instance of VSCode so the user can see the changes you are making.
6. Keep each PR focused on one selected task unless the user explicitly approves a dependency change.
7. Apply Go formatting, static analysis, tests, race detection, and relevant security or load verification before opening a PR.
8. Do not weaken architecture, tests, security controls, or delivery semantics merely to make a check pass.
9. Never merge a PR. Present evidence and ask the user to review and merge manually.
