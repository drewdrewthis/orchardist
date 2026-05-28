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
#   fold-contracts.sh <session-jsonl-path>   # explicit path
#   fold-contracts.sh --session <id> [<cwd>] # scan projects dirs for <id>.jsonl
#                                            # (cwd arg accepted for backcompat
#                                            #  but ignored — see Bug 3 below)
#   fold-contracts.sh --auto [<cwd>]         # newest jsonl under encoded-cwd
# Output: one line per open contract; empty if none. Exit 0 always.
#
# Path resolution: Claude Code encodes the SESSION'S STARTUP cwd into the
# projects dir name, not the current $PWD. If the user `cd`s after the session
# starts (which is the common case in worktree-driven workflows), $PWD diverges
# from the encoded path. Prior fold versions encoded $PWD and missed the jsonl
# entirely — Stop blocks fired and silently allowed because the fold returned
# empty (Bug 3, surfaced in PR #666 live verification).
#
# Fix: --session scans every projects subdir for <id>.jsonl rather than
# encoding $PWD. The session id is globally unique, so the scan is correct
# regardless of where the user has cd'd. The optional <cwd> argument is still
# accepted for backward compatibility with old callers but is now ignored.

set -uo pipefail

_resolve_session_jsonl() {
  # Given a cwd, print the newest .jsonl in the corresponding projects subdir.
  # Empty if no match. Cwd encoding mirrors Claude Code's: / and . → -.
  #
  # The cwd must be the RESOLVED (physical) path, not the symlinked one Claude
  # Code received via $PWD — on macOS, `/var` is a symlink to `/private/var`,
  # and the harness encodes the resolved `/private/var/...` path into the
  # projects dir name, while a script's $PWD may still report `/var/...`. We
  # resolve via `cd -P` so the encoding matches what the harness wrote.
  local cwd="$1"
  [ -n "$cwd" ] || return 0
  local resolved
  resolved=$(cd -P -- "$cwd" 2>/dev/null && pwd -P) || resolved="$cwd"
  local root="${CLAUDE_PROJECTS_DIR:-$HOME/.claude/projects}"
  local enc; enc=$(printf '%s' "$resolved" | tr '/.' '--')
  local dir="$root/$enc"
  [ -d "$dir" ] || return 0
  # ls -t sorts by mtime descending; ignore failures if no jsonls match.
  ls -t "$dir"/*.jsonl 2>/dev/null | head -1
}

_find_jsonl_by_sid() {
  # Scan every projects subdir for <sid>.jsonl. Print the first match's
  # absolute path; empty if not found. Session ids are globally unique, so
  # at most one match exists across all projects subdirs.
  local sid="$1"
  [ -n "$sid" ] || return 0
  local root="${CLAUDE_PROJECTS_DIR:-$HOME/.claude/projects}"
  [ -d "$root" ] || return 0
  # find ... -path is portable across macOS and Linux; -quit on first match.
  # Using -name keeps the scan cheap (one stat per subdir's <sid>.jsonl).
  find "$root" -maxdepth 2 -type f -name "$sid.jsonl" -print -quit 2>/dev/null
}

if [ "${1:-}" = "--session" ]; then
  sid="${2:-}"
  [ -n "$sid" ] || exit 0
  # The third arg (cwd) is accepted for backcompat but unused — see Bug 3 fix.
  jsonl=$(_find_jsonl_by_sid "$sid")
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
#
# For the strings path, split on \n and try fromjson on each line — tolerates
# bash recipes that chained `&& echo "..."` after emit-sentinel.sh and ended
# up writing `<sentinel>\nhuman text` into a single tool_result.content string.
# The sentinel is always one line, so per-line fromjson recovers it.
grep -F 'orchard_contract' "$jsonl" 2>/dev/null \
  | jq -c '
      ( [ .. | objects | select(.orchard_contract) ]
      + [ .. | strings
            | split("\n") | .[]
            | (fromjson? // empty)
            | select(type == "object" and .orchard_contract) ]
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
