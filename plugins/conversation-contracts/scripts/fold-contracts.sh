#!/usr/bin/env bash
# fold-contracts.sh — the single source of fold truth for conversation-contracts.
#
# Reads a session jsonl, folds the `orchard_contract` open/close sentinels, and
# prints the OPEN contracts (one per line, "- <id>: <statement>"). A contract is
# OPEN iff it has an open sentinel and no matching close sentinel (by id).
#
# The sentinel is a JSON object emitted into the session jsonl by:
#   - /open-contract and /close-contract skills (via a Bash echo, which lands in
#     the tool_result's stdout AND the assistant message content), and
#   - the on-prompt-submit.sh hook (auto-open of the conversation contract,
#     appended directly as a self-contained line).
# Because the sentinel can appear in more than one place per record, we extract
# every object carrying the `orchard_contract` key from anywhere in each line
# and dedupe before folding.
#
# Sentinel shape:
#   {"orchard_contract":"open","id":"C-YYYY-MM-DD-xxxxxxxx","statement":"...","ts":"..."}
#   {"orchard_contract":"close","id":"<same-id>","reason":"...","ts":"..."}
#
# Stateless by design: the jsonl IS the state. No sidecar, no checkpoint, no
# resident process. A fixed-string grep gates the (more expensive) jq pass so
# the common "no contracts" case costs almost nothing.
#
# The fold is SET-BASED, not sequence-based: a close cancels an open with the
# same id regardless of their order or timestamps in the file. For the
# append-only contract model this is intended — a close for an unknown id is a
# harmless no-op, and ids are random hex so reuse does not occur in practice.
#
# Usage:   fold-contracts.sh <session-jsonl-path>
# Output:  one line per open contract, "- <id>: <statement>"; empty if none.
# Exit:    0 always (missing/empty jsonl degrades to no output).

set -uo pipefail

jsonl="${1:-}"
[ -n "$jsonl" ] || exit 0
[ -f "$jsonl" ] || exit 0

# Fast gate: if the sentinel token is absent, there is nothing to fold.
grep -Fq 'orchard_contract' "$jsonl" 2>/dev/null || exit 0

# Pull every sentinel from anywhere in each record. A sentinel can appear two
# ways: (1) as a bare nested object — the auto-open hook appends one as a
# self-contained line; (2) as an escaped JSON *string* inside a tool_result's
# `content`/`stdout` — that is how a skill's `Bash(echo '<json>')` lands. So we
# union both extraction paths: recurse to objects carrying the key, AND recurse
# to strings, fromjson them, and keep the ones that parse to a sentinel object.
# Then dedupe (the same sentinel surfaces in both stdout and message content)
# and fold open-minus-close by id.
grep -F 'orchard_contract' "$jsonl" 2>/dev/null \
  | jq -c '
      (fromjson? // .) as $line
      | (
          [$line | .. | objects | select(.orchard_contract)]
          + [$line | .. | strings | (fromjson? // empty) | select(type == "object" and .orchard_contract)]
        )
      | .[]
    ' 2>/dev/null \
  | jq -sc '
      map(select(
        ((.orchard_contract == "open") or (.orchard_contract == "close"))
        and ((.id | type) == "string")
        and ((.id | length) > 0)
      ))
      | unique
    ' 2>/dev/null \
  | jq -r '
      group_by(.id)
      | map({
          id:        .[0].id,
          statement: ([.[] | select(.orchard_contract == "open") | .statement] | last),
          opened:    (any(.[]; .orchard_contract == "open")),
          closed:    (any(.[]; .orchard_contract == "close"))
        })
      | map(select(.opened and (.closed | not)))
      | .[]
      | "- " + .id + ": " + (.statement // "(no statement)")
    ' 2>/dev/null || true
