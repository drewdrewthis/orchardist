#!/usr/bin/env bash
# on-prompt-submit.sh — UserPromptSubmit hook for the conversation-contracts plugin.
#
# Auto-opens the conversation contract for the current session by appending one
# `orchard_contract` open sentinel to the session jsonl. The Stop hook folds it
# (open minus close) and the /close-conversation skill (or /exit etc.) closes it.
#
# No MCP server, no resident process. Idempotent: the sentinel is appended only
# if no auto-open sentinel already exists in the jsonl, so repeated prompts in a
# session yield exactly one auto-open contract.
#
# Inputs:
#   stdin              — Claude Code passes a JSON payload with session_id, cwd,
#                        hook_event_name, prompt. (Tests may fire via env only.)
#   CLAUDE_SESSION_ID  — optional: use directly instead of parsing stdin.
#   CLAUDE_PROJECTS_DIR — optional: override the projects root (default
#                        $HOME/.claude/projects).

set -uo pipefail

# The fixed deliverable for auto-opened conversation contracts. No JSON-special
# characters, so it embeds safely in the sentinel.
DELIVERABLE="user agrees conversation has come to a close and there are no loose ends"

# Capture the hook payload (Claude Code passes it on stdin). When stdin is not a
# JSON payload (test harness fires via env vars), the payload stays empty and we
# fall back to env-derived values.
payload=""
if [ ! -t 0 ]; then
    payload=$(cat)
fi

SESSION_ID="${CLAUDE_SESSION_ID:-}"
HOOK_CWD="${PWD:-}"
if [ -n "$payload" ]; then
    sid=$(printf '%s' "$payload" | jq -r '.session_id // empty' 2>/dev/null)
    if [ -n "$sid" ]; then SESSION_ID="$sid"; fi
    pcwd=$(printf '%s' "$payload" | jq -r '.cwd // empty' 2>/dev/null)
    if [ -n "$pcwd" ]; then HOOK_CWD="$pcwd"; fi
fi

# Without a session id we cannot resolve the target jsonl; nothing to do.
if [ -z "$SESSION_ID" ]; then
    exit 0
fi

# ---- resolve session jsonl path -----------------------------------------------
# Pattern: <projects_root>/<encoded-cwd>/<session-uuid>.jsonl
# Encoding: '/' → '-', '.' → '-'.

_encode_cwd() {
    printf '%s' "$1" | tr '/' '-' | tr '.' '-'
}

home="${HOME:-}"
projects_root="${CLAUDE_PROJECTS_DIR:-}"
if [ -z "$projects_root" ] && [ -n "$home" ]; then
    projects_root="$home/.claude/projects"
fi
if [ -z "$HOOK_CWD" ] || [ -z "$projects_root" ]; then
    exit 0
fi
encoded=$(_encode_cwd "$HOOK_CWD")
project_dir="$projects_root/$encoded"
session_jsonl="$project_dir/$SESSION_ID.jsonl"

# ---- idempotent auto-open -----------------------------------------------------
# Append the sentinel only if no auto-open sentinel already exists for this
# session. This replaces the MCP fold-dedup; repeated prompts yield exactly one
# auto-open contract.

if [ -f "$session_jsonl" ] \
    && grep -Fq '"source":"auto-prompt-submit"' "$session_jsonl" 2>/dev/null; then
    exit 0
fi

# Generate an id matching the contract id format: C-YYYY-MM-DD-<8 hex>.
id="C-$(date -u +%Y-%m-%d)-$(openssl rand -hex 4 2>/dev/null || printf '%08x' "$RANDOM$RANDOM")"
ts="$(date -u +%Y-%m-%dT%H:%M:%SZ)"

# Ensure the project dir exists (the jsonl may not be created yet on the very
# first prompt). Appending a self-contained sentinel line that the shared fold
# picks up via its objects-path extraction.
mkdir -p "$project_dir" 2>/dev/null || true
printf '{"orchard_contract":"open","id":"%s","statement":"%s","ts":"%s","source":"auto-prompt-submit"}\n' \
    "$id" "$DELIVERABLE" "$ts" >> "$session_jsonl"

exit 0
