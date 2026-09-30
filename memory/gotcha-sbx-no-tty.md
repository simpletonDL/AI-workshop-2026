---
type: gotcha
date: 2026-09-30
description: `sbx run` without a TTY loses output and refuses non-interactive runs — use `run --detached` + `sbx exec`
---
Symptom: a headless `sbx run ... claude -p` from a background Bash printed nothing; a second run failed
because the sandbox already existed. With a TTY the answer came back.
Workaround (in `scripts/sbx-claude.sh`): create the sandbox once with `sbx run --detached`, then
`sbx exec <name> claude -p "<task>" </dev/null` — exec passes stdout through and starts a stopped sandbox.
Arguments after `--` in `sbx run` go to the agent.
