#!/usr/bin/env bash
# gh-pr-review_test.sh — T2: assert {ok, data?, error?} envelope on success AND failure.
#
# T2: "Every mutation script has its own test of the --json envelope."
# These tests run with a fake `gh` stub on PATH that controls the exit code.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SCRIPT="$SCRIPT_DIR/gh-pr-review.sh"

pass=0
fail=0

assert_json_field() {
  local label="$1" json="$2" field="$3" expected="$4"
  local actual
  actual=$(echo "$json" | python3 -c "import json,sys; d=json.load(sys.stdin); print(d.get('$field','MISSING'))" 2>/dev/null || echo "PARSE_ERROR")
  if [[ "$actual" == "$expected" ]]; then
    echo "  PASS: $label"
    ((pass++)) || true
  else
    echo "  FAIL: $label — want $field=$expected, got $field=$actual"
    ((fail++)) || true
  fi
}

# --- Setup: fake gh stub that succeeds ---
TMPDIR_GH=$(mktemp -d)
trap 'rm -rf "$TMPDIR_GH"' EXIT

cat > "$TMPDIR_GH/gh" <<'EOF'
#!/usr/bin/env bash
# Fake gh CLI: exits 0 and prints a fake review URL.
echo "https://github.com/acme/repo/pull/42#pullrequestreview-12345"
exit 0
EOF
chmod +x "$TMPDIR_GH/gh"

# --- Test: success path ---
echo "gh-pr-review.sh: success path"
output=$(PATH="$TMPDIR_GH:$PATH" "$SCRIPT" --json --repo acme/repo --number 42 --event APPROVE --body "LGTM" 2>/dev/null)

assert_json_field "ok=true on success" "$output" "ok" "True"

error_field=$(echo "$output" | python3 -c "import json,sys; d=json.load(sys.stdin); print(d.get('error','null'))" 2>/dev/null)
if [[ "$error_field" == "None" || "$error_field" == "null" ]]; then
  echo "  PASS: error=null on success"
  ((pass++)) || true
else
  echo "  FAIL: expected error=null on success, got: $error_field"
  ((fail++)) || true
fi

# --- Setup: fake gh stub that fails ---
cat > "$TMPDIR_GH/gh" <<'EOF'
#!/usr/bin/env bash
echo "GraphQL error: not authorized" >&2
exit 1
EOF
chmod +x "$TMPDIR_GH/gh"

# --- Test: failure path ---
echo "gh-pr-review.sh: failure path"
output=$(PATH="$TMPDIR_GH:$PATH" "$SCRIPT" --json --repo acme/repo --number 42 --event APPROVE --body "" 2>/dev/null || true)

assert_json_field "ok=false on failure" "$output" "ok" "False"

error_obj=$(echo "$output" | python3 -c "import json,sys; d=json.load(sys.stdin); e=d.get('error'); print('present' if e else 'absent')" 2>/dev/null)
if [[ "$error_obj" == "present" ]]; then
  echo "  PASS: error object present on failure"
  ((pass++)) || true
else
  echo "  FAIL: expected error object on failure, got: absent"
  ((fail++)) || true
fi

# --- Test: invalid event rejection (M4) ---
echo "gh-pr-review.sh: invalid event"
output=$(PATH="$TMPDIR_GH:$PATH" "$SCRIPT" --json --repo acme/repo --number 42 --event BADVALUE --body "" 2>/dev/null || true)
assert_json_field "ok=false on invalid event" "$output" "ok" "False"

# --- Summary ---
echo ""
echo "Results: $pass passed, $fail failed"
if [[ $fail -gt 0 ]]; then
  exit 1
fi
