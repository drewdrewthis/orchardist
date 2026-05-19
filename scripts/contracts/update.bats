#!/usr/bin/env bats
# T2: L2 envelope assertions for contracts/update.sh
# Success AND failure paths. Uses temp dirs — no daemon required (writes JSONL directly).

setup() {
  SCRIPT="$BATS_TEST_DIRNAME/update.sh"
  TMPDIR_LOG="$(mktemp -d)"
  # Seed a contract JSONL file so update.sh can find it.
  CONTRACT_ID="C-2026-01-01-deadbeef"
  printf '{"timestamp":"2026-01-01T00:00:00Z","contract_id":"%s","status":"started","summary":"seed contract","reasoning":"setup","created_by":"test"}\n' \
    "$CONTRACT_ID" > "$TMPDIR_LOG/${CONTRACT_ID}.jsonl"
}

teardown() {
  rm -rf "$TMPDIR_LOG"
}

_json_field() {
  python3 -c "import json,sys; d=json.load(sys.stdin); print(d.get('$1','MISSING'))" 2>/dev/null || echo "PARSE_ERROR"
}

# --- success path ---

@test "update to blocked: ok=true and data.status=blocked" {
  output="$("$SCRIPT" --json \
    --id "$CONTRACT_ID" \
    --status blocked \
    --reasoning "waiting on PR review" \
    --log-dir "$TMPDIR_LOG" \
    2>/dev/null)"
  [ "$(echo "$output" | _json_field ok)" = "True" ]
  status_val="$(echo "$output" | python3 -c "import json,sys; d=json.load(sys.stdin); print(d.get('data',{}).get('status','MISSING'))" 2>/dev/null)"
  [ "$status_val" = "blocked" ]
}

@test "update to delivered: ok=true and new event appended to JSONL" {
  output="$("$SCRIPT" --json \
    --id "$CONTRACT_ID" \
    --status delivered \
    --reasoning "shipped" \
    --log-dir "$TMPDIR_LOG" \
    2>/dev/null)"
  [ "$(echo "$output" | _json_field ok)" = "True" ]
  line_count="$(wc -l < "$TMPDIR_LOG/${CONTRACT_ID}.jsonl")"
  [ "$line_count" -eq 2 ]
  last_status="$(tail -1 "$TMPDIR_LOG/${CONTRACT_ID}.jsonl" | python3 -c "import json,sys; print(json.load(sys.stdin)['status'])")"
  [ "$last_status" = "delivered" ]
}

@test "update with --owner: event contains owner field" {
  output="$("$SCRIPT" --json \
    --id "$CONTRACT_ID" \
    --status started \
    --reasoning "handoff" \
    --owner "boxd:orchard:new-session-uuid" \
    --log-dir "$TMPDIR_LOG" \
    2>/dev/null)"
  [ "$(echo "$output" | _json_field ok)" = "True" ]
  owner_val="$(tail -1 "$TMPDIR_LOG/${CONTRACT_ID}.jsonl" | python3 -c "import json,sys; print(json.load(sys.stdin).get('owner','MISSING'))")"
  [ "$owner_val" = "boxd:orchard:new-session-uuid" ]
}

# --- failure path ---

@test "missing --id: ok=false with INVALID_INPUT code" {
  output="$("$SCRIPT" --json --status started --reasoning "reason" --log-dir "$TMPDIR_LOG" 2>/dev/null || true)"
  [ "$(echo "$output" | _json_field ok)" = "False" ]
  code="$(echo "$output" | python3 -c "import json,sys; d=json.load(sys.stdin); print(d.get('error',{}).get('code','MISSING'))" 2>/dev/null)"
  [ "$code" = "INVALID_INPUT" ]
}

@test "missing --status: ok=false with INVALID_INPUT code" {
  output="$("$SCRIPT" --json --id "$CONTRACT_ID" --reasoning "reason" --log-dir "$TMPDIR_LOG" 2>/dev/null || true)"
  [ "$(echo "$output" | _json_field ok)" = "False" ]
  code="$(echo "$output" | python3 -c "import json,sys; d=json.load(sys.stdin); print(d.get('error',{}).get('code','MISSING'))" 2>/dev/null)"
  [ "$code" = "INVALID_INPUT" ]
}

@test "missing --reasoning: ok=false with INVALID_INPUT code" {
  output="$("$SCRIPT" --json --id "$CONTRACT_ID" --status started --log-dir "$TMPDIR_LOG" 2>/dev/null || true)"
  [ "$(echo "$output" | _json_field ok)" = "False" ]
  code="$(echo "$output" | python3 -c "import json,sys; d=json.load(sys.stdin); print(d.get('error',{}).get('code','MISSING'))" 2>/dev/null)"
  [ "$code" = "INVALID_INPUT" ]
}

@test "invalid --status value: ok=false with INVALID_STATUS code" {
  output="$("$SCRIPT" --json \
    --id "$CONTRACT_ID" \
    --status OPEN \
    --reasoning "reason" \
    --log-dir "$TMPDIR_LOG" \
    2>/dev/null || true)"
  [ "$(echo "$output" | _json_field ok)" = "False" ]
  code="$(echo "$output" | python3 -c "import json,sys; d=json.load(sys.stdin); print(d.get('error',{}).get('code','MISSING'))" 2>/dev/null)"
  [ "$code" = "INVALID_STATUS" ]
}

@test "contract not found: ok=false with CONTRACT_NOT_FOUND code" {
  output="$("$SCRIPT" --json \
    --id "C-2000-01-01-00000000" \
    --status delivered \
    --reasoning "done" \
    --log-dir "$TMPDIR_LOG" \
    2>/dev/null || true)"
  [ "$(echo "$output" | _json_field ok)" = "False" ]
  code="$(echo "$output" | python3 -c "import json,sys; d=json.load(sys.stdin); print(d.get('error',{}).get('code','MISSING'))" 2>/dev/null)"
  [ "$code" = "CONTRACT_NOT_FOUND" ]
}
