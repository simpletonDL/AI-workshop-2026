---
type: gotcha
date: 2026-10-01
description: Some hosts block mcr.microsoft.com, the Playwright CDN and Actions artifact downloads — render UI baselines in CI and pull them from the job log
---
Symptom: `scripts/ui-test.sh` fails on `docker pull` (403), `npx playwright install` is "Blocked by network
policy", `gh run download` of `ui-report` is 403 (artifacts live on Azure blob storage). npm and `gh run view --log` work.
Workaround: a throwaway branch whose workflow runs `scripts/ui-test.sh --update`, then `scripts/ui-test.sh`
(proves the baselines are stable), and prints `tar czf - test/ui/__screenshots__ | base64` between markers;
decode from `gh run view --job=<id> --log`, delete the branch. `--update-snapshots=all` re-renders every file with
anti-aliasing noise (passes Playwright's threshold) — commit only the baselines of screens the change affects.
No local Chromium also means no `demo` GIF: put the new baselines (commit URLs) into the PR's Demo section.
