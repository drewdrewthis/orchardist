#!/usr/bin/env bash
# scripts/contracts/get.sh — get a single contract by id from the orchard daemon (L1, L2, L3)
#
# Usage:
#   contracts/get.sh --id <contract-id> [--endpoint <url>] [--json]
#
# Queries Query.contract(id) via the daemon GraphQL endpoint.
# Returns null data when the contract is unknown (daemon returns null, ok=true).
#
# L2 envelope:
#   found:     {"ok":true,"data":{"contract":{...}}}
#   not found: {"ok":true,"data":{"contract":null}}
#   failure:   {"ok":false,"error":{"code":"<code>","message":"<msg>"}}

set -euo pipefail

CONTRACT_ID=""
ENDPOINT="${ORCHARD_ENDPOINT:-http://127.0.0.1:7777/graphql}"
JSON_MODE=0

while [[ $# -gt 0 ]]; do
  case "$1" in
    --id)       CONTRACT_ID="$2"; shift 2 ;;
    --endpoint) ENDPOINT="$2";    shift 2 ;;
    --json)     JSON_MODE=1;      shift   ;;
    *)          echo "Unknown arg: $1" >&2; exit 1 ;;
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

[[ -z "$CONTRACT_ID" ]] && emit_error "INVALID_INPUT" "--id is required"

QUERY="query { contract(id: \"${CONTRACT_ID}\") {
  id contractId statement status ownerSessionId ownerAgentName
  reportsTo parentContractId createdAt updatedAt lastEventAt criteria
  openQuestions { questionId text askedBy askedAt deadline blocksClose }
} }"

PAYLOAD=$(printf '{"query":"%s"}' "${QUERY//\"/\\\"}")

if ! RESPONSE=$(curl -sf -X POST "$ENDPOINT" \
    -H "Content-Type: application/json" \
    -d "$PAYLOAD" 2>&1); then
  emit_error "DAEMON_UNAVAILABLE" "$RESPONSE"
fi

# Detect GraphQL errors.
if echo "$RESPONSE" | python3 -c "import json,sys; d=json.load(sys.stdin); sys.exit(0 if not d.get('errors') else 1)" 2>/dev/null; then
  DATA=$(echo "$RESPONSE" | python3 -c "import json,sys; d=json.load(sys.stdin); print(json.dumps({'contract': d['data']['contract']}))" 2>/dev/null) || \
    emit_error "PARSE_ERROR" "unexpected response shape"
  emit_ok "$DATA"
else
  ERRMSG=$(echo "$RESPONSE" | python3 -c "import json,sys; d=json.load(sys.stdin); print(d['errors'][0]['message'])" 2>/dev/null || echo "GraphQL error")
  emit_error "GRAPHQL_ERROR" "$ERRMSG"
fi
