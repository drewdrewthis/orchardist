#!/usr/bin/env bash
# tmux-kill-pane.sh — Kill a tmux pane.
#
# Usage: tmux-kill-pane.sh --pane <paneId> [--json]
#
# L2: --json emits {"ok": bool, "data"?: ..., "error"?: {"code": str, "message": str}}
# L1: canonical operation.
# L11: never calls the daemon.
set -euo pipefail

PANE_ID=""
JSON_MODE=0

while [[ $# -gt 0 ]]; do
  case "$1" in
    --pane) PANE_ID="$2"; shift 2 ;;
    --json) JSON_MODE=1; shift ;;
    *) echo "unknown arg: $1" >&2; exit 1 ;;
  esac
done

fail() {
  local code="$1" msg="$2"
  if [[ "$JSON_MODE" -eq 1 ]]; then
    printf '{"ok":false,"error":{"code":"%s","message":"%s"}}\n' \
      "$code" "$(printf '%s' "$msg" | sed 's/"/\\"/g')"
  else
    echo "error: $msg" >&2
  fi
  exit 1
}

ok() {
  local data="$1"
  if [[ "$JSON_MODE" -eq 1 ]]; then
    printf '{"ok":true,"data":%s}\n' "$data"
  fi
}

if [[ -z "$PANE_ID" ]]; then
  fail "INVALID_INPUT" "--pane is required"
fi

if ! err=$(tmux kill-pane -t "$PANE_ID" 2>&1); then
  fail "TMUX_ERROR" "kill-pane failed: $err"
fi

ok '{"paneId":"'"$PANE_ID"'"}'
