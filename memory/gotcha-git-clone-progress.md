---
type: gotcha
date: 2026-09-30
description: Clone progress comes from parsing `git clone --progress` stderr; the same stderr is the error message
---
Without a TTY git prints no progress unless `--progress` is passed (`--quiet` suppresses it). Updates end with
`\r`, not `\n`, and arrive in arbitrary chunks. `Updating files` / `Resolving deltas` are often skipped
entirely on small or fast clones (git delays those meters), so the bar may jump from ~80% of the clone to done.
Stderr also carries `Cloning into…` and `remote: Total…` chatter, filtered out of the error message
(`remote: Repository not found.` must stay).

Since the sparse clone ([[decision-sparse-blobless-clone]]) a clone is two commands: `clone` (receiving 0–50,
deltas 50–60, then 60 is reported) and `checkout --progress` (updating files 60–100). The lazy blob fetch inside
checkout prints no progress.
**How to apply:** the callback reaches `gitClone` via the context (`withCloneProgress`), not the
`checkoutFunc` signature — test stubs ignore it. Requests waiting on the cache for another request's clone
get no intermediate progress.
