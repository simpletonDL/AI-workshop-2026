#!/usr/bin/env bash
# Air cloud environment startup for atlas (Go CLI + `atlas serve` web UI).
# WARMUP (snapshot-baking) run: primes Go/npm/Docker caches, runs the test suites, waits for readiness.
# TASK run: refreshes deps, builds, starts `atlas serve` on :8080 in the background and exits promptly.
set -euo pipefail

if [ "${AIR_STARTUP_MODE:-}" = warmup ]; then WARMUP=1; else WARMUP=; fi

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$root"

PLAYWRIGHT_IMAGE=mcr.microsoft.com/playwright:v1.63.0-noble # keep in sync with scripts/ui-test.sh
SERVE_ADDR=0.0.0.0:8080
SERVE_LOG=/tmp/atlas-serve.log

log() { echo "[startup $(date +%H:%M:%S)] $*"; }

# make is not in the base image and sudo is unavailable: unpack the Ubuntu package into ~/.local.
export PATH="$HOME/.local/bin:$PATH"
if ! command -v make >/dev/null 2>&1; then
  log "installing make into ~/.local"
  tmp=$(mktemp -d)
  (cd "$tmp" && apt-get download make)
  mkdir -p "$HOME/.local/opt/make" "$HOME/.local/bin"
  dpkg-deb -x "$tmp"/make_*.deb "$HOME/.local/opt/make"
  ln -sf "$HOME/.local/opt/make/usr/bin/make" "$HOME/.local/bin/make"
  rm -rf "$tmp"
fi
log "$(make --version | head -1)"

# Put ~/.local/bin (make) and the Go bin directory (go install ./cmd/atlas) on PATH for agent shells.
env_file="$HOME/.atlas-env.sh"
cat >"$env_file" <<EOF
export PATH="\$HOME/.local/bin:\$PATH:$(go env GOPATH)/bin"
EOF
hook="[ -f \"$env_file\" ] && . \"$env_file\" # atlas-env"
profile=
for f in "$HOME/.bash_profile" "$HOME/.bash_login" "$HOME/.profile"; do
  if [ -f "$f" ]; then profile=$f; break; fi
done
[ -n "$profile" ] || { profile="$HOME/.profile"; touch "$profile"; }
for f in "$profile" "$HOME/.bashrc"; do
  grep -qF '# atlas-env' "$f" 2>/dev/null || echo "$hook" >>"$f"
done

log "go $(go version | awk '{print $3}'): downloading modules"
go mod download
log "building atlas"
make build
go install ./cmd/atlas
log "atlas installed to $(go env GOPATH)/bin/atlas"

log "starting atlas serve on $SERVE_ADDR (log: $SERVE_LOG)"
pkill -f "bin/atlas serve --addr $SERVE_ADDR" 2>/dev/null || true
nohup "$root/bin/atlas" serve --addr "$SERVE_ADDR" >"$SERVE_LOG" 2>&1 &

# Containers (scripts/ui-test.sh runs `npm ci` in one) reach the network only through the environment's
# proxy: let the Docker client pass it into every container.
if [ -n "${HTTPS_PROXY:-${https_proxy:-}}" ]; then
  log "configuring the Docker client proxy (~/.docker/config.json)"
  mkdir -p "$HOME/.docker"
  python3 - "$HOME/.docker/config.json" <<'EOF'
import json, os, sys
path = sys.argv[1]
cfg = json.load(open(path)) if os.path.exists(path) else {}
env = lambda *keys: next((os.environ[k] for k in keys if os.environ.get(k)), "")
cfg.setdefault("proxies", {})["default"] = {
    "httpProxy": env("HTTP_PROXY", "http_proxy"),
    "httpsProxy": env("HTTPS_PROXY", "https_proxy"),
    "noProxy": env("NO_PROXY", "no_proxy") or "localhost,127.0.0.1,::1",
}
json.dump(cfg, open(path, "w"), indent=2)
EOF
fi

wait_docker() {
  local n=0
  until docker info >/dev/null 2>&1; do
    n=$((n + 1)); [ $((n % 10)) -eq 1 ] && log "waiting for the Docker daemon..."
    sleep 2
  done
}

healthcheck() {
  log "healthcheck: waiting for atlas serve on :8080"
  local n=0
  # The exposed-port proxy forwards its own public hostname as Host.
  until body=$(curl -fsS -H "Host: example.com" http://127.0.0.1:8080/ 2>/dev/null) && [[ "$body" == *"<html"* ]]; do
    if ! pgrep -f "bin/atlas serve --addr $SERVE_ADDR" >/dev/null; then
      log "healthcheck: atlas serve is not running"; cat "$SERVE_LOG" >&2; return 1
    fi
    n=$((n + 1)); [ $((n % 10)) -eq 1 ] && log "healthcheck: atlas serve not answering yet"
    sleep 1
  done
  log "healthcheck: atlas serve answers"
  wait_docker
  docker image inspect "$PLAYWRIGHT_IMAGE" >/dev/null || { log "healthcheck: $PLAYWRIGHT_IMAGE missing"; return 1; }
  log "healthcheck: OK"
}

if [ -n "$WARMUP" ]; then
  log "warmup: npm ci for demo/ and Playwright Chromium (demo recordings)"
  npm ci --prefix demo --no-audit --no-fund
  (cd demo && npx --no-install playwright install chromium)

  wait_docker
  log "warmup: pulling $PLAYWRIGHT_IMAGE"
  docker pull "$PLAYWRIGHT_IMAGE"

  log "warmup: unit tests"
  go test ./...
  # Integration tests (make test-integration) hit live third-party GitHub repos; not run here so upstream
  # changes cannot break startup.
  log "warmup: UI tests (primes test/ui/node_modules)"
  scripts/ui-test.sh

  healthcheck
fi
log "done"
