#!/usr/bin/env bash
# on-prompt-submit.sh — UserPromptSubmit hook. Auto-opens the conversation
# contract by appending one `orchard_contract` open sentinel to the session
# jsonl. Idempotent: appends only if no auto-open sentinel already exists, so
# repeated prompts yield exactly one. No MCP, no resident process.

set -uo pipefail

DELIVERABLE="user agrees conversation has come to a close and there are no loose ends"

# session_id + cwd come from the stdin payload (real Claude Code) or env.
payload=""
[ -t 0 ] || payload=$(cat)
session_id="${CLAUDE_SESSION_ID:-}"
cwd="${PWD:-}"
if [ -n "$payload" ]; then
  sid=$(printf '%s' "$payload" | jq -r '.session_id // empty' 2>/dev/null); [ -n "$sid" ] && session_id="$sid"
  pcwd=$(printf '%s' "$payload" | jq -r '.cwd // empty' 2>/dev/null); [ -n "$pcwd" ] && cwd="$pcwd"
fi
[ -n "$session_id" ] && [ -n "$cwd" ] || exit 0

root="${CLAUDE_PROJECTS_DIR:-$HOME/.claude/projects}"
jsonl="$root/$(printf '%s' "$cwd" | tr '/.' '--')/$session_id.jsonl"

# Idempotent: skip if an auto-open sentinel already exists for this session.
[ -f "$jsonl" ] && grep -Fq '"source":"auto-prompt-submit"' "$jsonl" 2>/dev/null && exit 0

id="C-$(date -u +%Y-%m-%d)-$(openssl rand -hex 4 2>/dev/null || printf '%08x' "$RANDOM$RANDOM")"
mkdir -p "$(dirname "$jsonl")" 2>/dev/null || true
printf '{"orchard_contract":"open","id":"%s","statement":"%s","ts":"%s","source":"auto-prompt-submit"}\n' \
  "$id" "$DELIVERABLE" "$(date -u +%Y-%m-%dT%H:%M:%SZ)" >> "$jsonl"
exit 0
