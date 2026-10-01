---
type: decision
date: 2026-10-01
description: Web UI demos = Playwright scenario recorded in the atlas-demo Docker image → GIF on orphan branch demo-assets, linked by commit URL
---
GitHub has no public API to upload attachments to a PR, so the GIF is committed to the orphan branch
`demo-assets` with plumbing (temp index + `commit-tree`, no checkout) and linked as
`raw.githubusercontent.com/<repo>/<commit>/<path>` — works because the repo is public; a commit URL is immutable.
GIF, not mp4: only images render inline from raw links. `progress.js` replaces the page with `document.write`,
which drops injected elements — the fake cursor is re-added before every move. CI never runs on `demo-assets`:
the orphan branch has no `.github/workflows`, and a push only triggers workflows present in the pushed commit.

Recording runs in Docker (`demo/Dockerfile`, 2026-10-01): on the host it needed Playwright's Chromium from
`cdn.playwright.dev` (blocked on some hosts) plus system libraries (no sudo), so demos silently never happened.
The image uses only Docker Hub + the Ubuntu archive; Chromium falls back to the same Chrome for Testing build on
`storage.googleapis.com` (no linux-arm64 builds there), Playwright's video ffmpeg to `ffmpeg-static`.
`atlas serve` runs inside the container too (no host networking, works on macOS). See [[gotcha-docker-network-via-proxy]].

**Why:** the user wants the agent to show features in PRs; text input must be typed smoothly (`demo.type`).
**How to apply:** follow `.claude/skills/demo`; the host needs only Docker.
