---
type: decision
date: 2026-09-30
description: UI tests = Playwright screenshots in the Playwright Docker image; local git server, held clones, fake clock for the dancer
---
`test/ui/` compares screenshots at key moments with baselines. What made them deterministic:
- repositories come from `test/ui/fixtures/repos`, served by `git http-backend` behind a Node server on the
  fixed port 47123 (URLs are on the screenshots; serve rejects `file://`, and dumb HTTP can't do `--depth 1`);
- `git.hold(name)` delays a repository's requests, so the progress bar is caught at `Cloning …` 0%;
- the dancer: `reducedMotion: 'reduce'` everywhere except `dancer.spec.mjs`, which seeds `Math.random`
  (init script) and uses `page.clock.install` + `pauseAt` before `goto`, then `runFor` to each moment;
- rendering: the host (macOS) renders fonts differently, so tests only run in Docker; arm64 and amd64
  images gave pixel-identical screenshots, so the script uses the native architecture.

- failures: `failures-reporter.mjs` → `out/failures.json` → `scripts/ui-report.sh` (CI only) pushes the PNGs
  to `demo-assets` (`scripts/publish-assets.sh`, the ui job has `contents: write`) and links them in the log,
  annotations and job summary. The `<name>-expected.png` attachment points at the baseline in `__screenshots__`,
  so match attachments by name, not by file name. The dancer loop uses `expect.soft` to report every changed frame.

**How to apply:** an intended UI change → `scripts/ui-test.sh --update`, look at the PNGs, commit them.
Keep `@playwright/test` in `test/ui/package.json` and the image tag in `scripts/ui-test.sh` on the same version.
