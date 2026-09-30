---
type: gotcha
date: 2026-09-30
description: "Feature not visible" usually means a stale checkout or binary — build from origin/main
---
The user couldn't find the merged filter and /cluster features: the main checkout was on an old branch,
and `bin/atlas` / `~/go/bin/atlas` were built before the merge.
Fix: `git fetch && go build -o /tmp/atlas-main` from `origin/main` (or `go run ./cmd/atlas serve`),
run on a free port (`--addr localhost:8099`) and give direct URLs, e.g.
`/?repo=https://github.com/anthropics/skills&filter=doc`.
