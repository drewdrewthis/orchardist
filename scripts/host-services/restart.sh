#!/usr/bin/env bash
# host-service-restart.sh — restart a launchd (macOS) or systemd-user
# (Linux) service by name.
#
# Usage:
#   host-service-restart.sh [--json] [--host <machineID>] --name <name>
#
# Per L2 the --json flag emits:
#   {"ok": true, "data": {"name": "<name>", "action": "restart"}}
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
    printf '{"ok":true,"data":{"name":"%s","action":"restart"}}\n' "$NAME"
  else
    echo "restarted $NAME"
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
  # macOS: launchctl kickstart with -k (kill then restart) is the modern
  # way to restart. We try kickstart first and fall back to stop+start
  # for older macOS versions without a domain target.
  if ! launchctl kickstart -k "gui/$(id -u)/$NAME" >/dev/null 2>&1; then
    launchctl stop "$NAME" 2>/dev/null || true
    if output=$(launchctl start "$NAME" 2>&1); then
      emit_ok
    else
      emit_err "launchctl_error" "launchctl start $NAME after stop: $output"
    fi
  else
    emit_ok
  fi
elif command -v systemctl >/dev/null 2>&1; then
  if output=$(systemctl --user restart "$NAME" 2>&1); then
    emit_ok
  else
    emit_err "systemctl_error" "systemctl --user restart $NAME: $output"
  fi
else
  emit_err "service_manager_missing" "no service manager found (launchctl or systemctl)"
fi
