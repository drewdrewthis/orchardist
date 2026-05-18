#!/usr/bin/env bash
# host-service-start_test.sh — T2: assert the L2 envelope on success and
# failure paths for host-service-start.sh.
#
# Runs as a standalone bash script. Exit 0 = all assertions passed.
# Exit non-zero = at least one assertion failed.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
SCRIPT="$SCRIPT_DIR/host-service-start.sh"

PASS=0
FAIL=0

assert_json_ok() {
  local label="$1" output="$2"
  local ok
  ok=$(echo "$output" | python3 -c "import sys,json; d=json.load(sys.stdin); print(d['ok'])" 2>/dev/null)
  if [[ "$ok" == "True" ]]; then
    echo "PASS: $label"
    PASS=$((PASS+1))
  else
    echo "FAIL: $label — expected ok=true, got: $output"
    FAIL=$((FAIL+1))
  fi
}

assert_json_err() {
  local label="$1" output="$2" want_code="$3"
  local ok code
  ok=$(echo "$output" | python3 -c "import sys,json; d=json.load(sys.stdin); print(d['ok'])" 2>/dev/null)
  code=$(echo "$output" | python3 -c "import sys,json; d=json.load(sys.stdin); print(d.get('error',{}).get('code',''))" 2>/dev/null)
  if [[ "$ok" == "False" && "$code" == "$want_code" ]]; then
    echo "PASS: $label"
    PASS=$((PASS+1))
  else
    echo "FAIL: $label — expected ok=false code=$want_code, got ok=$ok code=$code output=$output"
    FAIL=$((FAIL+1))
  fi
}

# --- Failure path: missing --name ---
output=$("$SCRIPT" --json 2>/dev/null || true)
assert_json_err "missing --name" "$output" "invalid_input"

# --- Failure path: service manager missing ---
# Point PATH at an empty directory so no service manager is found.
empty_dir=$(mktemp -d)
trap 'rm -rf "$empty_dir"' EXIT

output=$(PATH="$empty_dir" "$SCRIPT" --json --name "example.test.svc" 2>/dev/null || true)
assert_json_err "service_manager_missing" "$output" "service_manager_missing"

# --- Success path: stub launchctl/systemctl that exits 0 ---
stub_dir=$(mktemp -d)
trap 'rm -rf "$stub_dir" "$empty_dir"' EXIT

cat > "$stub_dir/launchctl" << 'EOF'
#!/bin/sh
exit 0
EOF
chmod +x "$stub_dir/launchctl"

output=$(PATH="$stub_dir:$PATH" "$SCRIPT" --json --name "com.example.test.svc" 2>/dev/null)
assert_json_ok "success path (stub launchctl)" "$output"

echo ""
echo "Results: $PASS passed, $FAIL failed"
if [[ $FAIL -gt 0 ]]; then exit 1; fi
