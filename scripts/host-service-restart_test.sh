#!/usr/bin/env bash
# host-service-restart_test.sh — T2: L2 envelope assertion for restart script.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
SCRIPT="$SCRIPT_DIR/host-service-restart.sh"

PASS=0
FAIL=0

assert_json_ok() {
  local label="$1" output="$2"
  local ok
  ok=$(echo "$output" | python3 -c "import sys,json; d=json.load(sys.stdin); print(d['ok'])" 2>/dev/null)
  if [[ "$ok" == "True" ]]; then
    echo "PASS: $label"; PASS=$((PASS+1))
  else
    echo "FAIL: $label — expected ok=true, got: $output"; FAIL=$((FAIL+1))
  fi
}

assert_json_err() {
  local label="$1" output="$2" want_code="$3"
  local ok code
  ok=$(echo "$output" | python3 -c "import sys,json; d=json.load(sys.stdin); print(d['ok'])" 2>/dev/null)
  code=$(echo "$output" | python3 -c "import sys,json; d=json.load(sys.stdin); print(d.get('error',{}).get('code',''))" 2>/dev/null)
  if [[ "$ok" == "False" && "$code" == "$want_code" ]]; then
    echo "PASS: $label"; PASS=$((PASS+1))
  else
    echo "FAIL: $label — expected ok=false code=$want_code, got ok=$ok code=$code output=$output"; FAIL=$((FAIL+1))
  fi
}

empty_dir=$(mktemp -d)
stub_dir=$(mktemp -d)
trap 'rm -rf "$empty_dir" "$stub_dir"' EXIT

# missing --name
output=$("$SCRIPT" --json 2>/dev/null || true)
assert_json_err "missing --name" "$output" "invalid_input"

# service manager missing
output=$(PATH="$empty_dir" "$SCRIPT" --json --name "example.svc" 2>/dev/null || true)
assert_json_err "service_manager_missing" "$output" "service_manager_missing"

# success path with systemctl stub (avoids launchctl kickstart complexity)
cat > "$stub_dir/systemctl" << 'EOF'
#!/bin/sh
exit 0
EOF
chmod +x "$stub_dir/systemctl"
output=$(PATH="$stub_dir" "$SCRIPT" --json --name "example.service" 2>/dev/null)
assert_json_ok "success path (stub systemctl)" "$output"

echo ""; echo "Results: $PASS passed, $FAIL failed"
if [[ $FAIL -gt 0 ]]; then exit 1; fi
