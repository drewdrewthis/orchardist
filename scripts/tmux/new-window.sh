#!/usr/bin/env bash
# tmux-new-window.sh — Create a new window in a tmux session.
#
# Usage: tmux-new-window.sh --session <name> [--name <windowName>] [--json]
#
# L2: --json emits {"ok": bool, "data"?: {"session":str,"index":int,"name":str}, "error"?: ...}
# L1: canonical operation.
# L11: never calls the daemon.
set -euo pipefail

SESSION=""
WINDOW_NAME=""
JSON_MODE=0

while [[ $# -gt 0 ]]; do
  case "$1" in
    --session) SESSION="$2"; shift 2 ;;
    --name)    WINDOW_NAME="$2"; shift 2 ;;
    --json)    JSON_MODE=1; shift ;;
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

if [[ -z "$SESSION" ]]; then
  fail "INVALID_INPUT" "--session is required"
fi

# Build new-window args.
args=("new-window" "-t" "$SESSION" "-P" "-F" "#{session_name}:#{window_index}:#{window_name}")
if [[ -n "$WINDOW_NAME" ]]; then
  args+=("-n" "$WINDOW_NAME")
fi

if ! output=$(tmux "${args[@]}" 2>&1); then
  fail "TMUX_ERROR" "new-window failed: $output"
fi

# output = "sessionName:index:windowName"
IFS=':' read -r sess_out idx_out name_out <<< "$output"
ok '{"session":"'"$sess_out"'","index":'"$idx_out"',"name":"'"$name_out"'"}'
