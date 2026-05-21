#!/usr/bin/env bats
# T2: L2 envelope assertions for host-services/restart.sh
# Success AND failure paths.

setup() {
  SCRIPT="$BATS_TEST_DIRNAME/restart.sh"
  EMPTY_DIR="$(mktemp -d)"
  STUB_DIR="$(mktemp -d)"
}

teardown() {
  rm -rf "$EMPTY_DIR" "$STUB_DIR"
}

_json_field() {
  python3 -c "import json,sys; d=json.load(sys.stdin); print(d.get('$1','MISSING'))" 2>/dev/null || echo "PARSE_ERROR"
}

_json_err_code() {
  python3 -c "import json,sys; d=json.load(sys.stdin); print(d.get('error',{}).get('code','MISSING'))" 2>/dev/null || echo "PARSE_ERROR"
}

@test "missing --name: ok=false, code=invalid_input" {
  output="$("$SCRIPT" --json 2>/dev/null || true)"
  [ "$(echo "$output" | _json_field ok)" = "False" ]
  [ "$(echo "$output" | _json_err_code)" = "invalid_input" ]
}

@test "service manager missing: ok=false, code=service_manager_missing" {
  output="$(PATH="$EMPTY_DIR" "$SCRIPT" --json --name "example.svc" 2>/dev/null || true)"
  [ "$(echo "$output" | _json_field ok)" = "False" ]
  [ "$(echo "$output" | _json_err_code)" = "service_manager_missing" ]
}

@test "success path with stub systemctl: ok=true" {
  cat > "$STUB_DIR/systemctl" <<'EOF'
#!/bin/sh
exit 0
EOF
  chmod +x "$STUB_DIR/systemctl"
  output="$(PATH="$STUB_DIR" "$SCRIPT" --json --name "example.service" 2>/dev/null)"
  [ "$(echo "$output" | _json_field ok)" = "True" ]
}
