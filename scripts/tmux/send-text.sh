#!/usr/bin/env bash
# tmux-send-text.sh — Send literal text to a tmux pane, followed by Enter.
#
# Usage: tmux-send-text.sh --pane <paneId> --text <text> [--json]
#
# L2: --json flag emits {"ok": bool, "data": {...}?, "error": {"code": str, "message": str}?}
# L1: canonical operation; daemon and CLI both exec this script.
# L11: never calls the daemon.
set -euo pipefail

PANE_ID=""
TEXT=""
JSON_MODE=0

while [[ $# -gt 0 ]]; do
  case "$1" in
    --pane) PANE_ID="$2"; shift 2 ;;
    --text) TEXT="$2"; shift 2 ;;
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
if [[ -z "$TEXT" ]]; then
  fail "INVALID_INPUT" "--text is required"
fi

# Two-step send-keys: write text literally (no shell interpretation), then Enter.
if ! err=$(tmux send-keys -t "$PANE_ID" -l "$TEXT" 2>&1); then
  fail "TMUX_ERROR" "send-keys -l failed: $err"
fi
if ! err=$(tmux send-keys -t "$PANE_ID" "Enter" 2>&1); then
  fail "TMUX_ERROR" "send-keys Enter failed: $err"
fi

ok '{"paneId":"'"$PANE_ID"'"}'
