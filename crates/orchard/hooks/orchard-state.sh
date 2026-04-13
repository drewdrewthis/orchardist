#!/usr/bin/env bash
# Orchard state hook — writes Claude session state to $TMPDIR for orchard to read.
# Registered for: PreToolUse, PostToolUse, PostToolUseFailure, Stop, Notification,
#                 SessionEnd, SessionStart, UserPromptSubmit
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
transcript_path=$(echo "$input" | jq -r '.transcript_path // empty')

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

# Reads the existing state file and extracts a field value (or empty string).
read_state_field() {
    local field="$1"
    if [ -f "$state_file" ]; then
        jq -r --arg f "$field" '.[$f] // empty' "$state_file" 2>/dev/null || true
    fi
}

# Runs orchard hook-enrich and returns the JSON object (or "{}").
run_hook_enrich() {
    if [ -z "$transcript_path" ]; then
        echo "{}"
        return
    fi
    if command -v orchard >/dev/null 2>&1; then
        orchard hook-enrich --transcript "$transcript_path" 2>/dev/null || echo "{}"
    else
        echo "{}"
    fi
}

# Writes the main state file atomically.
# Reads inflight count from sidecar. Preserves session_start_ts, model,
# last_tool, current_task, and state_changed_at across events; extra fields
# passed in $2 override. state_changed_at only updates when state transitions.
write_state() {
    local state="$1"
    local inflight_count
    inflight_count=$(read_inflight | jq 'length')

    # Preserve session_start_ts across all events.
    local session_start_ts
    session_start_ts=$(read_state_field "session_start_ts")

    # Preserve model across events unless being set explicitly.
    local existing_model
    existing_model=$(read_state_field "model")

    # Preserve last_tool across events — caller overwrites via extra when needed.
    local existing_last_tool
    existing_last_tool=$(read_state_field "last_tool")

    # Preserve current_task across events — caller overwrites via extra when needed.
    local existing_current_task
    existing_current_task=$(read_state_field "current_task")

    # state_changed_at: only update when the state actually changes.
    local existing_state
    existing_state=$(read_state_field "state")
    local state_changed_at=""
    if [ "$state" = "$existing_state" ]; then
        state_changed_at=$(read_state_field "state_changed_at")
    fi
    # If state changed or no previous state_changed_at, set to now.
    if [ -z "$state_changed_at" ]; then
        state_changed_at="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
    fi

    # Merge transcript enrichment.
    local enrichment
    enrichment=$(run_hook_enrich)

    # Merge: enrichment fields from transcript + any extra fields passed in $2 (JSON fragment).
    # NOTE: do NOT write "${2:-{}}" — bash parses '}' as closing the expansion, leaving a
    # stray '}' appended to the value when $2 is set, producing invalid JSON.
    local _empty_json='{}'
    local extra="${2:-$_empty_json}"

    local tmp="${state_file}.tmp.$$"

    # Build base object, preserve existing fields, then merge extra (overrides),
    # then merge enrichment. Order: existing → extra override → enrichment override.
    jq -n \
        --arg state "$state" \
        --arg session_id "$session_id" \
        --arg tmux_session "$tmux_session" \
        --arg cwd "$cwd" \
        --arg event "$event" \
        --arg timestamp "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
        --arg state_changed_at "$state_changed_at" \
        --argjson inflight_tool_count "$inflight_count" \
        --argjson session_start_ts_val "$([ -n "$session_start_ts" ] && echo "$session_start_ts" || echo "null")" \
        --arg existing_model "$existing_model" \
        --arg existing_last_tool "$existing_last_tool" \
        --arg existing_current_task "$existing_current_task" \
        --argjson enrichment "$enrichment" \
        --argjson extra "$extra" \
        '
        {
          state: $state,
          session_id: $session_id,
          tmux_session: $tmux_session,
          cwd: $cwd,
          event: $event,
          timestamp: $timestamp,
          state_changed_at: $state_changed_at,
          inflight_tool_count: $inflight_tool_count
        }
        # Preserve session_start_ts if available.
        | if $session_start_ts_val != null then . + {session_start_ts: $session_start_ts_val} else . end
        # Preserve existing model unless enrichment or extra supplies a new one.
        | if $existing_model != "" then . + {model: $existing_model} else . end
        # Preserve last_tool across events (PreToolUse extra will overwrite when needed).
        | if $existing_last_tool != "" then . + {last_tool: $existing_last_tool} else . end
        # Preserve current_task across events (UserPromptSubmit extra will overwrite when needed).
        | if $existing_current_task != "" then . + {current_task: $existing_current_task} else . end
        # Merge enrichment fields from transcript (model, inputTokens, etc.),
        # converting camelCase to snake_case for the state file.
        | if $enrichment | has("model") then . + {model: $enrichment.model} else . end
        | if $enrichment | has("inputTokens") then . + {input_tokens: $enrichment.inputTokens} else . end
        | if $enrichment | has("outputTokens") then . + {output_tokens: $enrichment.outputTokens} else . end
        | if $enrichment | has("cacheCreationInputTokens") then . + {cache_creation_input_tokens: $enrichment.cacheCreationInputTokens} else . end
        | if $enrichment | has("cacheReadInputTokens") then . + {cache_read_input_tokens: $enrichment.cacheReadInputTokens} else . end
        # Merge event-specific extra fields last — these override preserved values.
        | . + $extra
        ' \
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
        # Record last_tool (snake_case in state file).
        write_state "working" "$(jq -n --arg t "$tool_name" '{last_tool: $t}')"
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
    # Clear last_tool by not including it in the extra object.
    inflight_count=$(read_inflight | jq 'length')

    session_start_ts=$(read_state_field "session_start_ts")
    existing_model=$(read_state_field "model")
    existing_current_task=$(read_state_field "current_task")
    existing_input_tokens=$(read_state_field "input_tokens")
    existing_output_tokens=$(read_state_field "output_tokens")
    existing_cache_creation=$(read_state_field "cache_creation_input_tokens")
    existing_cache_read=$(read_state_field "cache_read_input_tokens")

    # state_changed_at: only update when state transitions (previous != "idle").
    existing_state=$(read_state_field "state")
    if [ "$existing_state" = "idle" ]; then
        state_changed_at=$(read_state_field "state_changed_at")
    fi
    if [ -z "${state_changed_at:-}" ]; then
        state_changed_at="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
    fi

    enrichment=$(run_hook_enrich)

    local_tmp="${state_file}.tmp.$$"
    jq -n \
        --arg state "idle" \
        --arg session_id "$session_id" \
        --arg tmux_session "$tmux_session" \
        --arg cwd "$cwd" \
        --arg event "$event" \
        --arg timestamp "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
        --arg state_changed_at "$state_changed_at" \
        --arg stop_reason "$stop_reason" \
        --argjson inflight_tool_count "$inflight_count" \
        --argjson session_start_ts_val "$([ -n "$session_start_ts" ] && echo "$session_start_ts" || echo "null")" \
        --arg existing_model "$existing_model" \
        --arg existing_current_task "$existing_current_task" \
        --arg existing_input_tokens "$existing_input_tokens" \
        --arg existing_output_tokens "$existing_output_tokens" \
        --arg existing_cache_creation "$existing_cache_creation" \
        --arg existing_cache_read "$existing_cache_read" \
        --argjson enrichment "$enrichment" \
        '
        {
          state: $state,
          session_id: $session_id,
          tmux_session: $tmux_session,
          cwd: $cwd,
          event: $event,
          timestamp: $timestamp,
          state_changed_at: $state_changed_at,
          stop_reason: $stop_reason,
          inflight_tool_count: $inflight_tool_count
        }
        # last_tool is intentionally NOT preserved on Stop.
        | if $session_start_ts_val != null then . + {session_start_ts: $session_start_ts_val} else . end
        | if $existing_model != "" then . + {model: $existing_model} else . end
        | if $existing_current_task != "" then . + {current_task: $existing_current_task} else . end
        | if $existing_input_tokens != "" then . + {input_tokens: ($existing_input_tokens | tonumber)} else . end
        | if $existing_output_tokens != "" then . + {output_tokens: ($existing_output_tokens | tonumber)} else . end
        | if $existing_cache_creation != "" then . + {cache_creation_input_tokens: ($existing_cache_creation | tonumber)} else . end
        | if $existing_cache_read != "" then . + {cache_read_input_tokens: ($existing_cache_read | tonumber)} else . end
        | if $enrichment | has("model") then . + {model: $enrichment.model} else . end
        | if $enrichment | has("inputTokens") then . + {input_tokens: $enrichment.inputTokens} else . end
        | if $enrichment | has("outputTokens") then . + {output_tokens: $enrichment.outputTokens} else . end
        | if $enrichment | has("cacheCreationInputTokens") then . + {cache_creation_input_tokens: $enrichment.cacheCreationInputTokens} else . end
        | if $enrichment | has("cacheReadInputTokens") then . + {cache_read_input_tokens: $enrichment.cacheReadInputTokens} else . end
        ' \
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
    model_from_input=$(echo "$input" | jq -r '.model // empty')
    write_inflight "[]"
    # Write session_start_ts and model at session start.
    session_start_ts_new=$(date +%s)
    extra="{}"
    if [ -n "$model_from_input" ]; then
        extra=$(jq -n --arg m "$model_from_input" --argjson ts "$session_start_ts_new" \
            '{model: $m, session_start_ts: $ts}')
    else
        extra=$(jq -n --argjson ts "$session_start_ts_new" '{session_start_ts: $ts}')
    fi

    # Write state directly (bypass write_state's session_start_ts preservation
    # since we're setting it fresh here).
    inflight_count=$(read_inflight | jq 'length')
    enrichment=$(run_hook_enrich)
    local_tmp="${state_file}.tmp.$$"
    # SessionStart always sets state_changed_at fresh.
    state_changed_at_now="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
    jq -n \
        --arg state "idle" \
        --arg session_id "$session_id" \
        --arg tmux_session "$tmux_session" \
        --arg cwd "$cwd" \
        --arg event "$event" \
        --arg timestamp "$state_changed_at_now" \
        --arg state_changed_at "$state_changed_at_now" \
        --argjson inflight_tool_count "$inflight_count" \
        --argjson extra "$extra" \
        --argjson enrichment "$enrichment" \
        '
        {
          state: $state,
          session_id: $session_id,
          tmux_session: $tmux_session,
          cwd: $cwd,
          event: $event,
          timestamp: $timestamp,
          state_changed_at: $state_changed_at,
          inflight_tool_count: $inflight_tool_count
        }
        | . + $extra
        | if $enrichment | has("model") then . + {model: $enrichment.model} else . end
        | if $enrichment | has("inputTokens") then . + {input_tokens: $enrichment.inputTokens} else . end
        | if $enrichment | has("outputTokens") then . + {output_tokens: $enrichment.outputTokens} else . end
        | if $enrichment | has("cacheCreationInputTokens") then . + {cache_creation_input_tokens: $enrichment.cacheCreationInputTokens} else . end
        | if $enrichment | has("cacheReadInputTokens") then . + {cache_read_input_tokens: $enrichment.cacheReadInputTokens} else . end
        ' \
        > "$local_tmp" && mv "$local_tmp" "$state_file"
    ;;

  UserPromptSubmit)
    prompt=$(echo "$input" | jq -r '.prompt // empty')
    # Keep first line only and truncate to 80 characters.
    first_line=$(printf '%s' "$prompt" | head -n 1)
    task=$(printf '%.80s' "$first_line")
    write_state "working" "$(jq -n --arg t "$task" '{current_task: $t}')"
    ;;

  SessionEnd)
    rm -f "$state_file" "$inflight_file"
    exit 0
    ;;

  *)
    exit 0
    ;;
esac
