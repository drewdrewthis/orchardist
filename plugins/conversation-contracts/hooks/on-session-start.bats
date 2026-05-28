#!/usr/bin/env bats
# Tests for on-session-start.sh — the SessionStart hook.
#
# The hook writes one `orchard_contract` open sentinel to stdout. The Claude
# Code harness records hook stdout in the session jsonl as a `hook_success`
# attachment whose `stdout` field is the literal string we emit; fold-contracts.sh
# extracts the sentinel via its strings-via-fromjson path.
#
# v0.10.0: the conversation contract's statement is read from
# references/conversation-contract-statement.md (the discipline gateway), with
# a fallback to the minimal closure-deliverable string when the file is missing.

setup() {
  HOOK="$BATS_TEST_DIRNAME/on-session-start.sh"
  # Real Claude Code sets CLAUDE_PLUGIN_ROOT so the hook can `exec` the shared
  # emit-sentinel.sh; mirror that in the test environment.
  export CLAUDE_PLUGIN_ROOT="$(cd "$BATS_TEST_DIRNAME/.." && pwd)"
  STATEMENT_FILE="$CLAUDE_PLUGIN_ROOT/references/conversation-contract-statement.md"
  FALLBACK="user agrees conversation has come to a close and there are no loose ends"
  # The expected statement is the file collapsed to one line (matching the hook's
  # tr-and-sed normalization).
  if [ -r "$STATEMENT_FILE" ]; then
    DELIVERABLE=$(tr '\n' ' ' < "$STATEMENT_FILE" | sed 's/  */ /g; s/ *$//')
  else
    DELIVERABLE="$FALLBACK"
  fi
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

@test "the sentinel's statement is read from the statement file" {
  run bash "$HOOK"
  [ "$status" -eq 0 ]
  [ "$(_field "$output" statement)" = "$DELIVERABLE" ]
  # The statement file should reference /i-am-done as the close gate.
  [[ "$DELIVERABLE" == *"/i-am-done"* ]]
}

@test "the sentinel id has the C-YYYY-MM-DD-<8hex> shape" {
  run bash "$HOOK"
  [ "$status" -eq 0 ]
  id="$(_field "$output" id)"
  [[ "$id" =~ ^C-[0-9]{4}-[0-9]{2}-[0-9]{2}-[a-f0-9]{8}$ ]]
}

@test "falls back to the minimal deliverable string when the statement file is missing" {
  tmp_root="$(mktemp -d)"
  cp -r "$CLAUDE_PLUGIN_ROOT/scripts" "$tmp_root/"
  cp -r "$CLAUDE_PLUGIN_ROOT/hooks" "$tmp_root/"
  # No references/ in the temp root — simulates a missing statement file.
  CLAUDE_PLUGIN_ROOT="$tmp_root" run bash "$tmp_root/hooks/on-session-start.sh"
  [ "$status" -eq 0 ]
  [ "$(_field "$output" statement)" = "$FALLBACK" ]
  rm -rf "$tmp_root"
}

@test "the fold script picks up the sentinel when it appears in a stdout-attachment line" {
  # Real Claude Code records the hook's stdout in the jsonl as a hook_success
  # attachment whose `stdout` field is the literal string the hook printed.
  # The fold's strings-via-fromjson path must find the sentinel nested there.
  run bash "$HOOK"
  [ "$status" -eq 0 ]
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
  # Fold output truncates long statements at the first sentence; just verify
  # the conversation-contract closure deliverable prefix appears.
  [[ "$fold_out" == *"user agrees conversation has come to a close"* ]]
}
