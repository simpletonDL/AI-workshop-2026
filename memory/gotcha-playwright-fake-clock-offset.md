---
type: gotcha
date: 2026-10-01
description: Under page.clock install+pauseAt, performance.now() starts a random few ms off while rAF frames sit on a fixed 16 ms grid
---
Symptom: a dancer frame (`t14750`) failed now and then by the same 126 px; a fresh `--update` didn't help.
Cause: Playwright replays `install` → `pauseAt` in the page and advances `performance.now()` by the real ms
between the two calls; `pauseAt` sets only the wall time. rAF callbacks get timestamps on a 16 ms grid
(`delay = 16 - ticks % 16`). Anything timed from `performance.now()` at load (the first mode's deadline)
sometimes switched one frame later. Subtracting the start time is worse: then every frame shifts.
Fix: take all animation times from the rAF timestamps (`dancer.js` sets the first deadline on the first frame).
Check a determinism fix with `scripts/ui-test.sh --repeat-each=8` in CI — 1 failure in ~40 runs is easy to miss.
See [[decision-ui-screenshot-tests]].
