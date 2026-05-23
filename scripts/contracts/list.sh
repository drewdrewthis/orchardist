#!/usr/bin/env bash
# scripts/contracts/list.sh — list contracts from the orchard daemon (L1, L2, L3)
#
# Usage:
#   contracts/list.sh [--status <STATUS>] [--owner-session <id>] [--owner-agent <name>]
#                     [--endpoint <url>] [--json]
#
# Queries Query.contracts via the daemon GraphQL endpoint.
# Filter flags are optional and combined (AND semantics — daemon applies).
#
# L2 envelope:
#   success: {"ok":true,"data":{"contracts":[...]}}
#   failure: {"ok":false,"error":{"code":"<code>","message":"<msg>"}}
#
# STATUS values: OPEN DELIVERED_PENDING_VALIDATION DELIVERED_PENDING_PARENT_VALIDATION
#                PENDING_USER_APPROVAL AWAITING_CANCEL_ACK WAITING_EXTERNAL
#                SATISFIED CANCELLED JUDGE_REJECTED_TERMINAL

set -euo pipefail

STATUS=""
OWNER_SESSION=""
OWNER_AGENT=""
ENDPOINT="${ORCHARD_ENDPOINT:-http://127.0.0.1:7777/graphql}"
JSON_MODE=0

while [[ $# -gt 0 ]]; do
  case "$1" in
    --status)        STATUS="$2";        shift 2 ;;
    --owner-session) OWNER_SESSION="$2"; shift 2 ;;
    --owner-agent)   OWNER_AGENT="$2";   shift 2 ;;
    --endpoint)      ENDPOINT="$2";      shift 2 ;;
    --json)          JSON_MODE=1;        shift   ;;
    *)               echo "Unknown arg: $1" >&2; exit 1 ;;
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
    echo "$data"
  fi
}

if ! command -v curl &>/dev/null; then
  emit_error "CURL_NOT_FOUND" "curl not found on PATH"
fi

# Build GraphQL filter argument.
FILTER_FIELDS=""
[[ -n "$STATUS" ]]        && FILTER_FIELDS="${FILTER_FIELDS}statuses: [${STATUS}],"
[[ -n "$OWNER_SESSION" ]] && FILTER_FIELDS="${FILTER_FIELDS}ownerSessionId: \"${OWNER_SESSION}\","
[[ -n "$OWNER_AGENT" ]]   && FILTER_FIELDS="${FILTER_FIELDS}ownerAgentName: \"${OWNER_AGENT}\","

if [[ -n "$FILTER_FIELDS" ]]; then
  FILTER_ARG="filter: {${FILTER_FIELDS%,}}"
else
  FILTER_ARG=""
fi

QUERY="query { contracts($FILTER_ARG) {
  id contractId statement status ownerSessionId ownerAgentName
  createdAt updatedAt lastEventAt criteria
  openQuestions { questionId text askedBy askedAt blocksClose }
} }"

PAYLOAD=$(printf '{"query":"%s"}' "${QUERY//\"/\\\"}")

if ! RESPONSE=$(curl -sf -X POST "$ENDPOINT" \
    -H "Content-Type: application/json" \
    -d "$PAYLOAD" 2>&1); then
  emit_error "DAEMON_UNAVAILABLE" "$RESPONSE"
fi

# Detect GraphQL errors.
if echo "$RESPONSE" | python3 -c "import json,sys; d=json.load(sys.stdin); sys.exit(0 if not d.get('errors') else 1)" 2>/dev/null; then
  DATA=$(echo "$RESPONSE" | python3 -c "import json,sys; d=json.load(sys.stdin); print(json.dumps({'contracts': d['data']['contracts']}))" 2>/dev/null) || \
    emit_error "PARSE_ERROR" "unexpected response shape"
  emit_ok "$DATA"
else
  ERRMSG=$(echo "$RESPONSE" | python3 -c "import json,sys; d=json.load(sys.stdin); print(d['errors'][0]['message'])" 2>/dev/null || echo "GraphQL error")
  emit_error "GRAPHQL_ERROR" "$ERRMSG"
fi
