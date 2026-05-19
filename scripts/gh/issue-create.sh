#!/usr/bin/env bash
# gh-issue-create.sh — Create a GitHub issue.
#
# L1, L2, L3, L11 — see gh-pr-review.sh for full notes.
# M5: NOT idempotent — creating the same issue twice creates duplicates.
#
# Usage:
#   gh-issue-create.sh --json --repo owner/name --title "text" [--body "text"] [--labels label1,label2]

set -euo pipefail

JSON_MODE=0
REPO=""
TITLE=""
BODY=""
LABELS=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    --json)    JSON_MODE=1; shift ;;
    --repo)    REPO="$2";   shift 2 ;;
    --title)   TITLE="$2";  shift 2 ;;
    --body)    BODY="$2";   shift 2 ;;
    --labels)  LABELS="$2"; shift 2 ;;
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

[[ -z "$REPO" ]]  && emit_error "INVALID_INPUT" "repo is required"
[[ -z "$TITLE" ]] && emit_error "INVALID_INPUT" "title is required"

if ! command -v gh &>/dev/null; then
  emit_error "GH_NOT_INSTALLED" "gh CLI not found on PATH"
fi

ARGS=("issue" "create" "--repo" "$REPO" "--title" "$TITLE")
[[ -n "$BODY" ]]   && ARGS+=("--body" "$BODY")
[[ -n "$LABELS" ]] && ARGS+=("--label" "$LABELS")

# gh issue create outputs the issue URL on success.
if ! output=$(gh "${ARGS[@]}" 2>&1); then
  emit_error "GH_ERROR" "$output"
fi

# Extract issue number from the URL (last path segment).
issue_url=$(echo "$output" | grep -oE 'https://[^ ]+' | head -1 || true)
issue_number=$(echo "$issue_url" | grep -oE '[0-9]+$' || true)

emit_ok "{\"number\":${issue_number:-0},\"url\":\"${issue_url}\"}"
