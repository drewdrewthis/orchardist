#!/usr/bin/env bats
# Tests for on-stop.sh — the Stop hook.
#
# The hook folds the `orchard_contract` open/close sentinels out of the session
# jsonl (via ../scripts/fold-contracts.sh) and HARD-BLOCKS Stop while any
# contract is open, emitting {"decision":"block","reason":"..."}. With no open
# contract it emits nothing and exits 0 (Stop allowed).
#
# Each test execs the real on-stop.sh against a crafted session jsonl in a temp
# HOME — no daemon, no network, no mocking. Bash resets $PWD to its actual cwd
# on startup, so we run the hook from inside $WORK (cd in a subshell) and write
# the jsonl under the encoded-$WORK path the hook resolves from $PWD.

setup() {
  HOOK="$BATS_TEST_DIRNAME/on-stop.sh"
  PLUGIN_ROOT="$(cd "$BATS_TEST_DIRNAME/.." && pwd)"   # real Claude Code sets CLAUDE_PLUGIN_ROOT
  HOME_DIR="$(mktemp -d)"
  WORK="$HOME_DIR"                       # cwd == HOME keeps the encoding simple
  ROOT="$HOME_DIR/.claude/projects"
  DELIVERABLE="user agrees conversation has come to a close and there are no loose ends"
}

teardown() {
  rm -rf "$HOME_DIR"
}

# _encode encodes a cwd the way the hook does: '/' and '.' both become '-'.
_encode() { printf '%s' "$1" | tr '/.' '--'; }

# _write_jsonl <session-id> <line>... — writes the lines to the session jsonl
# under <ROOT>/<encoded-WORK>/<sid>.jsonl (honoring CLAUDE_PROJECTS_DIR if set).
_write_jsonl() {
  local sid="$1"; shift
  local root="${PROJECTS_ROOT:-$ROOT}"
  local cwd="${PROJECTS_CWD:-$WORK}"
  local dir="$root/$(_encode "$cwd")"
  mkdir -p "$dir"
  printf '%s\n' "$@" > "$dir/$sid.jsonl"
}

# _open_line / _close_line build a session jsonl line carrying the sentinel
# inside a tool_result content STRING — the shape a /open-contract Bash echo
# produces (escaped JSON nested in the transcript).
_open_line() {
  local inner="{\\\"orchard_contract\\\":\\\"open\\\",\\\"id\\\":\\\"$1\\\",\\\"statement\\\":\\\"$2\\\",\\\"ts\\\":\\\"2026-05-27T10:00:00Z\\\"}"
  printf '{"type":"user","message":{"role":"user","content":[{"type":"tool_result","content":"%s"}]}}' "$inner"
}
_close_line() {
  local inner="{\\\"orchard_contract\\\":\\\"close\\\",\\\"id\\\":\\\"$1\\\",\\\"reason\\\":\\\"$2\\\",\\\"ts\\\":\\\"2026-05-27T11:00:00Z\\\"}"
  printf '{"type":"user","message":{"role":"user","content":[{"type":"tool_result","content":"%s"}]}}' "$inner"
}

# _run_hook <session-id> — runs the Stop hook from $WORK with a Stop payload on
# stdin. Captures stdout into $output and the exit status into $status.
_run_hook() {
  local sid="$1"
  local payload="{\"hook_event_name\":\"Stop\",\"session_id\":\"$sid\"}"
  run env HOME="$HOME_DIR" CLAUDE_PLUGIN_ROOT="$PLUGIN_ROOT" \
      CLAUDE_PROJECTS_DIR="${CLAUDE_PROJECTS_DIR:-}" \
      bash -c "cd '$WORK' && printf '%s' '$payload' | bash '$HOOK'"
}

# ---- open with no close → block ---------------------------------------------

@test "open contract with no close blocks Stop and names the verbs" {
  local id="C-2026-05-27-aaaa1111"
  _write_jsonl "S-STOP-OPEN" "$(_open_line "$id" "ship the X refactor")"
  _run_hook "S-STOP-OPEN"

  [ -n "$output" ]
  [ "$(printf '%s' "$output" | python3 -c 'import json,sys; print(json.load(sys.stdin)["decision"])')" = "block" ]
  reason="$(printf '%s' "$output" | python3 -c 'import json,sys; print(json.load(sys.stdin)["reason"])')"
  [[ "$reason" == *"$id"* ]]
  [[ "$reason" == *"/close-contract"* ]]      # self-documenting: names the close verb
  [[ "$reason" == *"/my-contracts"* ]]        # ... and the list verb
}

# ---- open + matching close → allow ------------------------------------------

