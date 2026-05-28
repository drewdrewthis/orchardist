#!/usr/bin/env bats
# Tests for emit-sentinel.sh — the single source of truth for the sentinel
# JSON shape, shared by /open-contract, /close-contract, and the SessionStart
# auto-open hook. If this script renders something the fold can't parse, every
# caller breaks; if escaping is wrong, agents who include quotes or backslashes
# in statements/reasons produce broken jsonl. Direct coverage here is what
# keeps the three callers honest.

setup() {
  SCRIPT="$BATS_TEST_DIRNAME/emit-sentinel.sh"
}

# _field <json-line> <key> — extract a top-level string field via python (jq
# would also work; python keeps parity with the rest of the bats suite).
_field() {
  printf '%s' "$1" | python3 -c "
import json, sys
print(json.loads(sys.stdin.read().strip()).get('$2', ''))
"
}

# ---- open ------------------------------------------------------------------

@test "open: emits a well-formed sentinel with the expected fields" {
  run bash "$SCRIPT" open "C-2026-05-28-deadbeef" "ship the X refactor"
  [ "$status" -eq 0 ]
  [ "${#lines[@]}" -eq 1 ]
  [ "$(_field "$output" orchard_contract)" = "open" ]
  [ "$(_field "$output" id)" = "C-2026-05-28-deadbeef" ]
  [ "$(_field "$output" statement)" = "ship the X refactor" ]
  [[ "$(_field "$output" ts)" =~ ^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z$ ]]
}

# ---- close -----------------------------------------------------------------

@test "close: emits a well-formed sentinel with reason (not statement)" {
  run bash "$SCRIPT" close "C-2026-05-28-deadbeef" "delivered: PR merged at sha abc123"
  [ "$status" -eq 0 ]
  [ "$(_field "$output" orchard_contract)" = "close" ]
  [ "$(_field "$output" reason)" = "delivered: PR merged at sha abc123" ]
  [ -z "$(_field "$output" statement)" ]   # close uses `reason`, not `statement`
}

# ---- escaping --------------------------------------------------------------

@test "escapes embedded double-quotes in the body without breaking JSON" {
  # An agent who quotes evidence inside a delivered reason must not produce
  # broken jsonl — the script's job is to be the seatbelt.
  run bash "$SCRIPT" close "C-X" 'delivered: "all tests green" per CI'
  [ "$status" -eq 0 ]
  [ "$(_field "$output" reason)" = 'delivered: "all tests green" per CI' ]
}

@test "escapes embedded backslashes in the body without breaking JSON" {
  run bash "$SCRIPT" open "C-X" 'path is c:\foo\bar'
  [ "$status" -eq 0 ]
  [ "$(_field "$output" statement)" = 'path is c:\foo\bar' ]
}

# ---- usage errors ----------------------------------------------------------

@test "rejects an unknown verb with a non-zero exit and stderr usage hint" {
  run bash "$SCRIPT" reopen "C-X" "anything"
  [ "$status" -ne 0 ]
  [[ "$output" == *"unknown verb"* ]]
}

@test "rejects missing arguments with a non-zero exit and usage line" {
  run bash "$SCRIPT" open
  [ "$status" -ne 0 ]
  [[ "$output" == *"usage:"* ]]
}

# ---- fold compatibility ----------------------------------------------------

@test "an emitted open sentinel is picked up by the fold via the strings path" {
  # The whole point of the script: every emit shape lands in some jsonl string
  # field (tool_result.content or attachment.stdout); the fold's
  # strings-via-fromjson must extract it.
  run bash "$SCRIPT" open "C-FOLDABLE" "a statement"
  [ "$status" -eq 0 ]
  emitted="$output"
  emitted_escaped="${emitted//\\/\\\\}"
  emitted_escaped="${emitted_escaped//\"/\\\"}"

  jsonl="$(mktemp)"
  printf '{"type":"user","message":{"role":"user","content":[{"type":"tool_result","content":"%s"}]}}\n' \
    "$emitted_escaped" > "$jsonl"

  fold_out=$(bash "$BATS_TEST_DIRNAME/fold-contracts.sh" "$jsonl")
  rm -f "$jsonl"

  [[ "$fold_out" == *"C-FOLDABLE"* ]]
  [[ "$fold_out" == *"a statement"* ]]
}
