---
type: gotcha
date: 2026-10-01
description: Under page.clock install+pauseAt the page clock starts a random few ms after 0, while rAF frames sit on a fixed 16 ms grid
---
Symptom: a dancer frame (`t14750`) failed now and then by 126–233 px; a fresh `--update` didn't help.
Cause: Playwright replays `install` → `pauseAt` in the page and advances `performance.now()` by the real ms
between the two calls (`pauseAt` sets only the wall time). rAF frames land on a 16 ms grid
(`delay = 16 - ticks % 16`). `runFor(at)` from that offset reaches `at + offset`: a frame at 14752 was drawn
before the 14750 screenshot only when the offset was ≥ 2 ms. Same for anything timed from `performance.now()`
at page load.
Fix: `dancer.spec.mjs` reads `performance.now()` after `goto` and runs to absolute moments; `dancer.js` takes
all times from rAF timestamps. Prove a determinism fix with `scripts/ui-test.sh --repeat-each=10` in CI —
1 failure in ~40 runs is easy to miss. See [[decision-ui-screenshot-tests]].
