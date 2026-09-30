# Roles
Work is split between an **orchestrator** and **task agents**. Determine your role first:

- `.git` in the working directory is a **directory** (the main repo root) — you are the **orchestrator**.
- `.git` is a **file** (a git worktree) — you are a **task agent**.

# Orchestrator
The orchestrator only delegates. It **does not do tasks itself**: no code or spec edits, no commits, no running tests, no PRs.

1. Split the user's request into independent tasks. Each task gets a short kebab-case name — it becomes the branch, worktree and sandbox name.
2. For each task, create a worktree and a sandbox and start a task agent there:
   ```
   scripts/sbx-claude.sh <name> "<task description>"
   ```
   The script creates the worktree `../<name>` on branch `<name>` from `origin/main` and runs Claude Code in a Docker sandbox on it with the given task. Run it with `run_in_background` so several agents can work in parallel.
   - The task description must be self-contained: the agent does not see this conversation. Include the goal, relevant context and acceptance criteria.
3. Wait for the agents to finish, then check each result: `gh pr list --head <name>` and the PR's CI status.
4. Report to the user: for each task — the branch, the PR URL and the CI result. If an agent failed, say so and why; don't fix it yourself — start a follow-up task agent instead (same `<name>` reuses the existing worktree).

Do not remove worktrees or sandboxes unless the user asks.

# Task agent
A task agent works on exactly one task, in its own worktree and branch.

## Core rule
1. **Read the spec first.**
2. **Always check the tests.** Run the tests after making changes. If the tests are red, the task is **not considered done**.
3. **Every task ends with a PR.** A task is done only when CI is green for the pushed commit and the PR is open.

## Git workflow
1. The worktree is already on the task branch, created from `origin/main`. Stay on it: never switch branches and never commit to `main`.
2. Do the work. Commit to the branch as you go.

## CI workflow
After the local tests (`make test`, `make test-integration`) pass:

1. Commit the changes and push the branch: `git push -u origin HEAD` (you are allowed to do this without asking).
2. Wait for CI: run `scripts/ci-wait.sh` (Bash timeout 600000 ms, or `run_in_background` and wait for the notification).
   - It finds the GitHub Actions `Tests` run for the current `HEAD` commit and waits for it to finish.
   - Exit code 0 — CI is green. Exit code 1 — CI failed (failed job logs are printed), HEAD is not pushed, or no run was found.
3. If CI is red: read the logs, fix, commit, push, and run `scripts/ci-wait.sh` again.
4. Once `scripts/ci-wait.sh` prints `CI GREEN`, open a PR into `main` (you are allowed to do this without asking):
   ```
   gh pr create --base main --title "<title>" --body "<summary of changes>"
   ```
   If a PR for the branch already exists, just push — don't create a second one.
5. Only report the task as done after CI is green and the PR is open. In the final message, mention the commit SHA, the CI result and the PR URL.

Do not poll CI manually with repeated `gh run list` calls — use the script.
