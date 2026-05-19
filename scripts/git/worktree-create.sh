#!/usr/bin/env bash
# scripts/git-worktree-create.sh — create a git worktree (L1, L2, L3)
#
# Usage: git-worktree-create.sh --repo <repoId> --branch <branch> [--path <path>] [--json]
#
# Outputs L2 envelope on --json:
#   success: {"ok":true,"data":{"worktreeId":"<id>","path":"<path>"}}
#   failure: {"ok":false,"error":{"code":"<code>","message":"<msg>"}}
#
# Exit code 0 on ok:true, non-zero on ok:false.
set -euo pipefail

REPO_ID=""
BRANCH=""
WORKTREE_PATH=""
JSON_MODE=false

while [[ $# -gt 0 ]]; do
  case "$1" in
    --repo)     REPO_ID="$2";        shift 2 ;;
    --branch)   BRANCH="$2";         shift 2 ;;
    --path)     WORKTREE_PATH="$2";  shift 2 ;;
    --json)     JSON_MODE=true;      shift   ;;
    *)          echo "Unknown argument: $1" >&2; exit 2 ;;
  esac
done

json_ok() {
  local data="$1"
  echo "{\"ok\":true,\"data\":${data}}"
}

json_err() {
  local code="$1" msg="$2"
  # Escape double quotes in msg
  msg="${msg//\"/\\\"}"
  echo "{\"ok\":false,\"error\":{\"code\":\"${code}\",\"message\":\"${msg}\"}}"
  exit 1
}

if [[ -z "$REPO_ID" ]]; then
  if $JSON_MODE; then json_err "INVALID_INPUT" "repo is required"; else echo "repo is required" >&2; exit 1; fi
fi
if [[ -z "$BRANCH" ]]; then
  if $JSON_MODE; then json_err "INVALID_INPUT" "branch is required"; else echo "branch is required" >&2; exit 1; fi
fi

# Resolve repo path from orchard config.
CONFIG_FILE="${ORCHARD_CONFIG:-$HOME/.orchard/config.json}"
if [[ ! -f "$CONFIG_FILE" ]]; then
  if $JSON_MODE; then json_err "CONFIG_NOT_FOUND" "config file not found: $CONFIG_FILE"; else echo "config not found" >&2; exit 1; fi
fi

REPO_PATH=$(jq -r --arg id "$REPO_ID" '.repos[] | select(.slug == $id) | .path' "$CONFIG_FILE" 2>/dev/null || true)
if [[ -z "$REPO_PATH" ]]; then
  if $JSON_MODE; then json_err "REPO_NOT_FOUND" "repo not found: $REPO_ID"; else echo "repo not found: $REPO_ID" >&2; exit 1; fi
fi

# Default worktree path: sibling directory of the main checkout.
if [[ -z "$WORKTREE_PATH" ]]; then
  PARENT_DIR="$(dirname "$REPO_PATH")"
  REPO_NAME="$(basename "$REPO_PATH")"
  SAFE_BRANCH="${BRANCH//\//-}"
  WORKTREE_PATH="$PARENT_DIR/${REPO_NAME}-worktrees/${SAFE_BRANCH}"
fi

if ! ERR=$(git -C "$REPO_PATH" worktree add "$WORKTREE_PATH" -b "$BRANCH" 2>&1); then
  if $JSON_MODE; then json_err "GIT_ERROR" "$ERR"; else echo "$ERR" >&2; exit 1; fi
fi

if $JSON_MODE; then
  WID="${REPO_ID}:${BRANCH//\//-}"
  json_ok "{\"worktreeId\":\"${WID}\",\"path\":\"${WORKTREE_PATH}\"}"
else
  echo "Created worktree at $WORKTREE_PATH"
fi
