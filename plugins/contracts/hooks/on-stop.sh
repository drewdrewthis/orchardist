#!/bin/bash
# claude-contracts plugin — Stop hook (v0.7).
#
# Mechanical reminder only. Lists open contracts owned by the calling
# session via the helper CLI; if any exist, emits a `systemMessage` so the
# user / next turn can see them. NEVER blocks the Stop. NEVER infers
# commitments from turn text.
#
# Spec: references/contracts.md §"Stop-hook reminder loop".

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

OPEN=$(bun run "$CLI" list-mine --owner "$SESSION_ID" 2>/dev/null || true)
if [[ -z "$OPEN" ]]; then
  exit 0
fi

# Emit a systemMessage but never block. The user / next-prompt sees the list.
jq -n --arg msg "🛡 Open contracts owned by this session:
$OPEN" '{
  systemMessage: $msg
}'
