#!/usr/bin/env bash
# Build and start minute2win-server on a free port with throwaway state, then run the Playwright check in its container.
#   tests/ui/run.sh [screenshot-dir]   (default: a temp dir, printed)
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
SHOTS="${1:-$(mktemp -d)}"; mkdir -p "$SHOTS"
W="$(mktemp -d)"; PORT="${PORT:-18080}"
(cd "$ROOT/server" && go build -o "$W/minute2win-server" .)
"$W/minute2win-server" -listen "127.0.0.1:$PORT" -manifests "$ROOT/manifests" -data "" >"$W/server.log" 2>&1 &
PID=$!; trap 'kill $PID 2>/dev/null || true; rm -rf "$W"' EXIT
for _ in $(seq 50); do curl -sf "http://127.0.0.1:$PORT/api/state" >/dev/null && break; sleep 0.1; done
podman run --rm --network host -v "$ROOT/tests/ui":/t:Z,ro -v "$SHOTS":/shots:Z "${PW_IMAGE:-mcr.microsoft.com/playwright/python:v1.55.0-noble}" \
  sh -c "pip install -q --disable-pip-version-check playwright==1.55.0 >/dev/null 2>&1; python3 /t/test_ui.py http://127.0.0.1:$PORT /shots"
echo "screenshots: $SHOTS"
