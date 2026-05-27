#!/usr/bin/env bats
# Tests for on-prompt-submit.sh — the UserPromptSubmit hook.
#
# The hook auto-opens the conversation contract by appending exactly one
# `orchard_contract` open sentinel (source "auto-prompt-submit") to the session
# jsonl. It is idempotent: repeated prompts in a session yield exactly one
# auto-open sentinel. No MCP server, no resident process.
#
# Bash resets $PWD to its actual cwd on startup, so we run the hook from inside
# $WORK and write/read the jsonl under the encoded-$WORK path the hook resolves.

setup() {
  HOOK="$BATS_TEST_DIRNAME/on-prompt-submit.sh"
  HOME_DIR="$(mktemp -d)"
  WORK="$HOME_DIR"
  DELIVERABLE="user agrees conversation has come to a close and there are no loose ends"
}

teardown() {
  rm -rf "$HOME_DIR"
}

_encode() { printf '%s' "$1" | tr '/.' '--'; }

_jsonl_path() {
  printf '%s/.claude/projects/%s/%s.jsonl' "$HOME_DIR" "$(_encode "$WORK")" "$1"
}

# _count_auto_open <jsonl> — number of auto-prompt-submit open sentinels (bare
# objects, one per line).
_count_auto_open() {
  python3 - "$1" <<'PY'
import json, sys
n = 0
for line in open(sys.argv[1]):
    line = line.strip()
    if not line:
        continue
    try:
        rec = json.loads(line)
    except Exception:
        continue
    if isinstance(rec, dict) and rec.get("orchard_contract") == "open" and rec.get("source") == "auto-prompt-submit":
        n += 1
print(n)
PY
}

# _field <jsonl> <key> — the value of <key> on the (first) auto-open sentinel.
_field() {
  python3 - "$1" "$2" <<'PY'
import json, sys
for line in open(sys.argv[1]):
    line = line.strip()
    if not line:
        continue
    try:
        rec = json.loads(line)
    except Exception:
        continue
    if isinstance(rec, dict) and rec.get("orchard_contract") == "open" and rec.get("source") == "auto-prompt-submit":
        print(rec.get(sys.argv[2], ""))
        break
PY
}

# _run_env <session-id> — run with CLAUDE_SESSION_ID in env (no stdin payload).
_run_env() {
  run env HOME="$HOME_DIR" CLAUDE_SESSION_ID="$1" \
      bash -c "cd '$WORK' && bash '$HOOK' </dev/null"
}

# _run_stdin <session-id> — run with a UserPromptSubmit payload on stdin and NO
# CLAUDE_SESSION_ID in env (the real Claude Code contract).
_run_stdin() {
  local payload="{\"hook_event_name\":\"UserPromptSubmit\",\"session_id\":\"$1\",\"cwd\":\"$WORK\",\"prompt\":\"first message\"}"
  run env HOME="$HOME_DIR" \
      bash -c "cd '$WORK' && printf '%s' '$payload' | bash '$HOOK'"
}

# ---- first message writes exactly one auto-open sentinel --------------------

@test "first message writes exactly one auto-open sentinel with the deliverable" {
  _run_env "S-HOOK-TEST-001"
  [ "$status" -eq 0 ]

  local jsonl; jsonl="$(_jsonl_path "S-HOOK-TEST-001")"
  [ "$(_count_auto_open "$jsonl")" -eq 1 ]
  [ "$(_field "$jsonl" statement)" = "$DELIVERABLE" ]
  [[ "$(_field "$jsonl" id)" == C-* ]]
}

# ---- idempotent: three runs still yield one sentinel ------------------------

@test "idempotent: three runs yield exactly one auto-open sentinel" {
  _run_env "S-HOOK-IDEMPOTENT"
  _run_env "S-HOOK-IDEMPOTENT"
  _run_env "S-HOOK-IDEMPOTENT"

  local jsonl; jsonl="$(_jsonl_path "S-HOOK-IDEMPOTENT")"
  [ "$(_count_auto_open "$jsonl")" -eq 1 ]
}

# ---- production path: derives session_id from the stdin payload -------------

@test "derives session_id from stdin payload when CLAUDE_SESSION_ID is unset" {
  # Critically: no CLAUDE_SESSION_ID in env — real Claude Code passes it on stdin.
  _run_stdin "S-STDIN-PAYLOAD-001"
  [ "$status" -eq 0 ]

  local jsonl; jsonl="$(_jsonl_path "S-STDIN-PAYLOAD-001")"
  [ "$(_count_auto_open "$jsonl")" -eq 1 ]
}
