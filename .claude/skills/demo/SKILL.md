---
name: demo
description: Record a video/GIF demo of a web UI change in `atlas serve` (Playwright, headless) and attach it to the PR. Use when a code change is visible in the web UI, or when the user asks for a demo / screencast / video of a feature.
---

# Web UI demo
A demo is a **scenario script** replayed in headless Chromium against a freshly built `atlas serve`.
Never record by hand: the scenario is reviewable, re-runnable and doubles as an e2e check.

## Steps
1. Write `demo/scenarios/<name>.mjs` (`<name>` — kebab-case, the feature). Show the feature in 10–30 s, following the spec:
   ```js
   export default async (demo) => {
     await demo.goto('/');
     await demo.type('input[name=repo]', 'https://github.com/anthropics/skills');
     await demo.click('button[type=submit]');
     await demo.page.waitForSelector('.summary', { timeout: 60_000 });
     await demo.screenshot('results');
   };
   ```
   Helpers (`demo/record.mjs`):
   - `goto(path)`, `pause(ms)`, `scroll(dy)`, `page` (raw Playwright page for waits/asserts);
   - `click(sel)` — the cursor glides to the element, then clicks (headless video has no pointer, a dot is drawn);
   - `type(sel, text)` — **always use it for text input**: clicks the field and types key by key, never `fill`;
   - `screenshot(label)` — PNG next to the video.
   Wait for real states (`waitForSelector`), not fixed sleeps; add `pause` only so a viewer can read.
   Use small, stable public repos (see `memory/gotcha-integration-test-repos.md`); for `/cluster` pass a fake
   `--claude-bin` (as in the tests) so the demo doesn't depend on a real model.
2. Record locally: `scripts/demo-record.sh <name> [-- <atlas serve flags>]`. It builds `bin/atlas`, starts
   `serve` on a free port, writes `demo/out/<name>.{webm,gif}` and the PNGs (`demo/out/` is ignored by git).
   A failed step exits non-zero and leaves `<name>-error.png`.
3. **Check it:** `Read` the PNGs (`-final.png` and your labels). If they show an error, an empty page or the
   wrong state — fix the code or the scenario and record again. Never attach a demo you haven't looked at.
4. Publish: `scripts/demo-record.sh --publish <name>`. It pushes the GIF to the orphan branch `demo-assets`
   (`<branch>/<name>.gif`, the working tree and current branch are untouched) and prints a markdown image.
   Put that line into the PR body (`gh pr create --body` or, if the PR exists, `gh pr edit --body`).
5. Commit the scenario with the change. Update an existing scenario when the UI it shows changes.
