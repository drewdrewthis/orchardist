#!/usr/bin/env bash
# scripts/contracts/add.sh — append a contract-started event to the JSONL log (L1, L2, L3)
#
# Usage:
#   contracts/add.sh --summary <text> --reasoning <text>
#                    [--owner <machine:project:session_id>]
#                    [--source <pointer>]
#                    [--log-dir <path>]
#                    [--json]
#
# Appends a "started" event to the contracts JSONL log.
# Does NOT call the daemon (daemon is read-only per spec constraint 5).
# The daemon watches the log dir via fsnotify and picks up new events.
#
# contract_id format: C-YYYY-MM-DD-<8hex>
#
# L2 envelope:
#   success: {"ok":true,"data":{"contractId":"C-..."}}
#   failure: {"ok":false,"error":{"code":"<code>","message":"<msg>"}}

set -euo pipefail

SUMMARY=""
REASONING=""
OWNER=""
SOURCE=""
LOG_DIR="${ORCHARD_CONTRACTS_DIR:-$HOME/workspace/orchard-codex/contracts}"
JSON_MODE=0

while [[ $# -gt 0 ]]; do
  case "$1" in
    --summary)   SUMMARY="$2";   shift 2 ;;
    --reasoning) REASONING="$2"; shift 2 ;;
    --owner)     OWNER="$2";     shift 2 ;;
    --source)    SOURCE="$2";    shift 2 ;;
    --log-dir)   LOG_DIR="$2";   shift 2 ;;
    --json)      JSON_MODE=1;    shift   ;;
    *)           echo "Unknown arg: $1" >&2; exit 1 ;;
  esac
done

emit_error() {
  local code="$1" msg="$2"
  msg="${msg//\"/\\\"}"
  if [[ "$JSON_MODE" == "1" ]]; then
    printf '{"ok":false,"error":{"code":"%s","message":"%s"}}\n' "$code" "$msg"
  else
    echo "ERROR [$code]: $msg" >&2
  fi
  exit 1
}

emit_ok() {
  local data="$1"
  if [[ "$JSON_MODE" == "1" ]]; then
    printf '{"ok":true,"data":%s}\n' "$data"
  else
    echo "OK: $data"
  fi
}

[[ -z "$SUMMARY" ]]   && emit_error "INVALID_INPUT" "--summary is required"
[[ -z "$REASONING" ]] && emit_error "INVALID_INPUT" "--reasoning is required"

# Generate contract id: C-YYYY-MM-DD-<8hex>
DATE=$(date -u +%Y-%m-%d)
HEX=$(LC_ALL=C tr -dc 'a-f0-9' < /dev/urandom | head -c 8 2>/dev/null || \
      python3 -c "import secrets; print(secrets.token_hex(4))")
CONTRACT_ID="C-${DATE}-${HEX}"

TIMESTAMP=$(date -u +%Y-%m-%dT%H:%M:%SZ)

mkdir -p "$LOG_DIR" || emit_error "LOG_DIR_ERROR" "cannot create log dir: $LOG_DIR"

LOG_FILE="${LOG_DIR}/${CONTRACT_ID}.jsonl"

# Build event JSON via python3 to handle quoting safely.
EVENT=$(python3 -c "
import json, sys

ev = {
    'timestamp':   '${TIMESTAMP}',
    'contract_id': '${CONTRACT_ID}',
    'status':      'started',
    'summary':     sys.argv[1],
    'reasoning':   sys.argv[2],
    'created_by':  'contracts/add.sh',
}
if sys.argv[3]: ev['owner']  = sys.argv[3]
if sys.argv[4]: ev['source'] = sys.argv[4]

print(json.dumps(ev))
" "$SUMMARY" "$REASONING" "$OWNER" "$SOURCE") || emit_error "ENCODE_ERROR" "failed to encode event"

printf '%s\n' "$EVENT" >> "$LOG_FILE" || emit_error "WRITE_ERROR" "failed to write to $LOG_FILE"

emit_ok "{\"contractId\":\"${CONTRACT_ID}\"}"
