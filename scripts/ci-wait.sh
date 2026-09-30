#!/usr/bin/env bash
# Wait for the GitHub Actions "Tests" workflow run for the current HEAD commit.
# Exit 0 if CI is green, 1 otherwise (failed job logs are printed).
set -euo pipefail

workflow="Tests"
sha=$(git rev-parse HEAD)

if [ -z "$(git branch -r --contains "$sha" 2>/dev/null)" ]; then
  echo "HEAD $sha is not pushed. Push first: git push" >&2
  exit 1
fi

# GitHub may need a few seconds to register the run after a push.
run_id=""
for _ in $(seq 1 30); do
  run_id=$(gh run list --commit "$sha" --workflow "$workflow" --limit 1 \
    --json databaseId -q '.[0].databaseId' 2>/dev/null || true)
  [ -n "$run_id" ] && break
  sleep 2
done

if [ -z "$run_id" ]; then
  echo "No '$workflow' run found for $sha" >&2
  exit 1
fi

echo "Waiting for CI run $run_id (commit $sha)..."
if gh run watch "$run_id" --exit-status --interval 10 >/dev/null; then
  echo "CI GREEN for $sha"
  gh run view "$run_id"
else
  echo "CI FAILED for $sha" >&2
  gh run view "$run_id"
  echo "----- failed job logs (tail) -----"
  gh run view "$run_id" --log-failed | tail -200
  exit 1
fi
