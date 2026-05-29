#!/usr/bin/env bash
# on-stop.sh — Stop hook. Folds open contracts from the session jsonl and
# blocks Stop while any is open. The hook is DUMB about CONTENT: it throws
# the open contracts back at the agent verbatim, no procedural commentary
# about close mechanics. Discipline lives in each contract's statement
# (conversation contract → /i-am-done; work contracts carry their own
# done-conditions). v0.11.2 stripped the v0.9.x procedural prose for this
# reason — content discipline is statement policy, not hook code.
#
# The hook is NOT dumb about Claude Code's MECHANISM: it honors the
# stop_hook_active re-entry protocol so we deliver one block per generation,
# not nine-then-override.
#
# Block convention: emit {"decision":"block","reason":...} + exit 0;
# emit nothing + exit 0 to allow Stop.
#
# Re-entry protocol — stop_hook_active:
#   Claude Code's runQuery loop tracks `stopHookBlockingCount`. The first
#   Stop hook block fires the discipline; on subsequent re-fires within the
#   same generation, the harness sets `stop_hook_active: true` in the hook's
#   stdin payload. After CLAUDE_CODE_STOP_HOOK_BLOCK_CAP (default 8)
#   consecutive blocks the harness force-overrides with a fixed message
#   ("A hook blocked the turn from ending 9 consecutive times — overriding
#   and ending turn"), drowning the open-contracts list. Honest behavior:
#   surface the ledger ONCE per generation, then step aside on re-entry so
#   the model can either drive toward delivery or hand back to the user
#   cleanly. New user message → new generation → counter resets → block
#   again on the next first Stop. The discipline is preserved (every
#   generation that would end with debt gets a block), the noise is not.

set -uo pipefail

input=$(cat)
[ "$(printf '%s' "$input" | jq -r '.hook_event_name // empty' 2>/dev/null)" = "Stop" ] || exit 0

# Re-entry guard: harness already blocked this generation once, let the
# turn end cleanly. Without this, blocks 2–8 stack silently and the 9th
# triggers the harness override path that drowns the ledger.
[ "$(printf '%s' "$input" | jq -r '.stop_hook_active // false' 2>/dev/null)" = "true" ] && exit 0

session_id=$(printf '%s' "$input" | jq -r '.session_id // empty' 2>/dev/null)
[ -n "$session_id" ] || exit 0

open_contracts=$(bash "${CLAUDE_PLUGIN_ROOT}/scripts/fold-contracts.sh" --session "$session_id" "${PWD:-}" 2>/dev/null || true)
[ -n "$open_contracts" ] || exit 0

reason="Open contracts:
${open_contracts}"

jq -n --arg reason "$reason" '{decision:"block", reason:$reason}'
exit 0
