#!/usr/bin/env bash
# gh-pr-label.sh — Apply labels to a GitHub pull request.
#
# L1, L2, L3, L11 — see gh-pr-review.sh for full notes.
# M5: Idempotent — applying a label that's already present is a no-op (gh handles it).
#
# Usage:
#   gh-pr-label.sh --json --repo owner/name --number N --labels label1,label2

set -euo pipefail

JSON_MODE=0
REPO=""
NUMBER=""
LABELS=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    --json)    JSON_MODE=1; shift ;;
    --repo)    REPO="$2";   shift 2 ;;
    --number)  NUMBER="$2"; shift 2 ;;
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

[[ -z "$REPO" ]]   && emit_error "INVALID_INPUT" "repo is required"
[[ -z "$NUMBER" ]] && emit_error "INVALID_INPUT" "number is required"
[[ -z "$LABELS" ]] && emit_error "INVALID_INPUT" "labels is required"

if ! command -v gh &>/dev/null; then
  emit_error "GH_NOT_INSTALLED" "gh CLI not found on PATH"
fi

# Split labels on comma and apply each.
IFS=',' read -ra LABEL_ARRAY <<< "$LABELS"
applied=()

for label in "${LABEL_ARRAY[@]}"; do
  label="${label// /}"  # trim spaces
  [[ -z "$label" ]] && continue
  if ! gh pr edit --repo "$REPO" "$NUMBER" --add-label "$label" 2>/dev/null; then
    emit_error "GH_ERROR" "failed to apply label '$label' to $REPO#$NUMBER"
  fi
  applied+=("$label")
done

# Build JSON array of applied labels.
labels_json="["
for i in "${!applied[@]}"; do
  [[ $i -gt 0 ]] && labels_json+=","
  labels_json+="\"${applied[$i]}\""
done
labels_json+="]"

emit_ok "{\"applied_labels\":$labels_json}"
