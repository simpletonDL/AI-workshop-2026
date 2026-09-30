---
type: preference
date: 2026-09-30
description: Chat in Russian; specs, CLAUDE.md, skills, code comments in English and laconic
---
The user writes in Russian and expects answers in Russian. Repository docs (`spec/cli.md`, `CLAUDE.md`,
skills, commit messages) are in English. Every time a doc came out long or in Russian the user asked
"на английском" / "лаконично".

**Why:** docs are read by agents and people alike; short docs are followed better.
**How to apply:** write docs in English from the start, minimal, no exhaustive lists; don't add
features/flags that weren't asked for without saying so explicitly.
