#!/usr/bin/env bats
# Tests for collapse-statement.sh — the shared normalization helper used by
# both the SessionStart hook and the bats tests. Single source of truth: if
# this script changes, hook and tests get the new behavior together.

setup() {
  SCRIPT="$BATS_TEST_DIRNAME/collapse-statement.sh"
  TMP_FILE="$(mktemp)"
}

teardown() {
  rm -f "$TMP_FILE"
}

@test "collapses a single-line file to itself (no trailing newline drift)" {
  printf 'hello world' > "$TMP_FILE"
  run bash "$SCRIPT" "$TMP_FILE"
  [ "$status" -eq 0 ]
  [ "$output" = "hello world" ]
}

@test "collapses multi-line into space-separated single line" {
  printf 'line one\nline two\nline three\n' > "$TMP_FILE"
  run bash "$SCRIPT" "$TMP_FILE"
  [ "$status" -eq 0 ]
  [ "$output" = "line one line two line three" ]
}

@test "collapses runs of whitespace to a single space" {
  printf 'a     b\n\n\n   c\n' > "$TMP_FILE"
  run bash "$SCRIPT" "$TMP_FILE"
  [ "$status" -eq 0 ]
  [ "$output" = "a b c" ]
}

@test "strips trailing whitespace" {
  printf 'sentence with trailing   \n' > "$TMP_FILE"
  run bash "$SCRIPT" "$TMP_FILE"
  [ "$status" -eq 0 ]
  [ "$output" = "sentence with trailing" ]
}

@test "strips leading whitespace (newlines and spaces)" {
  # A statement file starting with a blank line would otherwise produce a
  # leading-space sentinel — the single-line shape lock in on-session-start.bats
  # does NOT catch this (awk counts content lines, not leading whitespace).
  printf '\n\n   leading garbage\n' > "$TMP_FILE"
  run bash "$SCRIPT" "$TMP_FILE"
  [ "$status" -eq 0 ]
  [ "$output" = "leading garbage" ]
}

@test "normalizes tabs into spaces" {
  # A statement file with tabs would otherwise ship tab characters in the
  # sentinel JSON value. The shape lock catches some structural starters but
  # not embedded tabs — the collapse helper must normalize them.
  printf 'word1\tword2\t\tword3\n' > "$TMP_FILE"
  run bash "$SCRIPT" "$TMP_FILE"
  [ "$status" -eq 0 ]
  [ "$output" = "word1 word2 word3" ]
}

@test "normalizes CRLF line endings" {
  # A Windows-edited statement file would otherwise leak \r control chars
  # into the sentinel JSON. Strip \r before the newline collapse so the
  # round-trip is clean.
  printf 'line one\r\nline two\r\n' > "$TMP_FILE"
  run bash "$SCRIPT" "$TMP_FILE"
  [ "$status" -eq 0 ]
  [ "$output" = "line one line two" ]
}

@test "fails with usage when file argument is missing" {
  run bash "$SCRIPT"
  [ "$status" -eq 1 ]
  [[ "$output" == *"usage"* ]]
}

@test "fails with usage when file does not exist" {
  run bash "$SCRIPT" "/nonexistent/path/$$"
  [ "$status" -eq 1 ]
  [[ "$output" == *"usage"* ]]
}

@test "hook and tests share this collapse helper (no co-drift surface)" {
  # The hook and the bats test setup both shell out to this script.
  # Verify they both reference it so a future edit doesn't accidentally
  # reintroduce inline `tr | sed` and reopen the co-drift gap.
  hook="$BATS_TEST_DIRNAME/../hooks/on-session-start.sh"
  test_file="$BATS_TEST_DIRNAME/../hooks/on-session-start.bats"
  grep -q 'collapse-statement.sh' "$hook"
  grep -q 'collapse-statement.sh' "$test_file"
}
