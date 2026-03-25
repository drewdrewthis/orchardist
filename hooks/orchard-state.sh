#!/usr/bin/env bash
# Orchard state hook — writes Claude session state to /tmp for orchard to read.
# Registered for: PreToolUse, PostToolUse, Stop, Notification, SessionEnd, SessionStart

set -euo pipefail
umask 077

# Only run inside tmux
[ -z "${TMUX:-}" ] && exit 0

input=$(cat)
event=$(echo "$input" | jq -r '.hook_event_name // empty')
session_id=$(echo "$input" | jq -r '.session_id // empty')
cwd=$(echo "$input" | jq -r '.cwd // empty')

# Derive tmux session name
tmux_session=$(tmux display-message -p '#S' 2>/dev/null || true)
[ -z "$tmux_session" ] && exit 0

state_file="/tmp/orchard-claude-${tmux_session}.json"

case "$event" in
  PreToolUse|PostToolUse)
    state="working"
    ;;
  Stop)
    state="idle"
    ;;
  Notification)
    ntype=$(echo "$input" | jq -r '.notification_type // empty')
    case "$ntype" in
      permission_prompt|elicitation_dialog) state="input" ;;
      idle_prompt) state="input" ;;
      *) exit 0 ;;
    esac
    ;;
  SessionStart)
    state="idle"
    ;;
  SessionEnd)
    rm -f "$state_file"
    exit 0
    ;;
  *)
    exit 0
    ;;
esac

# Write state file atomically
tmp_file="${state_file}.tmp.$$"
jq -n \
  --arg state "$state" \
  --arg session_id "$session_id" \
  --arg tmux_session "$tmux_session" \
  --arg cwd "$cwd" \
  --arg event "$event" \
  --arg timestamp "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
  '{state: $state, session_id: $session_id, tmux_session: $tmux_session, cwd: $cwd, event: $event, timestamp: $timestamp}' \
  > "$tmp_file" && mv "$tmp_file" "$state_file"
