#!/usr/bin/env bash
# gui-servers-up.sh — bring the live browser GUI up DURABLY.
#
# Root cause this fixes: the orchard daemon (:7777) and the vite preview
# server (:4173) used to run inside a Claude worker session. When that
# session died (burnout / reboot) the servers died with it and the public
# URL went 502/blank. This script runs them in a detached tmux session
# `gui-servers` (windows `daemon` + `vite`) that is independent of any
# Claude session, so they survive it dying. Idempotent: safe to re-run.
#
#   scripts/host-services/gui-servers-up.sh            # build + (re)launch + verify
#   scripts/host-services/gui-servers-up.sh --no-build # relaunch existing builds
#
# Verifies at the end: daemon /health 200, vite 200, public URL 200, and a
# real render (tests/render-gate.mjs). Exits non-zero if any gate fails.
set -euo pipefail

REPO="$(cd "$(dirname "$0")/../.." && pwd)"
GUI="$REPO/crates/orchard-gui"
SESSION="gui-servers"
PUBLIC_URL="https://orchard-gui.drewdrewthis.boxd.sh"
# The browser loads the GUI from the boxd HTTPS origin; the daemon's WS
# CheckOrigin must allowlist it or conversationChanged subscriptions 403.
ORIGIN="$PUBLIC_URL"
BUILD=true
[[ "${1:-}" == "--no-build" ]] && BUILD=false

cd "$REPO"

if $BUILD; then
  echo "[gui-servers] building daemon from HEAD…"
  go build -o bin/orchard-daemon ./cmd/orchard-daemon
  echo "[gui-servers] building GUI bundle…"
  ( cd "$GUI" && corepack pnpm build )
fi

echo "[gui-servers] (re)creating detached tmux session '$SESSION'…"
tmux kill-session -t "$SESSION" 2>/dev/null || true
tmux new-session -d -s "$SESSION" -n daemon -c "$REPO"
tmux send-keys -t "$SESSION:daemon" \
  "ORCHARD_INTROSPECTION=1 ORCHARD_ALLOWED_ORIGINS=$ORIGIN ./bin/orchard-daemon daemon start 2>&1 | tee /tmp/orchard-daemon.log" Enter
tmux new-window -t "$SESSION" -n vite -c "$GUI"
tmux send-keys -t "$SESSION:vite" \
  "corepack pnpm preview --port 4173 2>&1 | tee /tmp/vite-preview.log" Enter

wait_200() { # url, label
  for _ in $(seq 1 40); do
    code=$(curl -s -o /dev/null -w '%{http_code}' "$1" || true)
    [[ "$code" == "200" ]] && { echo "[gui-servers] $2: 200"; return 0; }
    sleep 0.5
  done
  echo "[gui-servers] $2: never reached 200 (last: ${code:-000})" >&2; return 1
}

wait_200 "http://127.0.0.1:7777/health" "daemon /health"
wait_200 "http://127.0.0.1:4173" "vite preview"
wait_200 "$PUBLIC_URL" "public URL"

echo "[gui-servers] verifying real render…"
( cd "$GUI" && node tests/render-gate.mjs "$PUBLIC_URL" )

echo "[gui-servers] UP — $PUBLIC_URL"
