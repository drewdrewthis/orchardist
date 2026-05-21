#!/usr/bin/env bats
# T2: L2 envelope assertions for contracts/get.sh
# Success AND failure paths. Uses stub curl binaries — no real daemon required.

setup() {
  SCRIPT="$BATS_TEST_DIRNAME/get.sh"
  STUB_DIR="$(mktemp -d)"
}

teardown() {
  rm -rf "$STUB_DIR"
}

_json_field() {
  python3 -c "import json,sys; d=json.load(sys.stdin); print(d.get('$1','MISSING'))" 2>/dev/null || echo "PARSE_ERROR"
}

# --- success path ---

@test "found contract: ok=true and data.contract present" {
  cat > "$STUB_DIR/curl" <<'EOF'
#!/usr/bin/env bash
printf '{"data":{"contract":{"id":"Contract:C-2026-01-01-abc12345","contractId":"C-2026-01-01-abc12345","statement":"do the thing","status":"OPEN","ownerSessionId":"s1","ownerAgentName":"agent1","reportsTo":null,"parentContractId":null,"createdAt":"2026-01-01T00:00:00Z","updatedAt":"2026-01-01T00:00:00Z","lastEventAt":"2026-01-01T00:00:00Z","criteria":[],"openQuestions":[]}}}'
EOF
  chmod +x "$STUB_DIR/curl"
  output="$(PATH="$STUB_DIR:$PATH" "$SCRIPT" --json --id "Contract:C-2026-01-01-abc12345" 2>/dev/null)"
  [ "$(echo "$output" | _json_field ok)" = "True" ]
  data_check="$(echo "$output" | python3 -c "import json,sys; d=json.load(sys.stdin); print('present' if d.get('data',{}).get('contract') is not None else 'absent')" 2>/dev/null)"
  [ "$data_check" = "present" ]
}

@test "unknown contract: ok=true and data.contract is null" {
  cat > "$STUB_DIR/curl" <<'EOF'
#!/usr/bin/env bash
printf '{"data":{"contract":null}}'
EOF
  chmod +x "$STUB_DIR/curl"
  output="$(PATH="$STUB_DIR:$PATH" "$SCRIPT" --json --id "Contract:unknown" 2>/dev/null)"
  [ "$(echo "$output" | _json_field ok)" = "True" ]
  null_check="$(echo "$output" | python3 -c "import json,sys; d=json.load(sys.stdin); print('null' if d.get('data',{}).get('contract') is None else 'not-null')" 2>/dev/null)"
  [ "$null_check" = "null" ]
}

# --- failure path ---

@test "missing --id: ok=false with INVALID_INPUT code" {
  output="$(PATH="$STUB_DIR:$PATH" "$SCRIPT" --json 2>/dev/null || true)"
  [ "$(echo "$output" | _json_field ok)" = "False" ]
  code="$(echo "$output" | python3 -c "import json,sys; d=json.load(sys.stdin); print(d.get('error',{}).get('code','MISSING'))" 2>/dev/null)"
  [ "$code" = "INVALID_INPUT" ]
}

@test "daemon unavailable: ok=false with DAEMON_UNAVAILABLE code" {
  cat > "$STUB_DIR/curl" <<'EOF'
#!/usr/bin/env bash
echo "connection refused" >&2
exit 7
EOF
  chmod +x "$STUB_DIR/curl"
  output="$(PATH="$STUB_DIR:$PATH" "$SCRIPT" --json --id "Contract:C-2026-01-01-abc12345" 2>/dev/null || true)"
  [ "$(echo "$output" | _json_field ok)" = "False" ]
  code="$(echo "$output" | python3 -c "import json,sys; d=json.load(sys.stdin); print(d.get('error',{}).get('code','MISSING'))" 2>/dev/null)"
  [ "$code" = "DAEMON_UNAVAILABLE" ]
}

@test "GraphQL error response: ok=false with GRAPHQL_ERROR code" {
  cat > "$STUB_DIR/curl" <<'EOF'
#!/usr/bin/env bash
printf '{"errors":[{"message":"internal error"}]}'
EOF
  chmod +x "$STUB_DIR/curl"
  output="$(PATH="$STUB_DIR:$PATH" "$SCRIPT" --json --id "Contract:C-2026-01-01-abc12345" 2>/dev/null || true)"
  [ "$(echo "$output" | _json_field ok)" = "False" ]
  code="$(echo "$output" | python3 -c "import json,sys; d=json.load(sys.stdin); print(d.get('error',{}).get('code','MISSING'))" 2>/dev/null)"
  [ "$code" = "GRAPHQL_ERROR" ]
}
