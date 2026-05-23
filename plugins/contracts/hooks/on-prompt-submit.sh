#!/bin/bash
# claude-contracts plugin — UserPromptSubmit hook (v0.7).
#
# Lists this session's open contracts (status != delivered) and injects them
# as additionalContext on every user prompt. Mechanical: no inference, no
# judging, no parsing of turn text.

set -uo pipefail

INPUT=$(cat)

if ! command -v jq >/dev/null 2>&1; then
  exit 0
fi
if ! command -v bun >/dev/null 2>&1; then
  exit 0
fi

SESSION_ID=$(echo "$INPUT" | jq -r '.session_id // empty')
if [[ -z "$SESSION_ID" ]]; then
  exit 0
fi

CLI="${CLAUDE_PLUGIN_ROOT}/server/cli.ts"
if [[ ! -f "$CLI" ]]; then
  exit 0
fi

CONTEXT=$(bun run "$CLI" inject-prompt-context --owner "$SESSION_ID" 2>/dev/null || true)
if [[ -z "$CONTEXT" ]]; then
  exit 0
fi

jq -n --arg ctx "$CONTEXT" '{
  hookSpecificOutput: {
    hookEventName: "UserPromptSubmit",
    additionalContext: $ctx
  }
}'
