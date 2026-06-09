#!/usr/bin/env bash
# scripts/git-worktree-remove.sh — remove a git worktree (L1, L2, L3)
#
# Usage: git-worktree-remove.sh --worktree-id <id> [--force] [--json]
#
# Outputs L2 envelope on --json:
#   success: {"ok":true,"data":{"worktreeId":"<id>"}}
#   failure: {"ok":false,"error":{"code":"<code>","message":"<msg>"}}
#
# Exit code 0 on ok:true, non-zero on ok:false.
set -euo pipefail

WORKTREE_ID=""
FORCE=false
JSON_MODE=false

while [[ $# -gt 0 ]]; do
  case "$1" in
    --worktree-id) WORKTREE_ID="$2"; shift 2 ;;
    --force)       FORCE=true;       shift   ;;
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

# Parse worktreeId: <projectId>:<worktreeName>
PROJECT_ID="${WORKTREE_ID%%:*}"
WT_NAME="${WORKTREE_ID#*:}"

if [[ -z "$PROJECT_ID" || -z "$WT_NAME" || "$WT_NAME" == "$WORKTREE_ID" ]]; then
  if $JSON_MODE; then json_err "INVALID_INPUT" "malformed worktreeId: $WORKTREE_ID"; else echo "malformed worktreeId" >&2; exit 1; fi
fi

CONFIG_FILE="${ORCHARD_CONFIG:-$HOME/.orchard/config.json}"
REPO_PATH=$(jq -r --arg id "$PROJECT_ID" '.repos[] | select(.slug == $id) | .path' "$CONFIG_FILE" 2>/dev/null || true)
if [[ -z "$REPO_PATH" ]]; then
  if $JSON_MODE; then json_err "REPO_NOT_FOUND" "repo not found: $PROJECT_ID"; else echo "repo not found" >&2; exit 1; fi
fi

# Get the worktree path from git.
# Parse porcelain blocks (worktree / HEAD / branch / blank) with awk so the
# grep -A2 / grep -B1 separator-line bug on macOS is avoided.
WT_PATH=$(git -C "$REPO_PATH" worktree list --porcelain 2>/dev/null \
  | awk -v branch="refs/heads/${WT_NAME}" '
      /^worktree /  { cur = $2 }
      $0 == "branch " branch { print cur; exit }
    ' || true)

if [[ -z "$WT_PATH" ]]; then
  # Already removed — treat as success (idempotent per M5).
  if $JSON_MODE; then json_ok "{\"worktreeId\":\"${WORKTREE_ID}\"}"; else echo "worktree already removed"; fi
  exit 0
fi

FORCE_FLAG=""
if $FORCE; then FORCE_FLAG="--force"; fi

if ! ERR=$(git -C "$REPO_PATH" worktree remove $FORCE_FLAG "$WT_PATH" 2>&1); then
  # Fallback: if the directory no longer exists (deleted out-of-band) or if
  # git worktree remove --force itself failed (locked worktree, submodule
  # gitlinks), fall back to rm -rf + git worktree prune.
  #
  # The path was already confirmed as a registered worktree above, so rm -rf
  # here is safe (we are not removing an arbitrary path).
  if [ -d "$WT_PATH" ]; then
    if ! RM_ERR=$(rm -rf "$WT_PATH" 2>&1); then
      if $JSON_MODE; then json_err "RM_ERROR" "$RM_ERR"; else echo "$RM_ERR" >&2; exit 1; fi
    fi
  fi
  # Always prune after a fallback removal (reconciles stale registration).
  if ! PRUNE_ERR=$(git -C "$REPO_PATH" worktree prune 2>&1); then
    if $JSON_MODE; then json_err "GIT_ERROR" "$PRUNE_ERR"; else echo "$PRUNE_ERR" >&2; exit 1; fi
  fi
fi

if $JSON_MODE; then
  json_ok "{\"worktreeId\":\"${WORKTREE_ID}\"}"
else
  echo "Removed worktree $WT_NAME at $WT_PATH"
fi
