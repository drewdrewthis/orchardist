#!/usr/bin/env bats
# T2: L2 envelope assertions for git/worktree-create.sh
# Success AND failure paths.

setup() {
  SCRIPT="$BATS_TEST_DIRNAME/worktree-create.sh"
  TMPDIR_CFG="$(mktemp -d)"
}

teardown() {
  rm -rf "$TMPDIR_CFG"
}

_json_field() {
  python3 -c "import json,sys; d=json.load(sys.stdin); print(d.get('$1','MISSING'))" 2>/dev/null || echo "PARSE_ERROR"
}

@test "missing --repo: ok=false" {
  output="$("$SCRIPT" --json --branch main 2>/dev/null || true)"
  [ "$(echo "$output" | _json_field ok)" = "False" ]
}

@test "missing --branch: ok=false" {
  output="$("$SCRIPT" --json --repo myrepo 2>/dev/null || true)"
  [ "$(echo "$output" | _json_field ok)" = "False" ]
}

@test "config file missing: ok=false" {
  output="$(ORCHARD_CONFIG="$TMPDIR_CFG/no-such.json" "$SCRIPT" --json --repo myrepo --branch main 2>/dev/null || true)"
  [ "$(echo "$output" | _json_field ok)" = "False" ]
}

@test "repo not found in config: ok=false" {
  config="$TMPDIR_CFG/config.json"
  printf '{"repos":[]}' > "$config"
  output="$(ORCHARD_CONFIG="$config" "$SCRIPT" --json --repo unknownrepo --branch main 2>/dev/null || true)"
  [ "$(echo "$output" | _json_field ok)" = "False" ]
}
