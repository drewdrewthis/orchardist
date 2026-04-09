#!/usr/bin/env bash
# Orchard state hook — writes Claude session state to $TMPDIR for orchard to read.
# Registered for: PreToolUse, PostToolUse, PostToolUseFailure, Stop, Notification,
#                 SessionEnd, SessionStart
#
# Two-file design (both keyed on tmux session name):
#   $TMPDIR/orchard-claude-<session>.json         — current state
#   $TMPDIR/orchard-claude-<session>.inflight.json — JSON array of open tool_use_ids
#
# Atomic rename is used on every write. No locking needed because hooks for one
# session are sequential (Claude calls them in order).

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

tmpdir="${TMPDIR:-/tmp}"
state_file="${tmpdir}/orchard-claude-${tmux_session}.json"
inflight_file="${tmpdir}/orchard-claude-${tmux_session}.inflight.json"

# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------

# Reads the current inflight array, returns "[]" if file is absent/malformed.
read_inflight() {
    if [ -f "$inflight_file" ]; then
        jq -e '. | arrays' "$inflight_file" 2>/dev/null || echo "[]"
    else
        echo "[]"
    fi
}

# Writes a JSON array to the inflight sidecar atomically.
write_inflight() {
    local arr="$1"
    local tmp="${inflight_file}.tmp.$$"
    printf '%s\n' "$arr" > "$tmp" && mv "$tmp" "$inflight_file"
}

# Writes the main state file atomically. Reads inflight count from sidecar.
write_state() {
    local state="$1"
    local inflight_count
    inflight_count=$(read_inflight | jq 'length')

    local tmp="${state_file}.tmp.$$"
    jq -n \
        --arg state "$state" \
        --arg session_id "$session_id" \
        --arg tmux_session "$tmux_session" \
        --arg cwd "$cwd" \
        --arg event "$event" \
        --arg timestamp "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
        --argjson inflight_tool_count "$inflight_count" \
        '{state: $state, session_id: $session_id, tmux_session: $tmux_session,
          cwd: $cwd, event: $event, timestamp: $timestamp,
          inflight_tool_count: $inflight_tool_count}' \
        > "$tmp" && mv "$tmp" "$state_file"
}

# ---------------------------------------------------------------------------
# Event dispatch
# ---------------------------------------------------------------------------

case "$event" in
  PreToolUse)
    tool_name=$(echo "$input" | jq -r '.tool_name // empty')
    tool_use_id=$(echo "$input" | jq -r '.tool_use_id // empty')

    if [ "$tool_name" = "AskUserQuestion" ]; then
        # Waiting for user — do not add to inflight; write input state.
        write_state "input"
    else
        # Add tool_use_id to the inflight sidecar (if non-empty).
        if [ -n "$tool_use_id" ]; then
            inflight=$(read_inflight)
            inflight=$(echo "$inflight" | jq --arg id "$tool_use_id" '. + [$id]')
            write_inflight "$inflight"
        fi
        write_state "working"
    fi
    ;;

  PostToolUse|PostToolUseFailure)
    tool_use_id=$(echo "$input" | jq -r '.tool_use_id // empty')

    if [ -n "$tool_use_id" ]; then
        inflight=$(read_inflight)
        inflight=$(echo "$inflight" | jq --arg id "$tool_use_id" '[.[] | select(. != $id)]')
        write_inflight "$inflight"
    fi
    write_state "working"
    ;;

  Stop)
    stop_reason=$(echo "$input" | jq -r '.stop_reason // empty')
    if [ "$stop_reason" = "tool_use" ]; then
        # Transitional stop mid-tool-loop — do not touch the state file.
        exit 0
    fi
    # end_turn, max_tokens, other, or empty → session is truly idle.
    inflight_count=$(read_inflight | jq 'length')
    local_tmp="${state_file}.tmp.$$"
    jq -n \
        --arg state "idle" \
        --arg session_id "$session_id" \
        --arg tmux_session "$tmux_session" \
        --arg cwd "$cwd" \
        --arg event "$event" \
        --arg timestamp "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
        --arg stop_reason "$stop_reason" \
        --argjson inflight_tool_count "$inflight_count" \
        '{state: $state, session_id: $session_id, tmux_session: $tmux_session,
          cwd: $cwd, event: $event, timestamp: $timestamp,
          stop_reason: $stop_reason,
          inflight_tool_count: $inflight_tool_count}' \
        > "$local_tmp" && mv "$local_tmp" "$state_file"
    ;;

  Notification)
    ntype=$(echo "$input" | jq -r '.notification_type // empty')
    case "$ntype" in
      permission_prompt|elicitation_dialog|idle_prompt) write_state "input" ;;
      *) exit 0 ;;
    esac
    ;;

  SessionStart)
    write_inflight "[]"
    write_state "idle"
    ;;

  SessionEnd)
    rm -f "$state_file" "$inflight_file"
    exit 0
    ;;

  *)
    exit 0
    ;;
esac
