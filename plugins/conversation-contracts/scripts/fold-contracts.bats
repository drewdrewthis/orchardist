#!/usr/bin/env bats
# Tests for fold-contracts.sh — specifically the --auto mode the skills use.
#
# --auto exists because real Claude Code does NOT export CLAUDE_SESSION_ID to
# skill subprocesses (SDK/--print in particular), so the older `--session
# "$CLAUDE_SESSION_ID"` recipe ran blind in production. --auto resolves the
# session jsonl from $PWD's encoding + the newest jsonl in that directory.
# (Explicit-path and --session modes are exercised transitively by on-stop.bats.)

setup() {
  SCRIPT="$BATS_TEST_DIRNAME/fold-contracts.sh"
  # Resolve to the physical path so tests see the same encoding the fold uses
  # (macOS's /var → /private/var symlink would otherwise produce mismatching
  # projects-dir lookups).
  TMP_HOME="$(cd -P -- "$(mktemp -d)" && pwd -P)"
  CWD="$TMP_HOME/work"
  mkdir -p "$CWD"
  ENCODED_CWD=$(printf '%s' "$CWD" | tr '/.' '--')
  PROJECTS_DIR="$TMP_HOME/.claude/projects/$ENCODED_CWD"
  mkdir -p "$PROJECTS_DIR"
}

teardown() {
  rm -rf "$TMP_HOME"
}

# _open_line / _close_line — sentinel embedded in a tool_result content string
# (the shape the harness writes when a skill echoes via emit-sentinel.sh).
_open_line() {
  local inner="{\\\"orchard_contract\\\":\\\"open\\\",\\\"id\\\":\\\"$1\\\",\\\"statement\\\":\\\"$2\\\",\\\"ts\\\":\\\"2026-05-28T10:00:00Z\\\"}"
  printf '{"type":"user","message":{"role":"user","content":[{"type":"tool_result","content":"%s"}]}}' "$inner"
}

_run_auto() {
  # Invoke the script with $PWD=$CWD and $HOME overridden so the encoded-cwd
  # lookup resolves to $PROJECTS_DIR — mirroring how a skill would call it.
  run env HOME="$TMP_HOME" \
      bash -c "cd '$CWD' && bash '$SCRIPT' --auto"
}

@test "--auto picks the newest jsonl under encoded-cwd and folds it" {
  printf '%s\n' "$(_open_line "C-AUTO-1" "the only session")" > "$PROJECTS_DIR/s-1.jsonl"
  _run_auto
  [ "$status" -eq 0 ]
  [[ "$output" == *"C-AUTO-1"* ]]
  [[ "$output" == *"the only session"* ]]
}

@test "--auto chooses the most recently modified jsonl when multiple sessions exist" {
  # Older session (closed open).
  printf '%s\n%s\n' \
    "$(_open_line "C-OLD" "older — should be ignored")" \
    "{\"type\":\"user\",\"message\":{\"role\":\"user\",\"content\":[{\"type\":\"tool_result\",\"content\":\"{\\\"orchard_contract\\\":\\\"close\\\",\\\"id\\\":\\\"C-OLD\\\",\\\"reason\\\":\\\"d\\\",\\\"ts\\\":\\\"t\\\"}\"}]}}" \
    > "$PROJECTS_DIR/s-old.jsonl"
  # Bump older's mtime back; current session is newer.
  touch -t 202001010000 "$PROJECTS_DIR/s-old.jsonl"
  printf '%s\n' "$(_open_line "C-CURRENT" "this session's open")" > "$PROJECTS_DIR/s-current.jsonl"

  _run_auto
  [[ "$output" == *"C-CURRENT"* ]]
  [[ "$output" != *"C-OLD"* ]]
}

@test "--auto gracefully degrades when no jsonl exists for this cwd" {
  # No jsonl was written.
  _run_auto
  [ "$status" -eq 0 ]
  [ -z "$output" ]
}

@test "--auto resolves symlinked cwd to its physical path before encoding" {
  # The trigger: on macOS, /var is a symlink to /private/var. A session whose
  # cwd reports /var/folders/... has its jsonl written under
  # ~/.claude/projects/-private-var-folders-... — the encoded RESOLVED path.
  # If --auto encodes $PWD directly (the symlinked /var/... form), it misses
  # the projects dir entirely and returns empty. Bug 1 from PR #666 install
  # verification.
  printf '%s\n' "$(_open_line "C-RESOLVED" "resolved-path open")" > "$PROJECTS_DIR/s-resolved.jsonl"
  # Symlink a /tmp/<rand> dir at the SAME real path as $CWD — so $PWD-as-string
  # differs from `pwd -P` but they encode to the same projects dir.
  symlink="$TMP_HOME/symlinked-cwd"
  ln -s "$CWD" "$symlink"
  run env HOME="$TMP_HOME" \
      bash -c "cd '$symlink' && bash '$SCRIPT' --auto"
  rm -f "$symlink"
  [ "$status" -eq 0 ]
  [[ "$output" == *"C-RESOLVED"* ]]
}

@test "fold tolerates trailing text after a sentinel in a tool_result.content string" {
  # The trigger: a skill recipe wrote `bash emit-sentinel.sh open ... && echo
  # "Opened contract $id"` which made tool_result.content the multi-line string
  # `<sentinel>\nOpened contract C-...`. The fold's plain `fromjson?` failed
  # on the trailing text and silently dropped the sentinel. Bug 2 from PR #666
  # install verification.
  #
  # Python builds the jsonl fixture so the embedded newline in the content
  # string is correctly JSON-escaped (a literal \n inside the JSON string,
  # which decodes to a real newline — the production shape).
  python3 - "$PROJECTS_DIR/s-trailing.jsonl" <<'PY'
import json, sys
sentinel = '{"orchard_contract":"open","id":"C-TRAIL","statement":"with trailing text","ts":"t"}'
content = sentinel + "\nOpened contract C-TRAIL"
line = {"type":"user","message":{"role":"user","content":[{"type":"tool_result","content":content}]}}
open(sys.argv[1], "w").write(json.dumps(line) + "\n")
PY

  _run_auto
  [ "$status" -eq 0 ]
  [[ "$output" == *"C-TRAIL"* ]]
  [[ "$output" == *"with trailing text"* ]]
}

@test "--auto picks up an explicit cwd argument when given" {
  printf '%s\n' "$(_open_line "C-ELSEWHERE" "named-cwd path")" > "$PROJECTS_DIR/s-elsewhere.jsonl"
  # Call from a DIFFERENT cwd but pass $CWD explicitly.
  other=$(mktemp -d)
  run env HOME="$TMP_HOME" \
      bash -c "cd '$other' && bash '$SCRIPT' --auto '$CWD'"
  rm -rf "$other"
  [[ "$output" == *"C-ELSEWHERE"* ]]
}
