---
type: decision
date: 2026-09-30
description: CI (workflow "Tests", jobs test + integration) runs on push to any branch, no pull_request trigger
---
Agents work in branches and wait for CI via `scripts/ci-wait.sh` before opening a PR, so CI must run on
branch pushes. The `pull_request` trigger was removed to avoid two identical runs per commit; the push
run's status is shown in the PR anyway.

**Why:** one run per commit, and `ci-wait.sh` finds it by commit SHA.
**How to apply:** trade-off — PRs from forks are not checked; bring back `pull_request` if that matters.
`ci-wait.sh` looks up the workflow by the name `Tests` — don't rename it.
