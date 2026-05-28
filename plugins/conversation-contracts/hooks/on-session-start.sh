#!/usr/bin/env bash
# on-session-start.sh — SessionStart hook. Auto-opens the conversation contract
# by emitting one `orchard_contract` open sentinel to stdout. The Claude Code
# harness records hook stdout in the session jsonl as a `hook_success`
# attachment whose `stdout` field is the literal string we printed — the fold
# script (scripts/fold-contracts.sh) finds it via its strings-via-fromjson path
# and treats it identically to a tool_result sentinel.
#
# Stateless: no file IO, no path resolution, no idempotency dance. SessionStart
# fires exactly once per session, so the open is naturally one-per-conversation.

set -uo pipefail

DELIVERABLE="user agrees conversation has come to a close and there are no loose ends"
id="C-$(date -u +%Y-%m-%d)-$(openssl rand -hex 4 2>/dev/null || printf '%08x' "$RANDOM$RANDOM")"
ts=$(date -u +%Y-%m-%dT%H:%M:%SZ)

printf '{"orchard_contract":"open","id":"%s","statement":"%s","ts":"%s","source":"auto-session-start"}\n' \
  "$id" "$DELIVERABLE" "$ts"
exit 0
