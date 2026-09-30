#!/usr/bin/env bash
# Installs atlas from a local clone of the repository.
set -euo pipefail

if ! command -v go >/dev/null 2>&1; then
  echo "error: Go is not installed. Install Go 1.22+ from https://go.dev/dl/" >&2
  exit 1
fi

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

echo "Installing atlas with $(go version)..."
go install ./cmd/atlas

gobin="$(go env GOBIN)"
if [ -z "$gobin" ]; then
  gobin="$(go env GOPATH)/bin"
fi
echo "Installed to $gobin/atlas"

case ":$PATH:" in
  *":$gobin:"*) ;;
  *)
    echo "warning: $gobin is not in your PATH." >&2
    echo "Add this to your shell profile:" >&2
    echo "  export PATH=\"\$PATH:$gobin\"" >&2
    ;;
esac
