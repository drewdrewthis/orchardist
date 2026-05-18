#!/usr/bin/env bats
# T2: L2 envelope assertions for gh/pr-review.sh
# Success AND failure paths.

setup() {
  SCRIPT="$BATS_TEST_DIRNAME/pr-review.sh"
  STUB_DIR="$(mktemp -d)"
}

teardown() {
  rm -rf "$STUB_DIR"
}

_json_field() {
  python3 -c "import json,sys; d=json.load(sys.stdin); print(d.get('$1','MISSING'))" 2>/dev/null || echo "PARSE_ERROR"
}

@test "success path: ok=true, error absent" {
  cat > "$STUB_DIR/gh" <<'EOF'
#!/usr/bin/env bash
echo "https://github.com/acme/repo/pull/42#pullrequestreview-12345"
exit 0
EOF
  chmod +x "$STUB_DIR/gh"
  output="$(PATH="$STUB_DIR:$PATH" "$SCRIPT" --json --repo acme/repo --number 42 --event APPROVE --body "LGTM" 2>/dev/null)"
  [ "$(echo "$output" | _json_field ok)" = "True" ]
  err="$(echo "$output" | python3 -c "import json,sys; d=json.load(sys.stdin); print(d.get('error','null'))" 2>/dev/null)"
  [ "$err" = "None" ] || [ "$err" = "null" ]
}

@test "failure path: ok=false, error present" {
  cat > "$STUB_DIR/gh" <<'EOF'
#!/usr/bin/env bash
echo "GraphQL error: not authorized" >&2
exit 1
EOF
  chmod +x "$STUB_DIR/gh"
  output="$(PATH="$STUB_DIR:$PATH" "$SCRIPT" --json --repo acme/repo --number 42 --event APPROVE --body "" 2>/dev/null || true)"
  [ "$(echo "$output" | _json_field ok)" = "False" ]
  present="$(echo "$output" | python3 -c "import json,sys; d=json.load(sys.stdin); print('present' if d.get('error') else 'absent')" 2>/dev/null)"
  [ "$present" = "present" ]
}

@test "invalid event: ok=false" {
  cat > "$STUB_DIR/gh" <<'EOF'
#!/usr/bin/env bash
exit 0
EOF
  chmod +x "$STUB_DIR/gh"
  output="$(PATH="$STUB_DIR:$PATH" "$SCRIPT" --json --repo acme/repo --number 42 --event BADVALUE --body "" 2>/dev/null || true)"
  [ "$(echo "$output" | _json_field ok)" = "False" ]
}
