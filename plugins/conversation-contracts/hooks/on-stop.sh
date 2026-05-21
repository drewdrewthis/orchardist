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
# No regex pattern matching against user message bodies is performed.
# No "unanswered questions" heuristic is applied.

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
if [ -z "$cwd" ] || [ -z "$home" ]; then
  session_jsonl=""
else
  encoded=$(_encode_cwd "$cwd")
  session_jsonl="$home/.claude/projects/$encoded/$session_id.jsonl"
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
  python3 - "$jsonl_path" 2>/dev/null <<'PYEOF'
import json, sys

path = sys.argv[1]
last_todos = None

try:
    with open(path) as f:
        for line in f:
            line = line.strip()
            if not line:
                continue
            try:
                r = json.loads(line)
            except Exception:
                continue
            if r.get('type') != 'assistant':
                continue
            msg = r.get('message', {})
            for c in msg.get('content', []):
                if isinstance(c, dict) and c.get('type') == 'tool_use' and c.get('name') == 'TodoWrite':
                    inp = c.get('input', {})
                    todos = inp.get('todos', [])
                    if isinstance(todos, list):
                        last_todos = todos
except Exception:
    pass

if not last_todos:
    sys.exit(0)

for item in last_todos:
    if not isinstance(item, dict):
        continue
    status = item.get('status', '')
    if status == 'completed':
        continue
    content = item.get('content', item.get('text', str(item)))
    print('- ' + str(content))
PYEOF
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

# Trim trailing newline from sections for cleaner JSON.
msg=$(printf '%s' "$sections" | sed 's/\\n$//')

jq -n --arg msg "$(printf '%b' "$sections")" \
  '{"continue": true, "systemMessage": $msg}'
