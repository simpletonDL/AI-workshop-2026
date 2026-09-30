#!/usr/bin/env bash
# Create a git worktree for a task and run Claude Code in a Docker sandbox on it,
# with the model accessed through the JetBrains Central proxy.
# Usage: scripts/sbx-claude.sh <name> [<task>]   (run from the main repo root)
#
# Without <task>, opens an interactive Claude session in the sandbox.
# With <task>, runs Claude headless (claude -p) on the task, prints the agent's
# final report to stdout and also saves it to logs/<name>.log. The exit code is
# Claude's; whether the task itself succeeded is in the report's STATUS line.
#
# One-time setup so the sandbox can push, open PRs and reach YouTrack:
#   sbx secret set github --command "gh auth token"
#   sbx secret set-custom --host youtrack.jetbrains.com --env YOUTRACK_TOKEN --value "$YOUTRACK_TOKEN"
set -euo pipefail

name="${1:?usage: sbx-claude.sh <name> [<task>]}"
task="${2:-}"

if [ ! -d .git ]; then
  echo "Run from the main repo root (not from a worktree)" >&2
  exit 1
fi

if [ ! -d "../$name" ]; then
  git fetch origin
  git worktree add -b "$name" "../$name" origin/main
fi

central=$(command -v central || command -v jbcentral) || {
  echo "JetBrains Central CLI (central/jbcentral) not found" >&2
  exit 1
}
port=$(jq -r '.proxy_port // 19516' ~/.jetbrains-central/config.json)
key=$("$central" proxy start --ensure-updated --return-key)
if [ -z "$key" ]; then
  echo "Failed to get a JetBrains Central proxy key" >&2
  exit 1
fi
env_args=(
  -e "ANTHROPIC_API_KEY=$key"
  -e "ANTHROPIC_BASE_URL=http://host.docker.internal:${port}/wire/${key}/claude-code/anthropic"
)

if [ -z "$task" ]; then
  exec sbx run --name "$name" "${env_args[@]}" claude "../$name" .git
fi

# sbx refuses a non-interactive run without --detached: create the sandbox
# first if needed, then run Claude in it with exec (which starts a stopped one).
if ! sbx ls | awk 'NR > 1 { print $1 }' | grep -qx -- "$name"; then
  sbx run --detached --name "$name" "${env_args[@]}" claude "../$name" .git >/dev/null
fi

mkdir -p logs
sbx exec "${env_args[@]}" "$name" claude -p "$task" </dev/null | tee "logs/$name.log"
exit "${PIPESTATUS[0]}"
