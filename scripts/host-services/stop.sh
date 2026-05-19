#!/usr/bin/env bash
# host-service-stop.sh — stop a launchd (macOS) or systemd-user (Linux)
# service by name.
#
# Usage:
#   host-service-stop.sh [--json] [--host <machineID>] --name <name>
#
# Per L2 the --json flag emits:
#   {"ok": true, "data": {"name": "<name>", "action": "stop"}}
#   {"ok": false, "error": {"code": "<code>", "message": "<message>"}}
set -euo pipefail

JSON=false
NAME=""
HOST=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    --json) JSON=true; shift ;;
    --host) HOST="$2"; shift 2 ;;
    --name) NAME="$2"; shift 2 ;;
    *) echo "unknown arg: $1" >&2; exit 2 ;;
  esac
done

if [[ -z "$NAME" ]]; then
  if $JSON; then
    printf '{"ok":false,"error":{"code":"invalid_input","message":"--name is required"}}\n'
  else
    echo "error: --name is required" >&2
  fi
  exit 1
fi

emit_ok() {
  if $JSON; then
    printf '{"ok":true,"data":{"name":"%s","action":"stop"}}\n' "$NAME"
  else
    echo "stopped $NAME"
  fi
}

emit_err() {
  local code="$1" msg="$2"
  if $JSON; then
    printf '{"ok":false,"error":{"code":"%s","message":"%s"}}\n' "$code" "$msg"
  else
    echo "error: $msg" >&2
  fi
  exit 1
}

if command -v launchctl >/dev/null 2>&1; then
  if output=$(launchctl stop "$NAME" 2>&1); then
    emit_ok
  else
    emit_err "launchctl_error" "launchctl stop $NAME: $output"
  fi
elif command -v systemctl >/dev/null 2>&1; then
  if output=$(systemctl --user stop "$NAME" 2>&1); then
    emit_ok
  else
    emit_err "systemctl_error" "systemctl --user stop $NAME: $output"
  fi
else
  emit_err "service_manager_missing" "no service manager found (launchctl or systemctl)"
fi
