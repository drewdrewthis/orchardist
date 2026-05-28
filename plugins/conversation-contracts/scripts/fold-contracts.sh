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
#   fold-contracts.sh --auto [<cwd>]           # newest jsonl under encoded-cwd
# Output: one line per open contract; empty if none. Exit 0 always.
#
# Prefer --auto in skills: real Claude Code does NOT export CLAUDE_SESSION_ID
# to user-driven skill subprocesses (SDK/--print mode in particular leaves it
# unset), so any skill that calls `--session "$CLAUDE_SESSION_ID"` is blind to
# the current session. --auto resolves the path from $PWD's encoding and picks
# the newest jsonl in that directory — the current session by definition,
# because we are running inside it as it writes to it.

set -uo pipefail

_resolve_session_jsonl() {
  # Given a cwd, print the newest .jsonl in the corresponding projects subdir.
  # Empty if no match. Cwd encoding mirrors Claude Code's: / and . → -.
  local cwd="$1"
  [ -n "$cwd" ] || return 0
  local root="${CLAUDE_PROJECTS_DIR:-$HOME/.claude/projects}"
  local enc; enc=$(printf '%s' "$cwd" | tr '/.' '--')
  local dir="$root/$enc"
  [ -d "$dir" ] || return 0
  # ls -t sorts by mtime descending; ignore failures if no jsonls match.
  ls -t "$dir"/*.jsonl 2>/dev/null | head -1
}

if [ "${1:-}" = "--session" ]; then
  sid="${2:-}"
  cwd="${3:-${PWD:-}}"
  [ -n "$sid" ] && [ -n "$cwd" ] || exit 0
  root="${CLAUDE_PROJECTS_DIR:-$HOME/.claude/projects}"
  enc=$(printf '%s' "$cwd" | tr '/.' '--')
  jsonl="$root/$enc/$sid.jsonl"
elif [ "${1:-}" = "--auto" ]; then
  cwd="${2:-${PWD:-}}"
  jsonl=$(_resolve_session_jsonl "$cwd")
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
