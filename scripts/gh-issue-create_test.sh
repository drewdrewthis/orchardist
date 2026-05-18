#!/usr/bin/env bash
# gh-issue-create_test.sh — T2: assert {ok, data?, error?} envelope on success AND failure.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SCRIPT="$SCRIPT_DIR/gh-issue-create.sh"

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

TMPDIR_GH=$(mktemp -d)
trap 'rm -rf "$TMPDIR_GH"' EXIT

# --- Success stub ---
cat > "$TMPDIR_GH/gh" <<'EOF'
#!/usr/bin/env bash
echo "https://github.com/acme/repo/issues/100"
exit 0
EOF
chmod +x "$TMPDIR_GH/gh"

echo "gh-issue-create.sh: success path"
output=$(PATH="$TMPDIR_GH:$PATH" "$SCRIPT" --json --repo acme/repo --title "Bug: something broke" --body "Details here" 2>/dev/null)
assert_json_field "ok=true on success" "$output" "ok" "True"

data_field=$(echo "$output" | python3 -c "import json,sys; d=json.load(sys.stdin); print('present' if d.get('data') else 'absent')" 2>/dev/null)
if [[ "$data_field" == "present" ]]; then
  echo "  PASS: data present on success"
  ((pass++)) || true
else
  echo "  FAIL: expected data on success"
  ((fail++)) || true
fi

# --- Failure stub ---
cat > "$TMPDIR_GH/gh" <<'EOF'
#!/usr/bin/env bash
echo "Error: authentication required" >&2
exit 1
EOF
chmod +x "$TMPDIR_GH/gh"

echo "gh-issue-create.sh: failure path"
output=$(PATH="$TMPDIR_GH:$PATH" "$SCRIPT" --json --repo acme/repo --title "Bug" --body "" 2>/dev/null || true)
assert_json_field "ok=false on failure" "$output" "ok" "False"

# --- Missing title (M4) ---
echo "gh-issue-create.sh: missing title"
output=$(PATH="$TMPDIR_GH:$PATH" "$SCRIPT" --json --repo acme/repo --title "" --body "" 2>/dev/null || true)
assert_json_field "ok=false on empty title" "$output" "ok" "False"

echo ""
echo "Results: $pass passed, $fail failed"
[[ $fail -eq 0 ]]
