#!/usr/bin/env bats
# T2: L2 envelope assertions for gh/pr-comment.sh
# Success AND failure paths.

setup() {
  SCRIPT="$BATS_TEST_DIRNAME/pr-comment.sh"
  STUB_DIR="$(mktemp -d)"
}

teardown() {
  rm -rf "$STUB_DIR"
}

_json_field() {
  python3 -c "import json,sys; d=json.load(sys.stdin); print(d.get('$1','MISSING'))" 2>/dev/null || echo "PARSE_ERROR"
}

@test "missing --repo: ok=false" {
  cat > "$STUB_DIR/gh" <<'EOF'
#!/usr/bin/env bash
exit 0
EOF
  chmod +x "$STUB_DIR/gh"
  output="$(PATH="$STUB_DIR:$PATH" "$SCRIPT" --json --number 1 --body "hello" 2>/dev/null || true)"
  [ "$(echo "$output" | _json_field ok)" = "False" ]
}

@test "missing --body: ok=false" {
  cat > "$STUB_DIR/gh" <<'EOF'
#!/usr/bin/env bash
exit 0
EOF
  chmod +x "$STUB_DIR/gh"
  output="$(PATH="$STUB_DIR:$PATH" "$SCRIPT" --json --repo acme/repo --number 1 --body "" 2>/dev/null || true)"
  [ "$(echo "$output" | _json_field ok)" = "False" ]
}

@test "success path: ok=true" {
  cat > "$STUB_DIR/gh" <<'EOF'
#!/usr/bin/env bash
echo "https://github.com/acme/repo/pull/1#issuecomment-12345"
exit 0
EOF
  chmod +x "$STUB_DIR/gh"
  output="$(PATH="$STUB_DIR:$PATH" "$SCRIPT" --json --repo acme/repo --number 1 --body "LGTM" 2>/dev/null)"
  [ "$(echo "$output" | _json_field ok)" = "True" ]
}

@test "failure path: ok=false" {
  cat > "$STUB_DIR/gh" <<'EOF'
#!/usr/bin/env bash
echo "Error: not found" >&2
exit 1
EOF
  chmod +x "$STUB_DIR/gh"
  output="$(PATH="$STUB_DIR:$PATH" "$SCRIPT" --json --repo acme/repo --number 1 --body "Comment" 2>/dev/null || true)"
  [ "$(echo "$output" | _json_field ok)" = "False" ]
}
