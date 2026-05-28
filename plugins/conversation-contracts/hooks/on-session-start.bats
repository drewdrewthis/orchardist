#!/usr/bin/env bats
# Tests for on-session-start.sh — the SessionStart hook.
#
# The hook writes one `orchard_contract` open sentinel to stdout. The Claude
# Code harness records hook stdout in the session jsonl as a `hook_success`
# attachment whose `stdout` field is the literal string we emit; fold-contracts.sh
# extracts the sentinel via its strings-via-fromjson path. Stateless: no file
# IO, no path resolution, no idempotency check needed (SessionStart fires once).

setup() {
  HOOK="$BATS_TEST_DIRNAME/on-session-start.sh"
  DELIVERABLE="user agrees conversation has come to a close and there are no loose ends"
}

# _field <hook-stdout> <key> — parse the emitted sentinel and print the value.
_field() {
  printf '%s' "$1" | python3 -c "
import json, sys
print(json.loads(sys.stdin.read().strip()).get('$2', ''))
"
}

@test "emits exactly one orchard_contract open sentinel to stdout" {
  run bash "$HOOK"
  [ "$status" -eq 0 ]
  # Exactly one line of JSON. bats' `run` sets $lines to an array of stdout
  # lines (trailing newline stripped) — one element ⇒ one line emitted.
  [ "${#lines[@]}" -eq 1 ]
  [ "$(_field "$output" orchard_contract)" = "open" ]
}

@test "the sentinel's statement is the fixed deliverable" {
  run bash "$HOOK"
  [ "$(_field "$output" statement)" = "$DELIVERABLE" ]
}

@test "the sentinel id has the C-YYYY-MM-DD-<8hex> shape" {
  run bash "$HOOK"
  id="$(_field "$output" id)"
  [[ "$id" =~ ^C-[0-9]{4}-[0-9]{2}-[0-9]{2}-[a-f0-9]{8}$ ]]
}

@test "source is auto-session-start (distinguishable from skill-opened contracts)" {
  run bash "$HOOK"
  [ "$(_field "$output" source)" = "auto-session-start" ]
}

@test "the fold script picks up the sentinel when it appears in a stdout-attachment line" {
  # Real Claude Code records the hook's stdout in the jsonl as a hook_success
  # attachment whose `stdout` field is the literal string the hook printed.
  # The fold's strings-via-fromjson path must find the sentinel nested there.
  run bash "$HOOK"
  hook_stdout="$output"
  hook_stdout_escaped="${hook_stdout//\\/\\\\}"
  hook_stdout_escaped="${hook_stdout_escaped//\"/\\\"}"
  hook_stdout_escaped="${hook_stdout_escaped%$'\n'}"  # strip trailing newline

  jsonl="$(mktemp)"
  # Match the real-runtime shape: an attachment record where .attachment.stdout
  # holds the literal sentinel JSON string.
  printf '{"type":"attachment","attachment":{"type":"hook_success","hookEvent":"SessionStart","stdout":"%s"}}\n' \
    "$hook_stdout_escaped" > "$jsonl"

  fold_out=$(bash "$BATS_TEST_DIRNAME/../scripts/fold-contracts.sh" "$jsonl")
  rm -f "$jsonl"

  id="$(_field "$hook_stdout" id)"
  [[ "$fold_out" == *"$id"* ]]
  [[ "$fold_out" == *"$DELIVERABLE"* ]]
}
