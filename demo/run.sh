#!/bin/sh
# Entry point inside the demo image: start atlas serve, record one scenario into /out.
# Usage: run.sh <scenario> [atlas serve flags]
set -eu
scenario=$1
shift
tmp=$(mktemp -d)
atlas serve --addr 127.0.0.1:0 --cache-dir "$tmp" "$@" >"$tmp/serve.log" 2>&1 &
pid=$!

url=""
for _ in $(seq 1 50); do
  url=$(sed -n 's/^Serving atlas on \(http[^ ]*\).*/\1/p' "$tmp/serve.log")
  [ -n "$url" ] && break
  kill -0 "$pid" 2>/dev/null || { cat "$tmp/serve.log" >&2; exit 1; }
  sleep 0.1
done
[ -n "$url" ] || { echo "atlas serve did not start" >&2; cat "$tmp/serve.log" >&2; exit 1; }

node /opt/demo/record.mjs "$scenario" "$url" /out
