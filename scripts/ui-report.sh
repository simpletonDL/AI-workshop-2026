#!/usr/bin/env bash
# CI, after failed UI tests: publish the expected/actual/diff screenshots listed in test/ui/out/failures.json
# (written by test/ui/failures-reporter.mjs) to demo-assets and explain every failure with direct image links:
# in the log, as ::error annotations and as image tables in the job summary.
set -euo pipefail

root=$(git rev-parse --show-toplevel)
ui="$root/test/ui"
failures="$ui/out/failures.json"
summary=${GITHUB_STEP_SUMMARY:-/dev/null}
run_url="${GITHUB_SERVER_URL:-https://github.com}/${GITHUB_REPOSITORY:-}/actions/runs/${GITHUB_RUN_ID:-}"

if [ ! -s "$failures" ] || [ "$(jq length "$failures")" = 0 ]; then
  echo "No screenshot failures recorded in $failures; see the test log above."
  exit 0
fi

# Image paths are relative to test/ui; publish them keeping that path, so equal names of different tests don't clash.
images=$(jq -r '.[].snapshots[] | .expected, .actual, .diff | values' "$failures")
urls='{}'
if [ -n "$images" ]; then
  # shellcheck disable=SC2086 # the paths have no spaces: Playwright builds them from test titles with dashes
  links=$(cd "$ui" && "$root/scripts/publish-assets.sh" "ui-failures/${GITHUB_RUN_ID:-local}-${GITHUB_RUN_ATTEMPT:-1}" $images) ||
    echo "::warning::could not publish the screenshots; download the ui-report artifact instead"
  [ -n "${links:-}" ] && urls=$(paste <(echo "$images") <(echo "$links") | jq -R 'split("\t") | {(.[0]): .[1]}' | jq -s add)
fi

# Paths without a published URL stay as paths inside the ui-report artifact.
jq -r --argjson u "$urls" '
  def link($p): $u[$p] // "ui-report artifact: \($p)";
  .[] | "",
    "✘ UI test failed: \(.title)  (test/ui/\(.file):\(.line))",
    (.errors[] | "    \(.)"),
    (.snapshots[] | "    screenshot \(.name)\(if .message then ": " + .message else "" end)",
      (["expected", "actual", "diff"][] as $k | .[$k] // empty | "      \($k | . + "        " | .[:8]) \(link(.))"))
' "$failures"
echo
echo "Full Playwright report: the ui-report artifact of $run_url"
echo "An intended UI change? Re-render the baselines: scripts/ui-test.sh --update"

# Annotations: shown on the run page and next to the test in the PR diff.
jq -r --argjson u "$urls" '
  .[] | . as $f
  | if (.snapshots | length) > 0
    then .snapshots[] | "::error file=test/ui/\($f.file),line=\($f.line),title=UI test failed — \($f.title | gsub("[,:]"; " "))::\(.name) \(.message // "differs") — diff: \($u[.diff] // $u[.actual] // "see ui-report artifact")"
    else "::error file=test/ui/\($f.file),line=\($f.line),title=UI test failed — \($f.title | gsub("[,:]"; " "))::\(.errors | join(" ") | .[:500])"
    end
' "$failures"

jq -r --argjson u "$urls" --arg run "$run_url" '
  def img($p): if $p == null then "—" elif $u[$p] then "<a href=\"\($u[$p])\"><img src=\"\($u[$p])\" width=\"400\"></a>" else "`\($p)`" end;
  "## ❌ UI tests: \(length) failed", "",
  (.[] | "### \(.title)", "`test/ui/\(.file):\(.line)`", "",
    (.errors[] | "> \(.)", ""),
    (.snapshots[] | "**\(.name)**\(if .message then " — " + .message else "" end)", "",
      "| Expected | Actual | Diff |", "|---|---|---|",
      "| \(img(.expected)) | \(img(.actual)) | \(img(.diff)) |", "")),
  "Full Playwright report (trace, all attachments): the `ui-report` artifact of [this run](\($run)#artifacts).",
  "An intended UI change? Re-render the baselines with `scripts/ui-test.sh --update` and commit them."
' "$failures" >>"$summary"
