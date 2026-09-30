---
type: decision
date: 2026-09-30
description: Sandboxed task agents run `claude -p`; the final message (STATUS/BRANCH/...) is the only channel back
---
`scripts/sbx-claude.sh <name> "<task>"` runs a headless agent; its output goes to the orchestrator's
background-task notification and to `logs/<name>.log`. The script's exit code only says Claude ran;
success is the `STATUS:` line. The orchestrator doesn't trust the report and checks `gh pr view <name>`.
Nobody can answer the agent's questions, so it decides itself and lists decisions in `SUMMARY`.

**How to apply:** task descriptions must be self-contained. See [[gotcha-sbx-no-tty]], [[gotcha-parallel-worktree-lock]].
