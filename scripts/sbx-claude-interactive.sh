#!/usr/bin/env bash
# Create a git worktree and open an interactive Claude Code session in a Docker
# sandbox on it, with the model accessed through the JetBrains Central proxy.
# Same setup as scripts/sbx-claude.sh, but always interactive (no task).
# Usage: scripts/sbx-claude-interactive.sh <name>   (run from the main repo root)
#
# Re-running with the same <name> reattaches to the existing worktree and sandbox.
# One-time sandbox secrets setup: see scripts/sbx-claude.sh.
set -euo pipefail

name="${1:?usage: sbx-claude-interactive.sh <name>}"

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

exec sbx run --name "$name" \
  -e "ANTHROPIC_API_KEY=$key" \
  -e "ANTHROPIC_BASE_URL=http://host.docker.internal:${port}/wire/${key}/claude-code/anthropic" \
  claude "../$name" .git
