#!/usr/bin/env bats
# T2: L2 envelope assertions for tmux/send-text.sh
# Success AND failure paths.

setup() {
  SCRIPT="$BATS_TEST_DIRNAME/send-text.sh"
}

_json_field() {
  python3 -c "import json,sys; d=json.load(sys.stdin); print(d.get('$1','MISSING'))" 2>/dev/null || echo "PARSE_ERROR"
}

@test "missing --pane: ok=false" {
  output="$(bash "$SCRIPT" --text "hi" --json 2>/dev/null || true)"
  [ "$(echo "$output" | _json_field ok)" = "False" ]
}

@test "missing --text: ok=false" {
  output="$(bash "$SCRIPT" --pane "%1" --json 2>/dev/null || true)"
  [ "$(echo "$output" | _json_field ok)" = "False" ]
}

@test "empty --pane: envelope has ok field" {
  output="$(bash "$SCRIPT" --pane "" --json 2>/dev/null || true)"
  has_ok="$(echo "$output" | python3 -c "import sys,json; d=json.load(sys.stdin); print('yes' if 'ok' in d else 'no')" 2>/dev/null)"
  [ "$has_ok" = "yes" ]
}

@test "success path with live tmux (skip if unavailable)" {
  if ! command -v tmux &>/dev/null || ! tmux info &>/dev/null 2>&1; then
    skip "tmux not available"
  fi
  first_pane="$(tmux list-panes -a -F "#{pane_id}" 2>/dev/null | head -1 || true)"
  if [ -z "$first_pane" ]; then
    skip "no panes available"
  fi
  output="$(bash "$SCRIPT" --pane "$first_pane" --text "#noop" --json 2>/dev/null || true)"
  [ "$(echo "$output" | _json_field ok)" = "True" ]
}
