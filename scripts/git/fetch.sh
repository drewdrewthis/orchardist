#!/usr/bin/env bash
# scripts/git-fetch.sh — fetch from remote (L1, L2, L3)
#
# Usage: git-fetch.sh --worktree-id <id> [--remote <remote>] [--json]
#
# L2 envelope:
#   success: {"ok":true,"data":{"worktreeId":"<id>","remote":"<remote>"}}
#   failure: {"ok":false,"error":{"code":"<code>","message":"<msg>"}}
set -euo pipefail

WORKTREE_ID=""
REMOTE="origin"
JSON_MODE=false

while [[ $# -gt 0 ]]; do
  case "$1" in
    --worktree-id) WORKTREE_ID="$2"; shift 2 ;;
    --remote)      REMOTE="$2";      shift 2 ;;
    --json)        JSON_MODE=true;   shift   ;;
    *)             echo "Unknown argument: $1" >&2; exit 2 ;;
  esac
done

json_ok() {
  local data="$1"
  echo "{\"ok\":true,\"data\":${data}}"
}

json_err() {
  local code="$1" msg="$2"
  msg="${msg//\"/\\\"}"
  echo "{\"ok\":false,\"error\":{\"code\":\"${code}\",\"message\":\"${msg}\"}}"
  exit 1
}

if [[ -z "$WORKTREE_ID" ]]; then
  if $JSON_MODE; then json_err "INVALID_INPUT" "worktreeId is required"; else echo "worktreeId is required" >&2; exit 1; fi
fi

PROJECT_ID="${WORKTREE_ID%%:*}"
WT_NAME="${WORKTREE_ID#*:}"

CONFIG_FILE="${ORCHARD_CONFIG:-$HOME/.orchard/config.json}"
REPO_PATH=$(jq -r --arg id "$PROJECT_ID" '.repos[] | select(.slug == $id) | .path' "$CONFIG_FILE" 2>/dev/null || true)
if [[ -z "$REPO_PATH" ]]; then
  if $JSON_MODE; then json_err "REPO_NOT_FOUND" "repo not found: $PROJECT_ID"; else echo "repo not found" >&2; exit 1; fi
fi

# Determine the worktree path. Main worktree uses repo root.
if [[ "$WT_NAME" == "main" ]]; then
  WT_PATH="$REPO_PATH"
else
  WT_PATH=$(git -C "$REPO_PATH" worktree list --porcelain 2>/dev/null \
    | awk '/^worktree /{path=$2} /^branch refs\/heads\/'"${WT_NAME}"'/{print path; exit}' || true)
fi

if [[ -z "$WT_PATH" ]]; then
  WT_PATH="$REPO_PATH"
fi

if ! ERR=$(git -C "$WT_PATH" fetch "$REMOTE" 2>&1); then
  if $JSON_MODE; then json_err "GIT_ERROR" "$ERR"; else echo "$ERR" >&2; exit 1; fi
fi

if $JSON_MODE; then
  json_ok "{\"worktreeId\":\"${WORKTREE_ID}\",\"remote\":\"${REMOTE}\"}"
else
  echo "Fetched $REMOTE in worktree $WT_NAME"
fi
