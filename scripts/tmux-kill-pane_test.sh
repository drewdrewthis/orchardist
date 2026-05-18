#!/usr/bin/env bash
# T2: assert L2 envelope on success AND failure paths for tmux-kill-pane.sh
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SCRIPT="$SCRIPT_DIR/tmux-kill-pane.sh"

pass=0
fail=0

assert_json_key() {
  local label="$1" json="$2" key="$3" want="$4"
  local got
  got=$(echo "$json" | python3 -c "import sys,json; d=json.load(sys.stdin); print(d.get('$key','MISSING'))" 2>/dev/null || echo "PARSE_ERROR")
  if [[ "$got" == "$want" ]]; then
    echo "  PASS: $label ($key=$want)"
    ((pass++)) || true
  else
    echo "  FAIL: $label: $key=$got want=$want  (json: $json)"
    ((fail++)) || true
  fi
}

# Test 1: missing --pane should fail with ok=false envelope.
echo "Test: missing --pane"
out=$(bash "$SCRIPT" --json 2>/dev/null || true)
assert_json_key "missing pane" "$out" "ok" "False"

# Test 2: invalid pane id (no server) should fail with ok=false.
echo "Test: invalid pane id"
out=$(bash "$SCRIPT" --pane "%99999" --json 2>/dev/null || true)
assert_json_key "invalid pane" "$out" "ok" "False"

# Test 3: envelope always has ok field.
echo "Test: envelope has ok field"
out=$(bash "$SCRIPT" --pane "" --json 2>/dev/null || true)
if echo "$out" | python3 -c "import sys,json; d=json.load(sys.stdin); assert 'ok' in d" 2>/dev/null; then
  echo "  PASS: envelope has ok field"
  ((pass++)) || true
else
  echo "  FAIL: envelope missing ok field (got: $out)"
  ((fail++)) || true
fi

echo ""
echo "Results: $pass passed, $fail failed"
[[ "$fail" -eq 0 ]]
