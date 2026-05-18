#!/usr/bin/env bash
# gh-pr-review.sh — Submit a review on a GitHub pull request.
#
# L1: canonical operation; daemon and CLI are wrappers over this script.
# L2: --json flag emits {"ok":bool,"data"?:...,"error"?:{...}}.
# L3: bash for portability; gh CLI for the actual API call.
# L11: does NOT call the daemon (scripts are leaves).
#
# Usage:
#   gh-pr-review.sh --json --repo owner/name --number N --event APPROVE|REQUEST_CHANGES|COMMENT [--body "text"]
#
# M5: NOT idempotent — submitting the same review twice creates a duplicate.

set -euo pipefail

JSON_MODE=0
REPO=""
NUMBER=""
EVENT=""
BODY=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    --json)       JSON_MODE=1; shift ;;
    --repo)       REPO="$2";   shift 2 ;;
    --number)     NUMBER="$2"; shift 2 ;;
    --event)      EVENT="$2";  shift 2 ;;
    --body)       BODY="$2";   shift 2 ;;
    *)            echo "Unknown arg: $1" >&2; exit 1 ;;
  esac
done

emit_error() {
  local code="$1" msg="$2"
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

# M4 equivalent: validate inputs.
[[ -z "$REPO" ]]   && emit_error "INVALID_INPUT" "repo is required"
[[ -z "$NUMBER" ]] && emit_error "INVALID_INPUT" "number is required"
[[ -z "$EVENT" ]]  && emit_error "INVALID_INPUT" "event is required"

case "$EVENT" in
  APPROVE|REQUEST_CHANGES|COMMENT) ;;
  *) emit_error "INVALID_INPUT" "event must be APPROVE, REQUEST_CHANGES, or COMMENT; got $EVENT" ;;
esac

# Verify gh is installed (L11: no daemon call).
if ! command -v gh &>/dev/null; then
  emit_error "GH_NOT_INSTALLED" "gh CLI not found on PATH"
fi

# Build gh pr review args.
ARGS=("pr" "review" "--repo" "$REPO" "$NUMBER")
case "$EVENT" in
  APPROVE)          ARGS+=("--approve") ;;
  REQUEST_CHANGES)  ARGS+=("--request-changes") ;;
  COMMENT)          ARGS+=("--comment") ;;
esac
[[ -n "$BODY" ]] && ARGS+=("--body" "$BODY")

# Execute. gh pr review outputs the review URL on stdout.
if ! output=$(gh "${ARGS[@]}" 2>&1); then
  emit_error "GH_ERROR" "$output"
fi

# gh pr review doesn't easily give us the review ID; emit the URL/output.
review_id=$(echo "$output" | grep -oE '[0-9]+$' | tail -1 || true)
emit_ok "{\"review_id\":\"${review_id}\",\"output\":$(printf '%s' "$output" | python3 -c 'import json,sys; print(json.dumps(sys.stdin.read()))')}"
