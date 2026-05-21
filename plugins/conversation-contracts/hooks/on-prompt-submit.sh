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
# Environment variables consumed:
#   CLAUDE_SESSION_ID     — calling session UUID (required by MCP server)
#   HOME                  — user home dir (required by MCP server)
#   PWD                   — calling cwd (required by MCP server)
#   CLAUDE_PLUGIN_ROOT    — root of this plugin (to locate the mcp binary)
#   CONTRACTS_MCP_BIN     — optional: override path to the mcp binary (for testing)

set -euo pipefail

# The fixed deliverable for auto-opened conversation contracts.
# This string contains no JSON-special characters so it can be safely embedded.
DELIVERABLE="user agrees conversation has come to a close and there are no loose ends"

# Locate the MCP binary.
if [[ -n "${CONTRACTS_MCP_BIN:-}" ]]; then
    MCP_BIN="${CONTRACTS_MCP_BIN}"
elif [[ -n "${CLAUDE_PLUGIN_ROOT:-}" ]] && [[ -x "${CLAUDE_PLUGIN_ROOT}/mcp/conversation-contracts-mcp" ]]; then
    MCP_BIN="${CLAUDE_PLUGIN_ROOT}/mcp/conversation-contracts-mcp"
else
    # Binary not found. Exit silently so a missing build does not surface
    # a noisy error on every prompt.
    exit 0
fi

# Build the MCP initialize + tools/call payload.
# The MCP server reads JSON-RPC lines from stdin and responds on stdout.
# The deliverable string has no JSON-special characters; embed directly.
INIT='{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{}}}'
NOTIFY='{"jsonrpc":"2.0","method":"notifications/initialized"}'
CALL="{\"jsonrpc\":\"2.0\",\"id\":2,\"method\":\"tools/call\",\"params\":{\"name\":\"open_contract\",\"arguments\":{\"deliverable\":\"${DELIVERABLE}\"}}}"

# Pipe the three messages to the MCP binary. We discard stdout; all
# observable side effects are the tool_use event written to the session
# jsonl by the MCP server's handleOpenContract.
printf '%s\n%s\n%s\n' "${INIT}" "${NOTIFY}" "${CALL}" | "${MCP_BIN}" > /dev/null 2>&1 || true