@test "open then matching close allows Stop (empty output)" {
  local id="C-2026-05-27-bbbb2222"
  _write_jsonl "S-STOP-CLOSED" \
    "$(_open_line "$id" "do the thing")" \
    "$(_close_line "$id" "delivered: done")"
  _run_hook "S-STOP-CLOSED"

  [ "$status" -eq 0 ]
  [ -z "$(printf '%s' "$output" | tr -d '[:space:]')" ]
}

# ---- two opens, one closed → block lists only the still-open one ------------

@test "partial close lists only the still-open contract" {
  _write_jsonl "S-STOP-PARTIAL" \
    "$(_open_line "C-FIRST" "first task")" \
    "$(_open_line "C-SECOND" "second task")" \
    "$(_close_line "C-FIRST" "delivered")"
  _run_hook "S-STOP-PARTIAL"

  reason="$(printf '%s' "$output" | python3 -c 'import json,sys; print(json.load(sys.stdin)["reason"])')"
  [[ "$reason" == *"C-SECOND"* ]]
  [[ "$reason" != *"C-FIRST"* ]]
}

# ---- auto-open conversation contract + skill-open both block (uniform fold) --

@test "auto-open conversation contract and skill-open both block" {
  # Two paths the fold must handle uniformly: (a) a bare self-contained
  # sentinel line — historical shape, still valid; (b) a sentinel nested in a
  # tool_result content string, the shape a skill's `Bash echo` produces. (The
  # SessionStart hook now emits to stdout, which the harness records as a
  # hook_success attachment whose .stdout is also a string — handled by the
  # same strings-via-fromjson path as the tool_result case.)
  local auto="{\"orchard_contract\":\"open\",\"id\":\"C-CONV-1\",\"statement\":\"$DELIVERABLE\",\"ts\":\"t\"}"
  _write_jsonl "S-STOP-BOTH" \
    "$auto" \
    "$(_open_line "C-SKILL-1" "a sub commitment")"
  _run_hook "S-STOP-BOTH"

  reason="$(printf '%s' "$output" | python3 -c 'import json,sys; print(json.load(sys.stdin)["reason"])')"
  [[ "$reason" == *"C-CONV-1"* ]]
  [[ "$reason" == *"C-SKILL-1"* ]]
}

# ---- no sentinels / missing jsonl → allow, exit 0 ---------------------------

@test "no contracts in jsonl allows Stop" {
  _write_jsonl "S-STOP-NONE" \
    '{"type":"user","message":{"role":"user","content":"just a normal message, no contracts"}}'
  _run_hook "S-STOP-NONE"

  [ "$status" -eq 0 ]
  [ -z "$(printf '%s' "$output" | tr -d '[:space:]')" ]
}

@test "missing jsonl allows Stop (degrades gracefully)" {
  _run_hook "S-STOP-MISSING"     # no jsonl written at all

  [ "$status" -eq 0 ]
  [ -z "$(printf '%s' "$output" | tr -d '[:space:]')" ]
}

# ---- malformed sentinel is ignored, valid one still blocks ------------------

@test "malformed sentinel (no id) is dropped; valid contract still blocks" {
  local valid="C-VALID-1"
  local malformed='{"type":"user","message":{"role":"user","content":[{"type":"tool_result","content":"{\"orchard_contract\":\"open\",\"statement\":\"no id here\",\"ts\":\"t\"}"}]}}'
  _write_jsonl "S-STOP-MALFORMED" \
    "$(_open_line "$valid" "real open contract")" \
    "$malformed"
  _run_hook "S-STOP-MALFORMED"

  reason="$(printf '%s' "$output" | python3 -c 'import json,sys; print(json.load(sys.stdin)["reason"])')"
  [[ "$reason" == *"$valid"* ]]
  [[ "$reason" != *"- : "* ]]        # no phantom empty-id contract line
}

# ---- CLAUDE_PROJECTS_DIR override -------------------------------------------

@test "honors CLAUDE_PROJECTS_DIR override for jsonl location" {
  local id="C-OVERRIDE-1"
  PROJECTS_ROOT="$HOME_DIR/custom/projects"
  PROJECTS_CWD="$HOME_DIR/work"
  mkdir -p "$PROJECTS_CWD"
  WORK="$PROJECTS_CWD"
  _write_jsonl "S-PROJECTS-DIR-OVR" "$(_open_line "$id" "verify override path")"

  CLAUDE_PROJECTS_DIR="$PROJECTS_ROOT" _run_hook "S-PROJECTS-DIR-OVR"

  reason="$(printf '%s' "$output" | python3 -c 'import json,sys; print(json.load(sys.stdin)["reason"])')"
  [[ "$reason" == *"$id"* ]]
}
