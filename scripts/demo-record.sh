#!/usr/bin/env bash
# Record a web UI demo: build atlas and the demo image (demo/Dockerfile), run `atlas serve` and
# demo/scenarios/<scenario>.mjs in headless Chromium inside it, write demo/out/<scenario>.{webm,gif,*.png}.
# Only Docker is needed on the host; proxy variables of the host are passed through.
# Serve flags run inside the container with the repository root as working directory (read-only).
# With --publish, push the GIF to the orphan branch "demo-assets" and print a markdown image for the PR body.
# Usage: scripts/demo-record.sh [--publish] <scenario> [-- <extra atlas serve flags>]
set -euo pipefail

publish=false
if [ "${1:-}" = "--publish" ]; then publish=true; shift; fi
scenario="${1:?usage: scripts/demo-record.sh [--publish] <scenario> [-- <serve flags>]}"
shift
[ "${1:-}" = "--" ] && shift

root=$(git rev-parse --show-toplevel)
demo="$root/demo"
out="$demo/out"
[ -f "$demo/scenarios/$scenario.mjs" ] || { echo "no scenario: demo/scenarios/$scenario.mjs" >&2; exit 1; }

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

proxy_build=() proxy_run=()
for v in HTTP_PROXY HTTPS_PROXY NO_PROXY http_proxy https_proxy no_proxy; do
  [ -n "${!v:-}" ] && proxy_build+=(--build-arg "$v") && proxy_run+=(-e "$v")
done
docker build -q -t atlas-demo ${proxy_build[@]+"${proxy_build[@]}"} "$demo" >"$tmp/build.log" 2>&1 ||
  { cat "$tmp/build.log" >&2; exit 1; }

arch=$(docker version --format '{{.Server.Arch}}')
GOOS=linux GOARCH="$arch" CGO_ENABLED=0 go build -C "$root" -o "bin/atlas-linux-$arch" ./cmd/atlas

# record.mjs runs from /opt/demo to use the image's node_modules, not the host's.
mkdir -p "$out"
docker run --rm --init --ipc=host -u "$(id -u):$(id -g)" -e HOME=/tmp ${proxy_run[@]+"${proxy_run[@]}"} \
  -v "$root:/work:ro" -w /work -v "$out:/out" \
  -v "$root/bin/atlas-linux-$arch:/usr/local/bin/atlas:ro" \
  -v "$demo/run.sh:/opt/demo/run.sh:ro" -v "$demo/record.mjs:/opt/demo/record.mjs:ro" \
  -v "$demo/scenarios:/opt/demo/scenarios:ro" \
  atlas-demo /opt/demo/run.sh "$scenario" "$@"

$publish || exit 0

# Commit the GIF to demo-assets without touching the working tree or the current branch.
branch=$(git rev-parse --abbrev-ref HEAD)
path="$branch/$scenario.gif"
repo=$(gh repo view --json nameWithOwner -q .nameWithOwner)
blob=$(git hash-object -w "$out/$scenario.gif")
for attempt in 1 2 3; do
  git fetch -q origin demo-assets 2>/dev/null || true
  parent=$(git rev-parse -q --verify refs/remotes/origin/demo-assets || true)
  export GIT_INDEX_FILE="$tmp/index"
  rm -f "$GIT_INDEX_FILE"
  if [ -n "$parent" ]; then git read-tree "$parent"; else git read-tree --empty; fi
  git update-index --add --cacheinfo "100644,$blob,$path"
  tree=$(git write-tree)
  unset GIT_INDEX_FILE
  commit=$(git commit-tree "$tree" ${parent:+-p "$parent"} -m "demo: $path")
  if git push -q origin "$commit:refs/heads/demo-assets" 2>"$tmp/push.log"; then
    # A commit URL never changes, so the PR shows exactly this recording.
    echo "![$scenario demo](https://raw.githubusercontent.com/$repo/$commit/$path)"
    exit 0
  fi
  cat "$tmp/push.log" >&2; echo "push to demo-assets failed (attempt $attempt), retrying" >&2
done
exit 1
