#!/usr/bin/env bash
# on-stop.sh — Stop hook for the conversation-contracts plugin.
#
# Responsibilities:
#   1. Query the daemon for open child contracts owned by this session.
#   2. Extract open TodoWrite items from the session jsonl (best-effort).
#   3. Compose a systemMessage enumerating the inventory.
#   4. Return {"continue": true, "systemMessage": "..."} when there are items;
#      return {"continue": true} (no message) when the inventory is empty.
#
# This hook CONTRIBUTES text only — it does NOT hard-block.
# The existing universal ~/.claude/hooks/open-contracts-block-stop.sh does
# the hard block.
#
# Sources:
#   - Open child contracts: Query.contracts(filter:{ownerSessionId, statuses:[SIGNED]})
#     with the conversation contract itself (fixed deliverable) excluded.
#   - Open TodoWrite items: most-recent TodoWrite tool_use in the session jsonl,
#     items whose status != "completed".
#
# No regex pattern matching against user message bodies is performed
# (per AC 3 amendment — hard signals only).

set -uo pipefail

# ---- constants ----------------------------------------------------------------

CONV_CONTRACT_DELIVERABLE="user agrees conversation has come to a close and there are no loose ends"
DAEMON_URL="${ORCHARD_DAEMON_URL:-http://127.0.0.1:7777}"

# ---- read hook payload --------------------------------------------------------

input=$(cat)
hook_event=$(printf '%s' "$input" | jq -r '.hook_event_name // empty' 2>/dev/null)
[ "$hook_event" = "Stop" ] || exit 0

session_id=$(printf '%s' "$input" | jq -r '.session_id // empty' 2>/dev/null)
[ -z "$session_id" ] && exit 0

# ---- resolve session jsonl path -----------------------------------------------
# Pattern: ~/.claude/projects/<encoded-cwd>/<session-uuid>.jsonl
# Encoding: '/' → '-', '.' → '-'

_encode_cwd() {
  printf '%s' "$1" | tr '/' '-' | tr '.' '-'
}

cwd="${PWD:-}"
home="${HOME:-}"
projects_root="${CLAUDE_PROJECTS_DIR:-}"
if [ -z "$projects_root" ] && [ -n "$home" ]; then
  projects_root="$home/.claude/projects"
fi
if [ -z "$cwd" ] || [ -z "$projects_root" ]; then
  session_jsonl=""
else
  encoded=$(_encode_cwd "$cwd")
  session_jsonl="$projects_root/$encoded/$session_id.jsonl"
fi

# ---- query daemon for open child contracts ------------------------------------

_query_open_child_contracts() {
  local sid="$1"

  # Build the GraphQL payload. The query uses the SIGNED status (v0.8 two-value
  # model: SIGNED = open, CLOSED = closed).
  local payload
  payload=$(jq -n --arg sid "$sid" \
    '{"query": ("{ contracts(filter:{ownerSessionId:\"" + $sid + "\",statuses:[SIGNED]}){ contractId statement } }")}')

  local response
  response=$(curl -s --max-time 5 \
    -X POST "$DAEMON_URL/graphql" \
    -H 'Content-Type: application/json' \
    -d "$payload" 2>/dev/null) || true

  if [ -z "$response" ]; then
    return
  fi

  # Extract contracts, exclude the conversation contract itself by deliverable.
  printf '%s' "$response" | jq -r \
    --arg skip "$CONV_CONTRACT_DELIVERABLE" \
    '.data.contracts[]? | select(.statement != $skip) | "- " + .contractId + ": " + .statement' \
    2>/dev/null || true
}

# ---- extract open TodoWrite items from session jsonl -------------------------
# Parse the most-recent TodoWrite tool_use in the session jsonl.
# Items with status != "completed" are "open".
# Returns one line per open item, prefixed with "- ".
# Degrades silently if the jsonl is absent or has no TodoWrite records.

_extract_open_todo_items() {
  local jsonl_path="$1"
  [ -f "$jsonl_path" ] || return

  # Find the most-recent TodoWrite tool_use event in the session jsonl.
  # We scan all lines, keep only the last TodoWrite content block.
  # jq -Rs slurps the file as a raw string, splits on newlines, parses each
  # line as JSON (skipping blanks/invalid), then reduces to find the last
  # assistant record whose content array has a TodoWrite tool_use block.
  jq -Rs '
    split("\n")
    | map(select(length > 0) | . as $line | try fromjson catch null)
    | map(select(. != null))
    | map(select(.type == "assistant"))
    | map(
        .message.content // []
        | map(select(.type == "tool_use" and .name == "TodoWrite"))
        | last
        | .input.todos // empty
      )
    | map(select(. != null))
    | last // empty
    | .[]
    | select(.status != "completed")
    | "- " + (if .content then .content elif .text then .text else tojson end)
  ' "$jsonl_path" 2>/dev/null || true
}

# ---- compose inventory --------------------------------------------------------

child_contracts=$(_query_open_child_contracts "$session_id")
open_todos=""
if [ -n "$session_jsonl" ]; then
  open_todos=$(_extract_open_todo_items "$session_jsonl")
fi

# Build inventory sections.
sections=""

if [ -n "$child_contracts" ]; then
  sections="${sections}Open child contracts:\n${child_contracts}\n"
fi

if [ -n "$open_todos" ]; then
  sections="${sections}Open TodoWrite items:\n${open_todos}\n"
fi

# ---- emit hook response -------------------------------------------------------

if [ -z "$sections" ]; then
  printf '{"continue":true}\n'
  exit 0
fi

jq -n --arg msg "$(printf '%b' "$sections")" \
  '{"continue": true, "systemMessage": $msg}'
