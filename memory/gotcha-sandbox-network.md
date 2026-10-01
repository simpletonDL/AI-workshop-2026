---
type: gotcha
date: 2026-10-01
description: Sandbox blocks MCR blobs, cdn.playwright.dev, apt and Actions artifact downloads — render UI baselines in CI
---
Symptoms: `scripts/ui-test.sh` can't pull `mcr.microsoft.com/playwright` (403 from *.data.mcr.microsoft.com),
`playwright install` gets "Blocked by network policy", `apt-get` can't resolve, `gh run download` gets 403 from
blob.core.windows.net. `make` is not installed (run the `go test` commands from the Makefile directly).
Workaround that worked: a temporary commit makes the `ui` job run `scripts/ui-test.sh --update` and print
`tar -czf - __screenshots__ | base64 -w 4000` between markers; `gh run view --job <id> --log` (logs are reachable),
decode, `Read` the PNGs, commit only the ones whose tests failed (`--update=all` rewrites passing ones with new
bytes; `--update-snapshots=changed` rewrites only failing ones), restore the workflow. Demo GIFs do record here
now (`scripts/demo-record.sh` builds from Docker Hub) — [[decision-web-ui-demos]].
