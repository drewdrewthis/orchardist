#!/usr/bin/env bash
# gh-pr-comment.sh — Post a comment on a GitHub pull request.
#
# L1, L2, L3, L11 — see gh-pr-review.sh for full notes.
# M5: NOT idempotent — posting the same comment twice creates a duplicate.
#
# Usage:
#   gh-pr-comment.sh --json --repo owner/name --number N --body "text"

set -euo pipefail

JSON_MODE=0
REPO=""
NUMBER=""
BODY=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    --json)    JSON_MODE=1; shift ;;
    --repo)    REPO="$2";   shift 2 ;;
    --number)  NUMBER="$2"; shift 2 ;;
    --body)    BODY="$2";   shift 2 ;;
    *)         echo "Unknown arg: $1" >&2; exit 1 ;;
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

[[ -z "$REPO" ]]   && emit_error "INVALID_INPUT" "repo is required"
[[ -z "$NUMBER" ]] && emit_error "INVALID_INPUT" "number is required"
[[ -z "$BODY" ]]   && emit_error "INVALID_INPUT" "body is required"

if ! command -v gh &>/dev/null; then
  emit_error "GH_NOT_INSTALLED" "gh CLI not found on PATH"
fi

# Post the comment. gh pr comment outputs the comment URL.
if ! output=$(gh pr comment --repo "$REPO" "$NUMBER" --body "$BODY" 2>&1); then
  emit_error "GH_ERROR" "$output"
fi

# Extract comment ID from URL if present (e.g. .../issues/comments/12345)
comment_id=$(echo "$output" | grep -oE 'comments/[0-9]+' | grep -oE '[0-9]+' | tail -1 || true)
created_at=$(date -u +"%Y-%m-%dT%H:%M:%SZ")

emit_ok "{\"comment_id\":\"${comment_id}\",\"created_at\":\"${created_at}\"}"
