#!/usr/bin/env bash
# scripts/git/worktree-remove.sh — remove a git worktree (L1, L2, L3)
#
# Usage:
#   worktree-remove.sh --worktree-id <id> [--force] \
#     [--pr-merged <merged|not-merged|unknown>] \
#     [--base <base-branch>] \
#     [--upstream <remote-tracking-ref>] \
#     [--protected <branch1,branch2,...>] \
#     [--json]
#
# Outputs L2 envelope on --json:
#   success: {"ok":true,"data":{"worktreeId":"<id>","branchDelete":{...},"dockerTeardown":{...}}}
#   failure: {"ok":false,"error":{"code":"<code>","message":"<msg>"}}
#
# branchDelete is the result from scripts/git/branch-delete.sh:
#   deleted:  {"branch":"<n>","deleted":true}
#   skipped:  {"branch":"<n>","deleted":false,"skipReason":"<reason>","warning":"<msg>"}
# A branch-delete skip or error is NON-FATAL: the worktree/dir removal result
# is still ok:true; only the branchDelete sub-object reflects the skip.
#
# dockerTeardown is the result from scripts/git/docker-teardown.sh (AC5/AC6):
#   ran:   {"worktreeId":"<id>","projectKey":"<k>","action":"down"}
#   no-op: {"worktreeId":"<id>","action":"no-op","reason":"<reason>"}
# A docker-teardown error is NON-FATAL (same policy as branchDelete).
# Stage ordering (CRITICAL): docker-teardown runs BEFORE dir-removal so that
# the compose file is still on disk when docker compose reads it.
#
# Exit code 0 on ok:true, non-zero on ok:false.
set -euo pipefail

WORKTREE_ID=""
FORCE=false
JSON_MODE=false
# Branch-delete arguments (Step 3 — AC4 + AC-G2)
PR_MERGED=""
BASE_BRANCH=""
UPSTREAM=""
PROTECTED=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    --worktree-id) WORKTREE_ID="$2"; shift 2 ;;
    --force)       FORCE=true;       shift   ;;
    --json)        JSON_MODE=true;   shift   ;;
    --pr-merged)   PR_MERGED="$2";   shift 2 ;;
    --base)        BASE_BRANCH="$2"; shift 2 ;;
    --upstream)    UPSTREAM="$2";    shift 2 ;;
    --protected)   PROTECTED="$2";   shift 2 ;;
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

# ---- Stage 1a: docker compose teardown (AC5 + AC6) -------------------------
#
# MUST run BEFORE the directory is removed: the compose file lives inside
# WT_PATH and docker compose reads it from disk.  A skip or error here is
# NON-FATAL; we record the result and continue.
SCRIPT_DIR_DT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DT_SCRIPT="${SCRIPT_DIR_DT}/docker-teardown.sh"
DOCKER_TEARDOWN_DATA="null"

if [[ -f "$DT_SCRIPT" && -d "$WT_PATH" ]]; then
  DT_ARGS=(
    "--worktree-dir" "$WT_PATH"
    "--worktree-id"  "$WORKTREE_ID"
    "--json"
  )
  DT_OUTPUT="$(bash "$DT_SCRIPT" "${DT_ARGS[@]}" 2>/dev/null || true)"
  if [[ -n "$DT_OUTPUT" ]]; then
    DOCKER_TEARDOWN_DATA=$(echo "$DT_OUTPUT" | python3 -c "
import json,sys
raw=sys.stdin.read().strip()
try:
    d=json.loads(raw)
except Exception:
    print('null')
    sys.exit(0)
if d.get('ok'):
    print(json.dumps(d.get('data')))
else:
    err=d.get('error',{})
    print(json.dumps({'action':'error','reason':err.get('message','docker-teardown error')}))
" 2>/dev/null || echo "null")
  fi
fi

# ---- Stage 1b: git worktree remove + dir removal ---------------------------

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

# ---- Stage 2: safe branch deletion (AC4 + AC-G2) --------------------------
#
# This runs AFTER the worktree and its directory are gone.  A skip or error
# here is NON-FATAL: the worktree removal still succeeded.  We record the
# branch-delete result in the response envelope.
BRANCH_DELETE_DATA="null"

if [[ -n "$PR_MERGED" ]]; then
  SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
  BD_SCRIPT="${SCRIPT_DIR}/branch-delete.sh"

  BD_ARGS=(
    "--repo-path" "$REPO_PATH"
    "--branch"    "$WT_NAME"
    "--base"      "${BASE_BRANCH:-main}"
    "--pr-merged" "$PR_MERGED"
    "--json"
  )
  if [[ -n "$UPSTREAM" ]];  then BD_ARGS+=("--upstream"  "$UPSTREAM");  fi
  if [[ -n "$PROTECTED" ]]; then BD_ARGS+=("--protected" "$PROTECTED"); fi

  # Run branch-delete.sh; capture its output regardless of exit code.
  # A non-zero exit (json_err path) is still captured and embedded — the
  # worktree removal itself already succeeded.
  BD_OUTPUT="$(bash "$BD_SCRIPT" "${BD_ARGS[@]}" 2>/dev/null || true)"
  if [[ -n "$BD_OUTPUT" ]]; then
    # Extract the data field from the branch-delete L2 envelope.
    # Pipe BD_OUTPUT into python3 via stdin to avoid shell-quoting issues.
    BD_DATA=$(echo "$BD_OUTPUT" | python3 -c "
import json,sys
raw=sys.stdin.read().strip()
try:
    d=json.loads(raw)
except Exception:
    print('null')
    sys.exit(0)
if d.get('ok'):
    print(json.dumps(d.get('data')))
else:
    err=d.get('error',{})
    print(json.dumps({'deleted':False,'skipReason':'error','warning':err.get('message','branch-delete error')}))
" 2>/dev/null || echo "null")
    BRANCH_DELETE_DATA="$BD_DATA"
  fi
fi

if $JSON_MODE; then
  json_ok "{\"worktreeId\":\"${WORKTREE_ID}\",\"branchDelete\":${BRANCH_DELETE_DATA},\"dockerTeardown\":${DOCKER_TEARDOWN_DATA}}"
else
  echo "Removed worktree $WT_NAME at $WT_PATH"
fi
