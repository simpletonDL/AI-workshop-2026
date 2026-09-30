---
type: decision
date: 2026-09-30
description: PRs are merged one at a time with a linear history (rebase/squash); the next PR is rebased on the new main
---
Parallel feature PRs usually touch the same files (`serve.go`, `serve.html`, `spec/cli.md`).
Pattern that worked twice: merge the first PR, rebase the second onto the new `main`, fix conflicts,
wait for green CI on the rebased commit, merge exactly that commit.

**How to apply:** most conflicts were in `spec/cli.md` (both PRs rewrite the tests section) — merge
the descriptions instead of picking one side. Tell parallel agents to keep shared code changes minimal.
