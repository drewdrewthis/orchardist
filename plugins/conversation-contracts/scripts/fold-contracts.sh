#!/usr/bin/env bash
# fold-contracts.sh — the single source of fold truth for conversation-contracts.
#
# Prints the OPEN contracts in a session jsonl, one per line ("- <id>: <stmt>").
# A contract is OPEN iff it has an `orchard_contract` open sentinel and no
# matching close sentinel (by id). The jsonl IS the store — no daemon, no
# sidecar, no resident process.
#
# The fold is SET-BASED, not sequence-based: a close cancels an open with the
# same id regardless of order/timestamp. For the append-only model this is
# intended — a close for an unknown id is a harmless no-op.
#
# Usage:
#   fold-contracts.sh <session-jsonl-path>     # explicit path
#   fold-contracts.sh --session <id> [<cwd>]   # resolve path from session id
# Output: one line per open contract; empty if none. Exit 0 always.

set -uo pipefail

if [ "${1:-}" = "--session" ]; then
  sid="${2:-}"
  cwd="${3:-${PWD:-}}"
  [ -n "$sid" ] && [ -n "$cwd" ] || exit 0
  root="${CLAUDE_PROJECTS_DIR:-$HOME/.claude/projects}"
  enc=$(printf '%s' "$cwd" | tr '/.' '--')
  jsonl="$root/$enc/$sid.jsonl"
else
  jsonl="${1:-}"
fi

[ -n "$jsonl" ] && [ -f "$jsonl" ] || exit 0
grep -Fq 'orchard_contract' "$jsonl" 2>/dev/null || exit 0

# A sentinel appears two ways: a bare nested object (legacy/test fixture form)
# or a JSON string in any string-valued field — the production shape, where the
# harness writes the sentinel both as a skill's `Bash echo` tool_result content
# and as the SessionStart hook's recorded stdout (a `hook_success` attachment).
# Union both extraction paths, keep only well-formed open/close sentinels with
# a non-empty string id, dedupe, then fold by id.
grep -F 'orchard_contract' "$jsonl" 2>/dev/null \
  | jq -c '
      ( [ .. | objects | select(.orchard_contract) ]
      + [ .. | strings | (fromjson? // empty) | select(type == "object" and .orchard_contract) ]
      ) | .[]
    ' 2>/dev/null \
  | jq -sc '
      map(select(
        (.orchard_contract == "open" or .orchard_contract == "close")
        and (.id | type) == "string" and (.id | length) > 0
      )) | unique
    ' 2>/dev/null \
  | jq -r '
      group_by(.id)
      | map(select(any(.[]; .orchard_contract == "open")
                   and (any(.[]; .orchard_contract == "close") | not)))
      | .[]
      | "- " + .[0].id + ": "
        + (([.[] | select(.orchard_contract == "open") | .statement] | last) // "(no statement)")
    ' 2>/dev/null || true
