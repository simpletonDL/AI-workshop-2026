---
type: decision
date: 2026-09-30
description: Web UI demos = Playwright scenario → GIF pushed to orphan branch demo-assets, linked by commit URL in the PR
---
GitHub has no public API to upload attachments to a PR, so the GIF is committed to the orphan branch
`demo-assets` with plumbing (temp index + `commit-tree`, no checkout) and linked as
`raw.githubusercontent.com/<repo>/<commit>/<path>` — works because the repo is public; a commit URL is immutable.
GIF, not mp4: only images render inline from raw links. ffmpeg comes from npm `ffmpeg-static`, so nothing
has to be installed system-wide. `progress.js` replaces the page with `document.write`, which drops injected
elements — the fake cursor is re-added before every move.

**Why:** the user wants the agent to show features in PRs; text input must be typed smoothly (`demo.type`).
**How to apply:** follow `.claude/skills/demo`; sandboxes need Chromium deps (`npx playwright install --with-deps chromium`).
