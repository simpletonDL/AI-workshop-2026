---
type: decision
date: 2026-09-30
description: /cluster groups skills by calling `claude -p` (flag --claude-bin); tests use a fake script
---
Only names and descriptions go to the model; the answer is JSON, taken from the `result` field and
tolerant to markdown wrapping. Unknown ids are dropped, a skill in several clusters stays in the
first, unassigned skills go to "Other", max 10 repos per request.

**Why:** no API keys in the service; reuse the logged-in `claude` of the host.
**How to apply:** the machine running `serve` needs a working `claude`; CI never calls a real model —
keep the fake-binary approach for new tests.
