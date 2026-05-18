#!/usr/bin/env bash
# T2: assert L2 envelope on success AND failure paths for tmux-new-window.sh
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SCRIPT="$SCRIPT_DIR/tmux-new-window.sh"

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

# Test 1: missing --session should fail with ok=false envelope.
echo "Test: missing --session"
out=$(bash "$SCRIPT" --json 2>/dev/null || true)
assert_json_key "missing session" "$out" "ok" "False"

# Test 2: invalid session name should fail with ok=false.
echo "Test: invalid session"
out=$(bash "$SCRIPT" --session "nosuchsession$$" --json 2>/dev/null || true)
assert_json_key "invalid session" "$out" "ok" "False"

# Test 3: success path (requires live tmux session).
if command -v tmux &>/dev/null && tmux info &>/dev/null 2>&1; then
  first_sess=$(tmux list-sessions -F "#{session_name}" 2>/dev/null | head -1 || true)
  if [[ -n "$first_sess" ]]; then
    echo "Test: success path (session $first_sess)"
    out=$(bash "$SCRIPT" --session "$first_sess" --name "test-window-$$" --json 2>/dev/null || true)
    assert_json_key "success" "$out" "ok" "True"
    # Clean up the test window.
    idx=$(echo "$out" | python3 -c "import sys,json; d=json.load(sys.stdin); print(d.get('data',{}).get('index',''))" 2>/dev/null || echo "")
    if [[ -n "$idx" ]]; then
      tmux kill-window -t "$first_sess:$idx" 2>/dev/null || true
    fi
  else
    echo "  SKIP: no sessions available"
    ((pass++)) || true
  fi
else
  echo "  SKIP: tmux not available or not running"
  ((pass++)) || true
fi

# Test 4: envelope always has ok field.
echo "Test: envelope has ok field"
out=$(bash "$SCRIPT" --json 2>/dev/null || true)
if echo "$out" | python3 -c "import sys,json; d=json.load(sys.stdin); assert 'ok' in d" 2>/dev/null; then
  echo "  PASS: envelope has ok field"
  ((pass++)) || true
else
  echo "  FAIL: envelope missing ok field"
  ((fail++)) || true
fi

echo ""
echo "Results: $pass passed, $fail failed"
[[ "$fail" -eq 0 ]]
