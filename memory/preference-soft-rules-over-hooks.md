---
type: preference
date: 2026-09-30
description: Enforce the workflow with CLAUDE.md rules + scripts, not hard hooks
---
When designing "agent waits for CI before saying done", three levels were offered (CLAUDE.md rule,
`scripts/ci-wait.sh`, a Stop hook). The user chose the rule + script, explicitly "без жесткого хука".

**Why:** hooks make the workflow rigid; a deterministic script plus a clear rule is enough.
**How to apply:** prefer a script + a rule in `CLAUDE.md`; propose hooks only if asked.
