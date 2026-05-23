#!/bin/bash
# claude-contracts plugin — SessionStart hook (v0.7).
#
# On session start, injects:
#   1. A one-line header: agent_name + session_id + count of open contracts
#   2. The open-contracts block (same shape as UserPromptSubmit)
#
# Mechanical only. No judging, no nudges, no inference.

set -uo pipefail

INPUT=$(cat)

if ! command -v jq >/dev/null 2>&1; then
  exit 0
fi
if ! command -v bun >/dev/null 2>&1; then
  exit 0
fi

SESSION_ID=$(echo "$INPUT" | jq -r '.session_id // empty')
AGENT_NAME=$(tmux display-message -p '#S' 2>/dev/null || echo "${USER:-unknown}")
if [[ -z "$SESSION_ID" ]]; then
  exit 0
fi

CLI="${CLAUDE_PLUGIN_ROOT}/server/cli.ts"
if [[ ! -f "$CLI" ]]; then
  exit 0
fi

MINE=$(bun run "$CLI" list-mine --owner "$SESSION_ID" 2>/dev/null || true)
NUM_MINE=$(printf '%s' "$MINE" | grep -c '^- ' || true)
CONTEXT=$(bun run "$CLI" inject-prompt-context --owner "$SESSION_ID" 2>/dev/null || true)

HEADER="[CONTRACTS ENGINE — SESSION START]
You are: agent_name=$AGENT_NAME, session_id=$SESSION_ID
Open contracts you own: $NUM_MINE

Drive each open contract to a delivered event (use update_contract). Abandonment
is logged as status=delivered with reasoning prefixed 'abandoned:'."

if [[ -n "$CONTEXT" ]]; then
  FULL="$HEADER

$CONTEXT"
else
  FULL="$HEADER"
fi

jq -n --arg ctx "$FULL" '{
  hookSpecificOutput: {
    hookEventName: "SessionStart",
    additionalContext: $ctx
  }
}'
