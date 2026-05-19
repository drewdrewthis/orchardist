#!/usr/bin/env bats
# T2: L2 envelope assertions for git/pull.sh
# Success AND failure paths.

setup() {
  SCRIPT="$BATS_TEST_DIRNAME/pull.sh"
  TMPDIR_CFG="$(mktemp -d)"
}

teardown() {
  rm -rf "$TMPDIR_CFG"
}

_json_field() {
  python3 -c "import json,sys; d=json.load(sys.stdin); print(d.get('$1','MISSING'))" 2>/dev/null || echo "PARSE_ERROR"
}

@test "missing --worktree-id: ok=false" {
  output="$("$SCRIPT" --json 2>/dev/null || true)"
  [ "$(echo "$output" | _json_field ok)" = "False" ]
}

@test "repo not found in config: ok=false" {
  config="$TMPDIR_CFG/config.json"
  printf '{"repos":[]}' > "$config"
  output="$(ORCHARD_CONFIG="$config" "$SCRIPT" --json --worktree-id "unknownrepo:main" 2>/dev/null || true)"
  [ "$(echo "$output" | _json_field ok)" = "False" ]
}
