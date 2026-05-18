#!/usr/bin/env bash
# Rig runner for the TWA acceptance suite.
#
# Spins up a dedicated vite dev server on port 5273 (NOT the user's
# 5173) so we don't fight with hot-reload, runs the suite, and tears
# down. Re-runs the suite TIMES times (env, default 3) so we can
# observe flakes — the AC bar is "green AND stable", not "green once".
set -euo pipefail

PORT=${PORT:-5273}
TIMES=${TIMES:-1}
HERE="$(cd "$(dirname "$0")/.." && pwd)"
cd "$HERE"

# Tear down any lingering test server on this port.
if lsof -nP -iTCP:"$PORT" -sTCP:LISTEN >/dev/null 2>&1; then
  echo "[rig] killing existing test server on :$PORT"
  PID=$(lsof -nP -iTCP:"$PORT" -sTCP:LISTEN -F p | awk '/^p/{print substr($0,2)}' | head -1)
  [ -n "$PID" ] && kill "$PID" 2>/dev/null || true
  sleep 1
fi

LOG=/tmp/orchard-test-dev.log
DEV_PID=""


restart_dev() {
  if [ -n "${DEV_PID:-}" ]; then
    kill "$DEV_PID" 2>/dev/null || true
    sleep 1
  fi
  : > "$LOG"
  pnpm exec vite dev --port "$PORT" --host 127.0.0.1 --strictPort > "$LOG" 2>&1 &
  DEV_PID=$!
  trap 'kill $DEV_PID 2>/dev/null || true' EXIT
  for i in $(seq 1 30); do
    if curl -s -m 1 "http://127.0.0.1:$PORT/" -o /dev/null; then return 0; fi
    sleep 0.5
  done
  echo "[rig] dev failed to come back up"; exit 4
}

# Each AC runs against a fresh dev server. Vite's WS proxy degrades
# under repeated test-page teardowns; restarting between tests gives
# every AC a clean slate. Slower (~12s overhead/AC) but stable.
ACS=("AC1:" "AC2:" "AC3:" "AC4:" "AC5:" "AC6:" "AC6b:" "AC7:" "AC8:")
FAILED=0
RESULTS=""
for i in $(seq 1 "$TIMES"); do
  echo
  echo "[rig] === PASS $i / $TIMES ==="
  for AC in "${ACS[@]}"; do
    restart_dev
    if pnpm exec playwright test tests/twa-acceptance.spec.ts -g "$AC" --reporter=line >/tmp/twa-ac.log 2>&1; then
      LINE="$AC ✓ ($(grep -oE '\([0-9.]+s\)' /tmp/twa-ac.log | head -1))"
    else
      LINE="$AC ✗"
      FAILED=$((FAILED + 1))
    fi
    echo "  $LINE"
    RESULTS+="$LINE
"
  done
done

echo
echo "[rig] === RESULTS ==="
printf '%s' "$RESULTS"
if [ "$FAILED" -gt 0 ]; then
  echo "[rig] $FAILED tests failed"
  exit 1
fi
echo "[rig] all tests PASSED"
