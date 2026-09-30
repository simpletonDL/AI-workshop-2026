---
type: preference
date: 2026-09-30
description: Default is doing the task yourself; sandbox orchestration only when asked; research never runs the pipeline
---
The orchestrator used to be the default role (from `.git` being a directory). It got in the way: even
"merge the PRs" or "show me how the UI looks" was delegated to sandboxes, and the user had to say
"не делегируй никому, сделай сам". It was moved into the `orchestrate` skill, and `CLAUDE.md` now says
research tasks don't run the pipeline at all.

**Why:** delegation is slow and heavy for small or interactive tasks.
**How to apply:** do the work yourself; use `orchestrate` only on an explicit request. Related: [[decision-headless-report]].
