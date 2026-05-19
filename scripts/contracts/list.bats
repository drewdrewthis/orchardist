#!/usr/bin/env bats
# T2: L2 envelope assertions for contracts/list.sh
# Success AND failure paths. Uses stub curl binaries — no real daemon required.

setup() {
  SCRIPT="$BATS_TEST_DIRNAME/list.sh"
  STUB_DIR="$(mktemp -d)"
}

teardown() {
  rm -rf "$STUB_DIR"
}

_json_field() {
  python3 -c "import json,sys; d=json.load(sys.stdin); print(d.get('$1','MISSING'))" 2>/dev/null || echo "PARSE_ERROR"
}

# --- success path ---

@test "success path: ok=true and data.contracts present" {
  cat > "$STUB_DIR/curl" <<'EOF'
#!/usr/bin/env bash
printf '{"data":{"contracts":[{"id":"Contract:C-2026-01-01-abc12345","contractId":"C-2026-01-01-abc12345","statement":"do the thing","status":"OPEN","ownerSessionId":"s1","ownerAgentName":"agent1","createdAt":"2026-01-01T00:00:00Z","updatedAt":"2026-01-01T00:00:00Z","lastEventAt":"2026-01-01T00:00:00Z","criteria":[],"openQuestions":[]}]}}'
EOF
  chmod +x "$STUB_DIR/curl"
  output="$(PATH="$STUB_DIR:$PATH" "$SCRIPT" --json 2>/dev/null)"
  [ "$(echo "$output" | _json_field ok)" = "True" ]
  data_check="$(echo "$output" | python3 -c "import json,sys; d=json.load(sys.stdin); print('present' if 'contracts' in d.get('data',{}) else 'absent')" 2>/dev/null)"
  [ "$data_check" = "present" ]
}

@test "success path with --status filter: ok=true" {
  cat > "$STUB_DIR/curl" <<'EOF'
#!/usr/bin/env bash
printf '{"data":{"contracts":[]}}'
EOF
  chmod +x "$STUB_DIR/curl"
  output="$(PATH="$STUB_DIR:$PATH" "$SCRIPT" --json --status OPEN 2>/dev/null)"
  [ "$(echo "$output" | _json_field ok)" = "True" ]
}

@test "success path with --owner-agent filter: ok=true" {
  cat > "$STUB_DIR/curl" <<'EOF'
#!/usr/bin/env bash
printf '{"data":{"contracts":[]}}'
EOF
  chmod +x "$STUB_DIR/curl"
  output="$(PATH="$STUB_DIR:$PATH" "$SCRIPT" --json --owner-agent "myagent" 2>/dev/null)"
  [ "$(echo "$output" | _json_field ok)" = "True" ]
}

# --- failure path ---

@test "daemon unavailable: ok=false with DAEMON_UNAVAILABLE code" {
  cat > "$STUB_DIR/curl" <<'EOF'
#!/usr/bin/env bash
echo "connection refused" >&2
exit 7
EOF
  chmod +x "$STUB_DIR/curl"
  output="$(PATH="$STUB_DIR:$PATH" "$SCRIPT" --json 2>/dev/null || true)"
  [ "$(echo "$output" | _json_field ok)" = "False" ]
  code="$(echo "$output" | python3 -c "import json,sys; d=json.load(sys.stdin); print(d.get('error',{}).get('code','MISSING'))" 2>/dev/null)"
  [ "$code" = "DAEMON_UNAVAILABLE" ]
}

@test "GraphQL error response: ok=false with GRAPHQL_ERROR code" {
  cat > "$STUB_DIR/curl" <<'EOF'
#!/usr/bin/env bash
printf '{"errors":[{"message":"field not found"}]}'
EOF
  chmod +x "$STUB_DIR/curl"
  output="$(PATH="$STUB_DIR:$PATH" "$SCRIPT" --json 2>/dev/null || true)"
  [ "$(echo "$output" | _json_field ok)" = "False" ]
  code="$(echo "$output" | python3 -c "import json,sys; d=json.load(sys.stdin); print(d.get('error',{}).get('code','MISSING'))" 2>/dev/null)"
  [ "$code" = "GRAPHQL_ERROR" ]
}

@test "unknown arg: exits non-zero" {
  run "$SCRIPT" --unknown-flag
  [ "$status" -ne 0 ]
}
