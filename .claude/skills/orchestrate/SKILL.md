---
name: orchestrate
description: Split a request into independent code-change tasks and run each one in parallel by a headless Claude task agent in its own git worktree and Docker sandbox (scripts/sbx-claude.sh), then collect branches, PRs and CI results. Use only when the user explicitly asks to orchestrate / run tasks in sandboxes / in parallel. Not for research or questions without code changes.
---

# Orchestrate
In this mode you are the **orchestrator**: you only delegate. You **do not do code-change tasks yourself**:
no code or spec edits, no commits, no running tests, no PRs. Run from the main repo root (`.git` is a directory).

## Only code changes go to task agents
Sandboxes, worktrees, CI and PRs are the pipeline for **code changes** (see *Task workflow* in `CLAUDE.md`).

- A task that is **research only** (questions, analysis, reading code/logs/issues — nothing in the repository changes)
  is **not** delegated: don't create a worktree or sandbox and don't start an agent for it. Answer it yourself right here.
- If the whole request is research, don't run the pipeline at all — just answer.
- If a request mixes both, answer the research part yourself and delegate only the code changes.

## Steps
1. Split the code-change part of the request into independent tasks. Each task gets a short kebab-case name — it becomes the branch, worktree and sandbox name.
2. For each task, create a worktree and a sandbox and start a task agent there:
   ```
   scripts/sbx-claude.sh <name> "<task description>"
   ```
   The script creates the worktree `../<name>` on branch `<name>` from `origin/main` (or reuses it), creates the sandbox `<name>` (or reuses it) and runs Claude Code headless (`claude -p`) in it on the task. Run it with `run_in_background` — a task takes longer than the Bash timeout — so several agents work in parallel.
   - The task description must be self-contained: the agent does not see this conversation. Include the goal, relevant context and acceptance criteria.
   - The agent follows the pipeline from `CLAUDE.md` (spec → tests → push → CI → PR) and ends with the final report described there.
3. **Getting the result.** When the agent finishes, the background command completes and its output is the agent's final report. The same report is saved to `logs/<name>.log`. Read the `STATUS` line — the script's exit code only says whether Claude ran, not whether the task succeeded.
4. Verify the report instead of trusting it: `gh pr view <name> --json url,state,statusCheckRollup`.
5. Report to the user: for each task — the branch, the PR URL and the CI result. If an agent failed, say so and why; don't fix it yourself — start a follow-up task agent instead (the same `<name>` reuses the worktree and sandbox; the new task must say what was already done and what went wrong).

Do not remove worktrees or sandboxes unless the user asks.
