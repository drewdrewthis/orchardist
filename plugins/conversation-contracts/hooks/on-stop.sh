#!/usr/bin/env bash
# on-stop.sh — Stop hook. Folds open contracts from the session jsonl and
# HARD-BLOCKS Stop while any is open. The block reason is self-documenting
# (names /close-contract and /my-contracts), so the block is the discovery
# surface. Block convention: emit {"decision":"block","reason":...} + exit 0;
# emit nothing + exit 0 to allow Stop.

set -uo pipefail

input=$(cat)
[ "$(printf '%s' "$input" | jq -r '.hook_event_name // empty' 2>/dev/null)" = "Stop" ] || exit 0
session_id=$(printf '%s' "$input" | jq -r '.session_id // empty' 2>/dev/null)
[ -n "$session_id" ] || exit 0

open_contracts=$(bash "${CLAUDE_PLUGIN_ROOT}/scripts/fold-contracts.sh" --session "$session_id" "${PWD:-}" 2>/dev/null || true)
[ -n "$open_contracts" ] || exit 0

count=$(printf '%s\n' "$open_contracts" | grep -c .)
reason="You own ${count} open contract(s). Close each before stopping: deliver with /close-contract <id> (cite evidence) or abandon (reason 'abandoned: ...'). List them with /my-contracts.

Open contracts:
${open_contracts}"

jq -n --arg reason "$reason" '{decision:"block", reason:$reason}'
exit 0
