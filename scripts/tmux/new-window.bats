#!/usr/bin/env bats
# T2: L2 envelope assertions for tmux/new-window.sh
# Success AND failure paths.

setup() {
  SCRIPT="$BATS_TEST_DIRNAME/new-window.sh"
}

_json_field() {
  python3 -c "import json,sys; d=json.load(sys.stdin); print(d.get('$1','MISSING'))" 2>/dev/null || echo "PARSE_ERROR"
}

@test "missing --session: ok=false" {
  output="$(bash "$SCRIPT" --json 2>/dev/null || true)"
  [ "$(echo "$output" | _json_field ok)" = "False" ]
}

@test "invalid session name: ok=false" {
  output="$(bash "$SCRIPT" --session "nosuchsession$$" --json 2>/dev/null || true)"
  [ "$(echo "$output" | _json_field ok)" = "False" ]
}

@test "envelope always has ok field" {
  output="$(bash "$SCRIPT" --json 2>/dev/null || true)"
  has_ok="$(echo "$output" | python3 -c "import sys,json; d=json.load(sys.stdin); print('yes' if 'ok' in d else 'no')" 2>/dev/null)"
  [ "$has_ok" = "yes" ]
}

@test "success path with live tmux (skip if unavailable)" {
  if ! command -v tmux &>/dev/null || ! tmux info &>/dev/null 2>&1; then
    skip "tmux not available"
  fi
  first_sess="$(tmux list-sessions -F "#{session_name}" 2>/dev/null | head -1 || true)"
  if [ -z "$first_sess" ]; then
    skip "no sessions available"
  fi
  win_name="bats-test-$$"
  output="$(bash "$SCRIPT" --session "$first_sess" --name "$win_name" --json 2>/dev/null || true)"
  [ "$(echo "$output" | _json_field ok)" = "True" ]
  idx="$(echo "$output" | python3 -c "import sys,json; d=json.load(sys.stdin); print(d.get('data',{}).get('index',''))" 2>/dev/null || echo "")"
  [ -n "$idx" ] && tmux kill-window -t "$first_sess:$idx" 2>/dev/null || true
}
