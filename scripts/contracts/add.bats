#!/usr/bin/env bats
# T2: L2 envelope assertions for contracts/add.sh
# Success AND failure paths. Uses temp dirs — no daemon required (writes JSONL directly).

setup() {
  SCRIPT="$BATS_TEST_DIRNAME/add.sh"
  TMPDIR_LOG="$(mktemp -d)"
}

teardown() {
  rm -rf "$TMPDIR_LOG"
}

_json_field() {
  python3 -c "import json,sys; d=json.load(sys.stdin); print(d.get('$1','MISSING'))" 2>/dev/null || echo "PARSE_ERROR"
}

# --- success path ---

@test "success path: ok=true and data.contractId present" {
  output="$("$SCRIPT" --json \
    --summary "implement feature X" \
    --reasoning "kicking off work" \
    --log-dir "$TMPDIR_LOG" \
    2>/dev/null)"
  [ "$(echo "$output" | _json_field ok)" = "True" ]
  cid="$(echo "$output" | python3 -c "import json,sys; d=json.load(sys.stdin); print(d.get('data',{}).get('contractId','MISSING'))" 2>/dev/null)"
  [[ "$cid" =~ ^C-[0-9]{4}-[0-9]{2}-[0-9]{2}-[a-f0-9]{8}$ ]]
}

@test "success path: JSONL file is created with started event" {
  output="$("$SCRIPT" --json \
    --summary "implement feature Y" \
    --reasoning "starting now" \
    --owner "local:orchard:test-session" \
    --log-dir "$TMPDIR_LOG" \
    2>/dev/null)"
  [ "$(echo "$output" | _json_field ok)" = "True" ]
  cid="$(echo "$output" | python3 -c "import json,sys; d=json.load(sys.stdin); print(d.get('data',{}).get('contractId',''))" 2>/dev/null)"
  [ -f "$TMPDIR_LOG/${cid}.jsonl" ]
  event_status="$(python3 -c "import json; line=open('$TMPDIR_LOG/${cid}.jsonl').readline(); print(json.loads(line)['status'])")"
  [ "$event_status" = "started" ]
}

@test "success path: event contains summary and reasoning" {
  output="$("$SCRIPT" --json \
    --summary "my summary text" \
    --reasoning "my reasoning text" \
    --log-dir "$TMPDIR_LOG" \
    2>/dev/null)"
  cid="$(echo "$output" | python3 -c "import json,sys; d=json.load(sys.stdin); print(d.get('data',{}).get('contractId',''))" 2>/dev/null)"
  summary_val="$(python3 -c "import json; line=open('$TMPDIR_LOG/${cid}.jsonl').readline(); print(json.loads(line)['summary'])")"
  [ "$summary_val" = "my summary text" ]
}

# --- failure path ---

@test "missing --summary: ok=false with INVALID_INPUT code" {
  output="$("$SCRIPT" --json --reasoning "some reason" --log-dir "$TMPDIR_LOG" 2>/dev/null || true)"
  [ "$(echo "$output" | _json_field ok)" = "False" ]
  code="$(echo "$output" | python3 -c "import json,sys; d=json.load(sys.stdin); print(d.get('error',{}).get('code','MISSING'))" 2>/dev/null)"
  [ "$code" = "INVALID_INPUT" ]
}

@test "missing --reasoning: ok=false with INVALID_INPUT code" {
  output="$("$SCRIPT" --json --summary "my work" --log-dir "$TMPDIR_LOG" 2>/dev/null || true)"
  [ "$(echo "$output" | _json_field ok)" = "False" ]
  code="$(echo "$output" | python3 -c "import json,sys; d=json.load(sys.stdin); print(d.get('error',{}).get('code','MISSING'))" 2>/dev/null)"
  [ "$code" = "INVALID_INPUT" ]
}

@test "unwritable log dir: ok=false with error code" {
  READONLY_DIR="$(mktemp -d)"
  chmod 444 "$READONLY_DIR"
  output="$("$SCRIPT" --json \
    --summary "work" \
    --reasoning "reason" \
    --log-dir "$READONLY_DIR/subdir-that-cannot-be-created" \
    2>/dev/null || true)"
  chmod 755 "$READONLY_DIR"
  rm -rf "$READONLY_DIR"
  [ "$(echo "$output" | _json_field ok)" = "False" ]
}
