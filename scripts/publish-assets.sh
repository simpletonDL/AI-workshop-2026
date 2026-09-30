#!/usr/bin/env bash
# Commit files to the orphan branch "demo-assets" as <dir>/<file> in one commit, without touching the working tree
# or the current branch, and print a raw URL for each file, in order. The URLs name the commit, so they never change.
# Usage: scripts/publish-assets.sh <dir> <file>...   (files relative to the current directory)
set -euo pipefail

dir="${1:?usage: scripts/publish-assets.sh <dir> <file>...}"
shift
[ $# -gt 0 ] || { echo "no files to publish" >&2; exit 1; }

repo=${GITHUB_REPOSITORY:-$(gh repo view --json nameWithOwner -q .nameWithOwner)}
# CI has no git identity; commit-tree takes it from the environment.
if ! git config user.email >/dev/null; then
  export GIT_AUTHOR_NAME=github-actions GIT_AUTHOR_EMAIL=github-actions@users.noreply.github.com
  export GIT_COMMITTER_NAME=$GIT_AUTHOR_NAME GIT_COMMITTER_EMAIL=$GIT_AUTHOR_EMAIL
fi
# A shallow CI checkout needs only the tip of demo-assets as the parent.
depth=()
[ "$(git rev-parse --is-shallow-repository)" = true ] && depth=(--depth 1)

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

paths=()
blobs=()
for f in "$@"; do
  case "$f" in /* | ../* | */../*) echo "not a relative path inside the current directory: $f" >&2; exit 1 ;; esac
  paths+=("$dir/${f#./}")
  blobs+=("$(git hash-object -w "$f")")
done

for attempt in 1 2 3; do
  git fetch -q ${depth[@]+"${depth[@]}"} origin +refs/heads/demo-assets:refs/remotes/origin/demo-assets 2>/dev/null || true
  parent=$(git rev-parse -q --verify refs/remotes/origin/demo-assets || true)
  export GIT_INDEX_FILE="$tmp/index"
  rm -f "$GIT_INDEX_FILE"
  if [ -n "$parent" ]; then git read-tree "$parent"; else git read-tree --empty; fi
  for i in "${!paths[@]}"; do git update-index --add --cacheinfo "100644,${blobs[$i]},${paths[$i]}"; done
  tree=$(git write-tree)
  unset GIT_INDEX_FILE
  commit=$(git commit-tree "$tree" ${parent:+-p "$parent"} -m "assets: $dir")
  if git push -q origin "$commit:refs/heads/demo-assets" 2>"$tmp/push.log"; then
    for p in "${paths[@]}"; do echo "https://raw.githubusercontent.com/$repo/$commit/$p"; done
    exit 0
  fi
  cat "$tmp/push.log" >&2; echo "push to demo-assets failed (attempt $attempt), retrying" >&2
done
exit 1
