---
type: gotcha
date: 2026-10-01
description: On the cloud host containers reach the internet only via the host's HTTPS_PROXY; apt-get update "succeeds" without network
---
Symptom: `npm ci` in `docker build` fails with `EAI_AGAIN`; `docker run ... apt-get update` exits 0 although
nothing was fetched (it only warns), so a quick "network works" check lies — check with `getent hosts` or a real install.
Cause: no DNS/egress in containers; the host goes through `HTTPS_PROXY=http://172.17.0.1:13128` (docker0 gateway).
Workaround: pass the host's proxy variables (`--build-arg HTTPS_PROXY`, `-e HTTPS_PROXY`, ...) — `scripts/demo-record.sh`
does it. Docker Hub pulls work (the daemon has its own route); mcr.microsoft.com blobs are 403. No `make` on this host either.
