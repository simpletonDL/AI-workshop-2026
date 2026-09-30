#!/usr/bin/env bash
# Record a web UI demo: build atlas, start `atlas serve`, run demo/scenarios/<scenario>.mjs
# in headless Chromium, write demo/out/<scenario>.{webm,gif,*.png}.
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

if [ ! -d "$demo/node_modules" ]; then
  npm ci --prefix "$demo" --silent
fi
(cd "$demo" && npx --no-install playwright install chromium >/dev/null)

make -C "$root" build >/dev/null

tmp=$(mktemp -d)
log="$tmp/serve.log"
"$root/bin/atlas" serve --addr 127.0.0.1:0 --cache-dir "$tmp" "$@" >"$log" 2>&1 &
pid=$!
trap 'kill $pid 2>/dev/null || true; wait $pid 2>/dev/null || true; rm -rf "$tmp"' EXIT

url=""
for _ in $(seq 1 50); do
  url=$(sed -n 's/^Serving atlas on \(http[^ ]*\).*/\1/p' "$log")
  [ -n "$url" ] && break
  kill -0 "$pid" 2>/dev/null || { cat "$log" >&2; exit 1; }
  sleep 0.1
done
[ -n "$url" ] || { echo "atlas serve did not start" >&2; cat "$log" >&2; exit 1; }

node "$demo/record.mjs" "$scenario" "$url" "$out"

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
