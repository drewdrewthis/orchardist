#!/usr/bin/env bash
# emit-sentinel.sh — render one `orchard_contract` sentinel as a single JSON
# line on stdout. The single source of truth for the on-disk shape: every
# caller (the /open-contract and /close-contract skills, the SessionStart auto-
# open hook) routes through this script. `printf` (not `echo`) so the format
# is one well-defined substitution; values are passed positionally and never
# interpolated into a string the shell parses.
#
# Usage:
#   emit-sentinel.sh open  <id> <statement>
#   emit-sentinel.sh close <id> <reason>
#
# `<statement>` and `<reason>` must be JSON-string-safe — the script JSON-
# escapes the typical hazards (backslash, double-quote, control chars).
# Timestamp is an RFC 3339 UTC stamp captured at the moment of emit.

set -uo pipefail

verb="${1:-}"
id="${2:-}"
body="${3:-}"
[ -n "$verb" ] && [ -n "$id" ] && [ -n "$body" ] || {
  printf 'usage: %s open|close <id> <statement-or-reason>\n' "$(basename "$0")" >&2
  exit 2
}

# JSON-escape: backslash → \\, double-quote → \", literal newline → \n.
# Other control chars are rare in our values and remain unescaped; if a
# caller ever needs them, switch this to a jq-based escape.
_json_escape() {
  local s="$1"
  s="${s//\\/\\\\}"
  s="${s//\"/\\\"}"
  s="${s//$'\n'/\\n}"
  printf '%s' "$s"
}

ts=$(date -u +%Y-%m-%dT%H:%M:%SZ)
body_esc=$(_json_escape "$body")
id_esc=$(_json_escape "$id")

case "$verb" in
  open)
    printf '{"orchard_contract":"open","id":"%s","statement":"%s","ts":"%s"}\n' \
      "$id_esc" "$body_esc" "$ts"
    ;;
  close)
    printf '{"orchard_contract":"close","id":"%s","reason":"%s","ts":"%s"}\n' \
      "$id_esc" "$body_esc" "$ts"
    ;;
  *)
    printf 'unknown verb: %s (want open|close)\n' "$verb" >&2
    exit 2
    ;;
esac
