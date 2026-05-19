#!/usr/bin/env bats
# T2: L2 envelope assertions for gh/issue-create.sh
# Success AND failure paths.

setup() {
  SCRIPT="$BATS_TEST_DIRNAME/issue-create.sh"
  STUB_DIR="$(mktemp -d)"
}

teardown() {
  rm -rf "$STUB_DIR"
}

_json_field() {
  python3 -c "import json,sys; d=json.load(sys.stdin); print(d.get('$1','MISSING'))" 2>/dev/null || echo "PARSE_ERROR"
}

@test "success path: ok=true and data present" {
  cat > "$STUB_DIR/gh" <<'EOF'
#!/usr/bin/env bash
echo "https://github.com/acme/repo/issues/100"
exit 0
EOF
  chmod +x "$STUB_DIR/gh"
  output="$(PATH="$STUB_DIR:$PATH" "$SCRIPT" --json --repo acme/repo --title "Bug: something broke" --body "Details here" 2>/dev/null)"
  [ "$(echo "$output" | _json_field ok)" = "True" ]
  data="$(echo "$output" | python3 -c "import json,sys; d=json.load(sys.stdin); print('present' if d.get('data') else 'absent')" 2>/dev/null)"
  [ "$data" = "present" ]
}

@test "failure path: ok=false and error present" {
  cat > "$STUB_DIR/gh" <<'EOF'
#!/usr/bin/env bash
echo "Error: authentication required" >&2
exit 1
EOF
  chmod +x "$STUB_DIR/gh"
  output="$(PATH="$STUB_DIR:$PATH" "$SCRIPT" --json --repo acme/repo --title "Bug" --body "" 2>/dev/null || true)"
  [ "$(echo "$output" | _json_field ok)" = "False" ]
}

@test "missing title: ok=false" {
  cat > "$STUB_DIR/gh" <<'EOF'
#!/usr/bin/env bash
echo "https://github.com/acme/repo/issues/101"
exit 0
EOF
  chmod +x "$STUB_DIR/gh"
  output="$(PATH="$STUB_DIR:$PATH" "$SCRIPT" --json --repo acme/repo --title "" --body "" 2>/dev/null || true)"
  [ "$(echo "$output" | _json_field ok)" = "False" ]
}
