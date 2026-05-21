#!/usr/bin/env bash
# on-prompt-submit.sh — UserPromptSubmit hook for the conversation-contracts plugin.
#
# Called by Claude Code on every UserPromptSubmit event. Opens the
# conversation contract for the current session via the MCP open_contract
# tool. The ContractFold deduplicates by (ownerSessionId, deliverable) so
# only the first invocation per session creates a Contract record.
#
# No state is written to ${CLAUDE_PLUGIN_DATA}. Idempotency is entirely
# derived from the fold dedup.
#
# Inputs:
#   stdin                 — Claude Code passes a JSON payload with session_id,
#                           cwd, transcript_path, hook_event_name, prompt.
#   CLAUDE_PLUGIN_ROOT    — root of this plugin (to locate the mcp binary)
#   CONTRACTS_MCP_BIN     — optional: override path to the mcp binary (for testing)
#   CLAUDE_SESSION_ID     — optional: skip the stdin payload parse and use the
#                           env var directly (tests use this path)

set -uo pipefail

# The fixed deliverable for auto-opened conversation contracts.
# This string contains no JSON-special characters so it can be safely embedded.
DELIVERABLE="user agrees conversation has come to a close and there are no loose ends"

# Capture the hook payload (Claude Code passes it on stdin). When stdin is
# not a JSON payload (test harness fires the hook directly via env vars), the
# payload string stays empty and we fall back to env-derived values.
payload=""
if [ ! -t 0 ]; then
    payload=$(cat)
fi

# Derive session_id and cwd. The MCP server consumes CLAUDE_SESSION_ID +
# PWD; the on-disk path is ~/.claude/projects/<encoded-cwd>/<session-uuid>.jsonl.
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

# Locate the MCP binary. Require an executable file in both branches —
# without the -x check we'd silently exec a stale or non-executable path.
if [[ -n "${CONTRACTS_MCP_BIN:-}" ]] && [[ -x "${CONTRACTS_MCP_BIN}" ]]; then
    MCP_BIN="${CONTRACTS_MCP_BIN}"
elif [[ -n "${CLAUDE_PLUGIN_ROOT:-}" ]] && [[ -x "${CLAUDE_PLUGIN_ROOT}/bin/contracts-mcp" ]]; then
    MCP_BIN="${CLAUDE_PLUGIN_ROOT}/bin/contracts-mcp"
else
    # Binary not found. Warn on stderr so the developer knows; still exit 0
    # so the missing build does not block the user's prompt.
    echo "conversation-contracts plugin: MCP binary not found at ${CLAUDE_PLUGIN_ROOT:-<CLAUDE_PLUGIN_ROOT unset>}/bin/contracts-mcp; conversation contract was NOT opened. Build with: make plugins-contracts-mcp" >&2
    exit 0
fi

# Build the MCP initialize + tools/call payload.
# The MCP server reads JSON-RPC lines from stdin and responds on stdout.
# The deliverable string has no JSON-special characters; embed directly.
INIT='{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{}}}'
NOTIFY='{"jsonrpc":"2.0","method":"notifications/initialized"}'
CALL="{\"jsonrpc\":\"2.0\",\"id\":2,\"method\":\"tools/call\",\"params\":{\"name\":\"open_contract\",\"arguments\":{\"deliverable\":\"${DELIVERABLE}\"}}}"

# Pipe the three messages to the MCP binary. We swallow stdout (the
# observable side effect is the tool_use event written to the session
# jsonl by handleOpenContract) but capture stderr so a failure is visible
# on the host's hook log instead of disappearing into /dev/null. Pass
# session_id + cwd via env so the MCP server can resolve the target jsonl
# regardless of the parent shell's env.
mcp_stderr=$(
    printf '%s\n%s\n%s\n' "${INIT}" "${NOTIFY}" "${CALL}" \
        | env CLAUDE_SESSION_ID="$SESSION_ID" PWD="$HOOK_CWD" HOME="$HOME" "${MCP_BIN}" 2>&1 >/dev/null
)
mcp_rc=$?
if [ "$mcp_rc" -ne 0 ]; then
    echo "conversation-contracts plugin: MCP open_contract failed (exit ${mcp_rc}): ${mcp_stderr}" >&2
    # Still exit 0 so the user's prompt is not blocked by a contract
    # housekeeping failure — visibility, not blocking.
fi
exit 0
