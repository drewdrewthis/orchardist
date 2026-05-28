#!/usr/bin/env bash
# on-stop.sh — Stop hook. Folds open contracts from the session jsonl and
# blocks Stop while any is open. The hook is DUMB by design: it throws the
# open contracts back at the agent verbatim. The discipline lives in each
# contract's statement (the conversation contract's statement points at
# /i-am-done; work-contract statements carry their own done-conditions).
#
# Procedural reminders about close mechanics ("deliver with /close-contract
# <id>", etc.) are NOT the hook's job — they were carried in v0.9.x but
# duplicated and contradicted the statement-as-discipline design landed in
# v0.10.0+. Hook is mechanism; statement is policy.
#
# Block convention: emit {"decision":"block","reason":...} + exit 0;
# emit nothing + exit 0 to allow Stop.

set -uo pipefail

input=$(cat)
[ "$(printf '%s' "$input" | jq -r '.hook_event_name // empty' 2>/dev/null)" = "Stop" ] || exit 0
session_id=$(printf '%s' "$input" | jq -r '.session_id // empty' 2>/dev/null)
[ -n "$session_id" ] || exit 0

open_contracts=$(bash "${CLAUDE_PLUGIN_ROOT}/scripts/fold-contracts.sh" --session "$session_id" "${PWD:-}" 2>/dev/null || true)
[ -n "$open_contracts" ] || exit 0

reason="Open contracts:
${open_contracts}"

jq -n --arg reason "$reason" '{decision:"block", reason:$reason}'
exit 0
