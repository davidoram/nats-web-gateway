# Repository instructions for Codex

1. Read `ARCHITECTURE.md` completely before planning or changing anything. It is binding.
2. Read `tasks/README.md` and every task file under `tasks/`; preserve task identifiers, ordering, dependencies, status, and completion evidence.
3. Use the task lifecycle skills: `$choose-task` to recommend, `$start-task` to create and open a worktree, `$implement-task` to deliver from that worktree, and `$close-task` to verify a human merge and clean up.
4. Work in a dedicated Git worktree and task branch. Never implement directly on `main`.
5. Open the Git worktree / task branch in a new instance of VSCode so the user can see the changes you are making.
6. Keep each PR focused on one selected task unless the user explicitly approves a dependency change.
7. Apply Go formatting, static analysis, tests, race detection, and relevant security or load verification before opening a PR.
8. Do not weaken architecture, tests, security controls, or delivery semantics merely to make a check pass.
9. Never merge a PR. Present evidence and ask the user to review and merge manually.
