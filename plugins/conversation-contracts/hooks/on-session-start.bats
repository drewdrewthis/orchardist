#!/usr/bin/env bats
# Tests for on-session-start.sh — the SessionStart hook.
#
# The hook writes one `orchard_contract` open sentinel to stdout. The Claude
# Code harness records hook stdout in the session jsonl as a `hook_success`
# attachment whose `stdout` field is the literal string we emit; fold-contracts.sh
# extracts the sentinel via its strings-via-fromjson path.
#
# The conversation contract's statement is read from
# references/conversation-contract-statement.md (the discipline gateway), with
# a fallback to the minimal closure-deliverable string when the file is missing.

setup() {
  HOOK="$BATS_TEST_DIRNAME/on-session-start.sh"
  # Real Claude Code sets CLAUDE_PLUGIN_ROOT so the hook can `exec` the shared
  # emit-sentinel.sh; mirror that in the test environment.
  export CLAUDE_PLUGIN_ROOT="$(cd "$BATS_TEST_DIRNAME/.." && pwd)"
  STATEMENT_FILE="$CLAUDE_PLUGIN_ROOT/references/conversation-contract-statement.md"
  FALLBACK="user agrees conversation has come to a close and there are no loose ends"
  # Compute the expected statement via the same shared collapse helper the
  # hook uses — this prevents the test and the hook from co-drifting if the
  # normalization shape ever changes (e.g. tabs, CRLF).
  if [ -r "$STATEMENT_FILE" ]; then
    DELIVERABLE=$(bash "$CLAUDE_PLUGIN_ROOT/scripts/collapse-statement.sh" "$STATEMENT_FILE")
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

@test "falls back when CLAUDE_PLUGIN_ROOT is unset (self-locates via BASH_SOURCE)" {
  # The hook must work end-to-end even without the env var — old installs,
  # test harnesses, smoke checks. It should resolve PLUGIN_ROOT from its own
  # path and still emit a well-formed sentinel.
  unset CLAUDE_PLUGIN_ROOT
  run bash "$HOOK"
  [ "$status" -eq 0 ]
  [ "$(_field "$output" orchard_contract)" = "open" ]
  # Statement file lives next to the hook, so the gateway statement still loads.
  [[ "$(_field "$output" statement)" == *"/i-am-done"* ]]
}

@test "BASH_SOURCE fallback smoke-tests a symlinked plugin install" {
  # SMOKE check, not a `cd -P` regression lock — I tried twice to write one
  # and failed because file reads through symlinks are kernel-transparent:
  # both `cd` and `cd -P` resolve to the same statement file when the
  # symlink points at the real install. (Two parallel-trees variants both
  # passed without `-P` because the read followed the same physical inode.)
  # The `cd -P` in the hook is defensive — it makes `$PLUGIN_ROOT` the
  # physical path for any future use that compares the value, but no
  # current code path is sensitive to that.
  #
  # This test verifies the smoke path: a symlinked install can still load
  # the statement and emit a well-formed sentinel. It does NOT regression-
  # lock `cd -P`. If you drop `-P` from the hook, this test will still
  # pass — that's by design.
  tmp_root="$(mktemp -d)"
  cp -r "$CLAUDE_PLUGIN_ROOT/scripts" "$tmp_root/"
  cp -r "$CLAUDE_PLUGIN_ROOT/hooks" "$tmp_root/"
  cp -r "$CLAUDE_PLUGIN_ROOT/references" "$tmp_root/"

  link_root="$(mktemp -d)/plugin-link"
  ln -s "$tmp_root" "$link_root"
  unset CLAUDE_PLUGIN_ROOT
  run bash "$link_root/hooks/on-session-start.sh"
  [ "$status" -eq 0 ]
  [ "$(_field "$output" orchard_contract)" = "open" ]
  [[ "$(_field "$output" statement)" == *"/i-am-done"* ]]

  rm -rf "$tmp_root" "$(dirname "$link_root")"
}

@test "collapses a multi-line statement file into a single sentinel line" {
  tmp_root="$(mktemp -d)"
  cp -r "$CLAUDE_PLUGIN_ROOT/scripts" "$tmp_root/"
  cp -r "$CLAUDE_PLUGIN_ROOT/hooks" "$tmp_root/"
  mkdir -p "$tmp_root/references"
  # Write a 3-line statement; the hook should collapse runs of whitespace to
  # a single space and strip the trailing newline.
  printf 'line one\nline two\n   line three\n' \
    > "$tmp_root/references/conversation-contract-statement.md"
  CLAUDE_PLUGIN_ROOT="$tmp_root" run bash "$tmp_root/hooks/on-session-start.sh"
  [ "$status" -eq 0 ]
  [ "$(_field "$output" statement)" = "line one line two line three" ]
  rm -rf "$tmp_root"
}

@test "statement file is single-line prose (no markdown structural starters)" {
  # Lock the shape: the hook collapses ANY content to one line, so a stray
  # `#` heading, `- ` list item, or `1.` numbered item would concatenate
  # inline with the prose and ship an ugly JSON string into every session
  # jsonl. If you want structured content later, teach the hook (or the
  # collapse helper) to skip / strip it first.
  #
  # Real single-line check: count lines with content (not trailing-newline
  # sensitive). `wc -l` would lie on a "foo\nbar" file (1 newline = 1 line).
  non_empty_lines=$(awk 'NF > 0 {n++} END {print n+0}' "$STATEMENT_FILE")
  [ "$non_empty_lines" -eq 1 ]
  # No common markdown structural starters at line start: heading, bullet,
  # asterisk-list, blockquote, table, numbered-list, fenced-code opener.
  ! grep -qE '^(#|-|\*|>|\||[0-9]+\.|```)' "$STATEMENT_FILE"
}

@test "emits empty stdout in ephemeral --print mode (CLAUDE_CODE_ENTRYPOINT=sdk-cli)" {
  # `claude --print` and SDK invocations have no interactive user to consent
  # to a "user agrees conversation has come to a close" contract; auto-opening
  # one mangles the agent's final reply into close-confirmation text. The
  # hook MUST skip silently in that mode (empty stdout, exit 0). Real-runtime
  # value: `claude --print` exports CLAUDE_CODE_ENTRYPOINT=sdk-cli on
  # Claude Code 2.1.x.
  CLAUDE_CODE_ENTRYPOINT=sdk-cli run bash "$HOOK"
  [ "$status" -eq 0 ]
  [ -z "$output" ]
}

@test "still emits the sentinel for interactive CLI (CLAUDE_CODE_ENTRYPOINT=cli)" {
  # The denylist is intentionally narrow: only known ephemeral values are
  # skipped. Interactive sessions (entrypoint=cli) must continue to auto-open
  # the conversation contract — that's the discipline gateway.
  CLAUDE_CODE_ENTRYPOINT=cli run bash "$HOOK"
  [ "$status" -eq 0 ]
  [ "${#lines[@]}" -eq 1 ]
  [ "$(_field "$output" orchard_contract)" = "open" ]
  [[ "$(_field "$output" statement)" == *"/i-am-done"* ]]
}

@test "still emits the sentinel for any future entrypoint not on the denylist" {
  # If Anthropic adds a new interactive entrypoint (cli-tui, vscode-extension,
  # ide, ...), the hook defaults to emitting. The denylist must be widened by
  # design choice, never by accident. This pins that property.
  CLAUDE_CODE_ENTRYPOINT=some-future-interactive-mode run bash "$HOOK"
  [ "$status" -eq 0 ]
  [ "${#lines[@]}" -eq 1 ]
  [ "$(_field "$output" orchard_contract)" = "open" ]
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
  # Equality, not substring. Fold output shape is exactly `- <id>: <statement>`
  # (scripts/fold-contracts.sh:115-116). Substring would let trailing or
  # leading garbage hide regressions. If this ever fails: fix fold-contracts.sh
  # or the statement file — do NOT relax this assertion.
  [ "$fold_out" = "- $id: $DELIVERABLE" ]
}
