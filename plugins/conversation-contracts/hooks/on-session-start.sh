#!/usr/bin/env bash
# on-session-start.sh — SessionStart hook. Auto-opens the conversation contract
# by emitting one `orchard_contract` open sentinel to stdout. The Claude Code
# harness records hook stdout in the session jsonl as a `hook_success`
# attachment whose `stdout` field is the literal string we printed; the fold
# script (scripts/fold-contracts.sh) finds it via its strings-via-fromjson path
# and treats it identically to a tool_result sentinel.
#
# Stateless: no file IO except reading the statement file. SessionStart fires
# once per session, so the open is naturally one-per-conversation. The sentinel
# itself is rendered by scripts/emit-sentinel.sh — the single source of truth
# for the on-disk shape, shared with /open-contract and /close-contract.
#
# The conversation contract's statement is the discipline gateway: it embeds
# the failure-mode self-audit and the /i-am-done invocation. Statement is read
# from references/conversation-contract-statement.md so the discipline is
# editable without touching the hook. If the file is missing or empty, fall
# back to the minimal closure-deliverable string.
#
# Plugin root resolution: prefer $CLAUDE_PLUGIN_ROOT (what real Claude Code
# exports to hook subprocesses); fall back to $(BASH_SOURCE[0])/.. so the
# hook still works when invoked directly (tests, smoke checks, harness
# misconfig). The fallback covers BOTH the statement-file path and the
# emit-sentinel.sh exec path.
#
# Ephemeral-mode skip: `claude --print` and SDK invocations set
# CLAUDE_CODE_ENTRYPOINT=sdk-cli. They have no interactive user to "agree the
# conversation has come to a close" and end after a single turn — auto-opening
# a contract there shifts the agent's final reply into bogus close-confirmation
# text instead of the requested output. The discriminator is intentionally a
# narrow denylist on known ephemeral values (not an allowlist on "cli") so a
# future interactive entrypoint (e.g. cli-tui, vscode-extension) keeps
# auto-opening rather than silently falling out of the gateway.

set -uo pipefail

# Ephemeral one-shot? Skip silently — empty stdout means the harness records
# no SessionStart attachment, the fold sees nothing, and the agent's final text
# is the task output the caller asked for. Verified value (Claude Code 2.1.x):
# `claude --print` sets CLAUDE_CODE_ENTRYPOINT=sdk-cli; interactive sets `cli`.
case "${CLAUDE_CODE_ENTRYPOINT:-}" in
  sdk-cli) exit 0 ;;
esac

FALLBACK="user agrees conversation has come to a close and there are no loose ends"

# Resolve the plugin root: env var first, then the hook's own parent dir.
# Use `cd -P` / `pwd -P` to match scripts/fold-contracts.sh — both resolve
# symlinks so the plugin works under symlinked install paths consistently.
PLUGIN_ROOT="${CLAUDE_PLUGIN_ROOT:-}"
if [ -z "$PLUGIN_ROOT" ]; then
  PLUGIN_ROOT="$(cd -P "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
fi

STATEMENT_FILE="$PLUGIN_ROOT/references/conversation-contract-statement.md"

if [ -r "$STATEMENT_FILE" ]; then
  # Single source of truth for the normalization shape lives in
  # scripts/collapse-statement.sh — keeps hook and test in sync.
  DELIVERABLE=$(bash "$PLUGIN_ROOT/scripts/collapse-statement.sh" "$STATEMENT_FILE")
fi
DELIVERABLE="${DELIVERABLE:-$FALLBACK}"

id="C-$(date -u +%Y-%m-%d)-$(openssl rand -hex 4 2>/dev/null || printf '%08x' "$RANDOM$RANDOM")"

exec bash "$PLUGIN_ROOT/scripts/emit-sentinel.sh" open "$id" "$DELIVERABLE"
