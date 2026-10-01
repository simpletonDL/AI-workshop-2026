---
type: decision
date: 2026-10-01
description: All clones are shallow + blobless + sparse (SKILL.md only); the GitHub Trees API idea was dropped
---
`gitClone` = `clone --depth 1 --filter=blob:none --no-checkout` → `sparse-checkout set --no-cone SKILL.md` →
`checkout`. Measured: microsoft/vscode 4 MB / 1.9 s instead of 396 MB / 11 s; same skills (integration tests,
symlinked dirs included). Servers without `uploadpack.allowFilter` (UI test git server, `file://`) print
"filtering not recognized by server" and send everything — still works.

**Why:** the Trees API + raw.githubusercontent design (built once, lost uncommitted) costs one REST call per
repository — an organization scan would burn the 60/h anonymous limit; git transfer has no such quota.
**How to apply:** don't claim atlas reads repos via the GitHub API — only organization *listing* uses it
([[decision-org-scan]]). Lesson kept: uncommitted work is lost when a session ends.
