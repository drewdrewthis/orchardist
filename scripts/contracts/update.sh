#!/usr/bin/env bash
# scripts/contracts/update.sh — append a status-update event to the JSONL log (L1, L2, L3)
#
# Usage:
#   contracts/update.sh --id <contract-id> --status <STATUS> --reasoning <text>
#                       [--owner <machine:project:session_id>]
#                       [--source <pointer>]
#                       [--log-dir <path>]
#                       [--json]
#
# Appends a status-update event to the contract's JSONL log file.
# Does NOT call the daemon (daemon is read-only per spec constraint 5).
#
# STATUS values: started | blocked | delivered
#
# L2 envelope:
#   success: {"ok":true,"data":{"contractId":"C-...","status":"<STATUS>"}}
#   failure: {"ok":false,"error":{"code":"<code>","message":"<msg>"}}

set -euo pipefail

CONTRACT_ID=""
STATUS=""
REASONING=""
OWNER=""
SOURCE=""
LOG_DIR="${ORCHARD_CONTRACTS_DIR:-$HOME/workspace/orchard-codex/contracts}"
JSON_MODE=0

while [[ $# -gt 0 ]]; do
  case "$1" in
    --id)        CONTRACT_ID="$2"; shift 2 ;;
    --status)    STATUS="$2";      shift 2 ;;
    --reasoning) REASONING="$2";   shift 2 ;;
    --owner)     OWNER="$2";       shift 2 ;;
    --source)    SOURCE="$2";      shift 2 ;;
    --log-dir)   LOG_DIR="$2";     shift 2 ;;
    --json)      JSON_MODE=1;      shift   ;;
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

[[ -z "$CONTRACT_ID" ]] && emit_error "INVALID_INPUT" "--id is required"
[[ -z "$STATUS" ]]      && emit_error "INVALID_INPUT" "--status is required"
[[ -z "$REASONING" ]]   && emit_error "INVALID_INPUT" "--reasoning is required"

# Validate status values (spec §Status semantics — three values only).
case "$STATUS" in
  started|blocked|delivered) ;;
  *) emit_error "INVALID_STATUS" "status must be started, blocked, or delivered; got: $STATUS" ;;
esac

LOG_FILE="${LOG_DIR}/${CONTRACT_ID}.jsonl"

if [[ ! -f "$LOG_FILE" ]]; then
  emit_error "CONTRACT_NOT_FOUND" "no JSONL file for contract: $CONTRACT_ID (expected $LOG_FILE)"
fi

TIMESTAMP=$(date -u +%Y-%m-%dT%H:%M:%SZ)

EVENT=$(python3 -c "
import json, sys

ev = {
    'timestamp':   '${TIMESTAMP}',
    'contract_id': '${CONTRACT_ID}',
    'status':      '${STATUS}',
    'reasoning':   sys.argv[1],
    'created_by':  'contracts/update.sh',
}
if sys.argv[2]: ev['owner']  = sys.argv[2]
if sys.argv[3]: ev['source'] = sys.argv[3]

print(json.dumps(ev))
" "$REASONING" "$OWNER" "$SOURCE") || emit_error "ENCODE_ERROR" "failed to encode event"

printf '%s\n' "$EVENT" >> "$LOG_FILE" || emit_error "WRITE_ERROR" "failed to write to $LOG_FILE"

emit_ok "{\"contractId\":\"${CONTRACT_ID}\",\"status\":\"${STATUS}\"}"
