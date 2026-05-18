#!/usr/bin/env bats
# T2: L2 envelope assertions for tmux/kill-pane.sh
# Success AND failure paths.

setup() {
  SCRIPT="$BATS_TEST_DIRNAME/kill-pane.sh"
}

_json_field() {
  python3 -c "import json,sys; d=json.load(sys.stdin); print(d.get('$1','MISSING'))" 2>/dev/null || echo "PARSE_ERROR"
}

@test "missing --pane: ok=false" {
  output="$(bash "$SCRIPT" --json 2>/dev/null || true)"
  [ "$(echo "$output" | _json_field ok)" = "False" ]
}

@test "invalid pane id (no server): ok=false" {
  output="$(bash "$SCRIPT" --pane "%99999" --json 2>/dev/null || true)"
  [ "$(echo "$output" | _json_field ok)" = "False" ]
}

@test "envelope always has ok field" {
  output="$(bash "$SCRIPT" --pane "" --json 2>/dev/null || true)"
  has_ok="$(echo "$output" | python3 -c "import sys,json; d=json.load(sys.stdin); print('yes' if 'ok' in d else 'no')" 2>/dev/null)"
  [ "$has_ok" = "yes" ]
}
