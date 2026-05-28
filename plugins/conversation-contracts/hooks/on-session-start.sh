#!/usr/bin/env bash
# on-session-start.sh — SessionStart hook. Auto-opens the conversation contract
# by emitting one `orchard_contract` open sentinel to stdout. The Claude Code
# harness records hook stdout in the session jsonl as a `hook_success`
# attachment whose `stdout` field is the literal string we printed; the fold
# script (scripts/fold-contracts.sh) finds it via its strings-via-fromjson path
# and treats it identically to a tool_result sentinel.
#
# Stateless: no file IO, no path resolution. SessionStart fires once per
# session, so the open is naturally one-per-conversation. The sentinel itself
# is rendered by scripts/emit-sentinel.sh — the single source of truth for the
# on-disk shape, shared with /open-contract and /close-contract.

set -uo pipefail

DELIVERABLE="user agrees conversation has come to a close and there are no loose ends"
id="C-$(date -u +%Y-%m-%d)-$(openssl rand -hex 4 2>/dev/null || printf '%08x' "$RANDOM$RANDOM")"

exec bash "${CLAUDE_PLUGIN_ROOT}/scripts/emit-sentinel.sh" open "$id" "$DELIVERABLE"
