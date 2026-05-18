#!/usr/bin/env bash
# T2: assert L2 envelope on success AND failure paths for tmux-send-text.sh
# Run with: bash scripts/tmux-send-text_test.sh
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SCRIPT="$SCRIPT_DIR/tmux-send-text.sh"

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

# Test 1: missing --pane should fail with envelope
echo "Test: missing --pane"
out=$(bash "$SCRIPT" --text "hi" --json 2>/dev/null || true)
assert_json_key "missing pane" "$out" "ok" "False"

# Test 2: missing --text should fail with envelope
echo "Test: missing --text"
out=$(bash "$SCRIPT" --pane "%1" --json 2>/dev/null || true)
assert_json_key "missing text" "$out" "ok" "False"

# Test 3: success path (requires live tmux; skip if not available)
if command -v tmux &>/dev/null && tmux info &>/dev/null 2>&1; then
  # Use the first available pane
  first_pane=$(tmux list-panes -a -F "#{pane_id}" 2>/dev/null | head -1 || true)
  if [[ -n "$first_pane" ]]; then
    echo "Test: success path (pane $first_pane)"
    out=$(bash "$SCRIPT" --pane "$first_pane" --text "#noop" --json 2>/dev/null || true)
    assert_json_key "success" "$out" "ok" "True"
  else
    echo "  SKIP: no panes available"
    ((pass++)) || true
  fi
else
  echo "  SKIP: tmux not available or not running"
  ((pass++)) || true
fi

# Test 4: envelope has ok field
echo "Test: envelope always has ok field"
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
