#!/usr/bin/env bash
# Run the UI tests (test/ui, Playwright screenshots) in the Playwright Docker image,
# so the baselines render with the same browser and fonts locally and in CI.
# Usage: scripts/ui-test.sh [--update] [playwright test args]
#   --update  re-render the baselines in test/ui/__screenshots__ (review them before committing)
set -euo pipefail

image=mcr.microsoft.com/playwright:v1.63.0-noble # keep in sync with test/ui/package.json
args=()
if [ "${1:-}" = "--update" ]; then args+=(--update-snapshots=all); shift; fi

root=$(git rev-parse --show-toplevel)
# The native architecture of the Docker host: arm64 and amd64 render the same pixels.
arch=$(docker version --format '{{.Server.Arch}}')
GOOS=linux GOARCH="$arch" CGO_ENABLED=0 go build -C "$root" -o "bin/atlas-linux-$arch" ./cmd/atlas

docker run --rm --init --ipc=host --platform "linux/$arch" \
  -u "$(id -u):$(id -g)" -e HOME=/tmp -e CI="${CI:-}" \
  -e ATLAS_BIN="/work/bin/atlas-linux-$arch" \
  -v "$root:/work" -w /work/test/ui "$image" \
  sh -c 'npm ci --silent --no-audit --no-fund && npx playwright test "$@"' sh ${args[@]+"${args[@]}"} "$@"
