#!/usr/bin/env bats
# T2: L2 envelope assertions for gh/pr-label.sh
# Success AND failure paths.

setup() {
  SCRIPT="$BATS_TEST_DIRNAME/pr-label.sh"
  STUB_DIR="$(mktemp -d)"
}

teardown() {
  rm -rf "$STUB_DIR"
}

_json_field() {
  python3 -c "import json,sys; d=json.load(sys.stdin); print(d.get('$1','MISSING'))" 2>/dev/null || echo "PARSE_ERROR"
}

@test "missing --labels: ok=false" {
  cat > "$STUB_DIR/gh" <<'EOF'
#!/usr/bin/env bash
exit 0
EOF
  chmod +x "$STUB_DIR/gh"
  output="$(PATH="$STUB_DIR:$PATH" "$SCRIPT" --json --repo acme/repo --number 1 --labels "" 2>/dev/null || true)"
  [ "$(echo "$output" | _json_field ok)" = "False" ]
}

@test "success path: ok=true" {
  cat > "$STUB_DIR/gh" <<'EOF'
#!/usr/bin/env bash
exit 0
EOF
  chmod +x "$STUB_DIR/gh"
  output="$(PATH="$STUB_DIR:$PATH" "$SCRIPT" --json --repo acme/repo --number 1 --labels "bug,enhancement" 2>/dev/null)"
  [ "$(echo "$output" | _json_field ok)" = "True" ]
}

@test "failure path: ok=false" {
  cat > "$STUB_DIR/gh" <<'EOF'
#!/usr/bin/env bash
exit 1
EOF
  chmod +x "$STUB_DIR/gh"
  output="$(PATH="$STUB_DIR:$PATH" "$SCRIPT" --json --repo acme/repo --number 1 --labels "somelabel" 2>/dev/null || true)"
  [ "$(echo "$output" | _json_field ok)" = "False" ]
}
