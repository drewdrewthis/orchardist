#!/usr/bin/env bash
# scripts/git/branch-delete.sh — safe, fail-closed local branch deletion (L1, L2, L3)
#
# This script implements the AC4 + AC-G2 safe-delete predicate for the
# worktree cleanup pipeline (issue #693, Step 3).
#
# It is called AFTER the worktree and its directory have already been removed.
# A skip or error here MUST NOT abort the calling remove operation; only the
# branch-delete stage is affected.
#
# Usage:
#   branch-delete.sh \
#     --repo-path   <abs-path-to-repo> \
#     --branch      <branch-name> \
#     --base        <base-branch-or-sha> \
#     --pr-merged   <merged|not-merged|unknown> \
#     [--upstream   <remote-tracking-ref>]   (e.g. "origin/my-branch")
#     [--protected  <branch1,branch2,...>]   (comma-separated protected list)
#     [--json]
#
# Outputs L2 envelope on --json:
#   deleted:  {"ok":true,"data":{"branch":"<n>","deleted":true}}
#   skipped:  {"ok":true,"data":{"branch":"<n>","deleted":false,"skipReason":"<reason>","warning":"<msg>"}}
#   error:    {"ok":false,"error":{"code":"<code>","message":"<msg>"}}
#
# Skip reasons (typed per AC4 / AC-G2):
#   default-branch               — branch is the repo default branch
#   protected                    — branch is in the configured protected set
#   not-merged                   — branch is not fully merged (both signals agree)
#   local-commits-ahead-of-merge — branch has unpushed commits ahead of upstream
#   no-upstream                  — no upstream configured; cannot confirm pushed
#   merged-state-unavailable     — gh merged signal absent/errored/unknown
#
# Fail-closed (AC-G2): any doubt defaults to SKIP, not DELETE.
#
# Default protected set (mirrors PROTECTED_SESSION_KEEPERS pattern):
#   main, master, develop, dev
# Additional protected branches may be passed via --protected.
set -euo pipefail

REPO_PATH=""
BRANCH=""
BASE=""
PR_MERGED=""
UPSTREAM=""
EXTRA_PROTECTED=""
JSON_MODE=false

while [[ $# -gt 0 ]]; do
  case "$1" in
    --repo-path)  REPO_PATH="$2";      shift 2 ;;
    --branch)     BRANCH="$2";         shift 2 ;;
    --base)       BASE="$2";           shift 2 ;;
    --pr-merged)  PR_MERGED="$2";      shift 2 ;;
    --upstream)   UPSTREAM="$2";       shift 2 ;;
    --protected)  EXTRA_PROTECTED="$2";shift 2 ;;
    --json)       JSON_MODE=true;      shift   ;;
    *)            echo "Unknown argument: $1" >&2; exit 2 ;;
  esac
done

# ---- L2 envelope helpers ---------------------------------------------------

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

# Emit a skip result (ok:true, deleted:false) with a typed reason + warning.
json_skip() {
  local branch="$1" reason="$2" msg="$3"
  branch="${branch//\"/\\\"}"
  msg="${msg//\"/\\\"}"
  reason="${reason//\"/\\\"}"
  local data="{\"branch\":\"${branch}\",\"deleted\":false,\"skipReason\":\"${reason}\",\"warning\":\"${msg}\"}"
  if $JSON_MODE; then
    json_ok "$data"
  else
    echo "SKIP branch=${branch} reason=${reason}: ${msg}" >&2
    echo "$data"
  fi
}

# Emit a deleted result (ok:true, deleted:true).
json_deleted() {
  local branch="$1"
  branch="${branch//\"/\\\"}"
  local data="{\"branch\":\"${branch}\",\"deleted\":true}"
  if $JSON_MODE; then
    json_ok "$data"
  else
    echo "DELETED branch=${branch}"
    echo "$data"
  fi
}

# ---- Input validation (M4) -------------------------------------------------

if [[ -z "$REPO_PATH" ]]; then
  if $JSON_MODE; then json_err "INVALID_INPUT" "repo-path is required"; else echo "repo-path is required" >&2; exit 1; fi
fi
if [[ -z "$BRANCH" ]]; then
  if $JSON_MODE; then json_err "INVALID_INPUT" "branch is required"; else echo "branch is required" >&2; exit 1; fi
fi
if [[ -z "$BASE" ]]; then
  if $JSON_MODE; then json_err "INVALID_INPUT" "base is required"; else echo "base is required" >&2; exit 1; fi
fi
if [[ -z "$PR_MERGED" ]]; then
  if $JSON_MODE; then json_err "INVALID_INPUT" "pr-merged is required (merged|not-merged|unknown)"; else echo "pr-merged is required" >&2; exit 1; fi
fi

# ---- Predicate 1: fail-closed on unknown merged state (AC-G2) --------------
#
# If the gh merged signal is absent/errored/rate-limited, we CANNOT positively
# confirm merged.  Treat as unavailable -> skip.
if [[ "$PR_MERGED" == "unknown" ]]; then
  json_skip "$BRANCH" "merged-state-unavailable" \
    "gh merged signal unavailable (unknown); keeping branch ${BRANCH} to avoid data loss"
  exit 0
fi

# ---- Predicate 2: default branch check ------------------------------------
#
# Determine the repo default branch via a fallback chain.  Each step is tried
# in order; the first that yields a non-empty branch name wins.
#
# 1. symbolic-ref refs/remotes/origin/HEAD  (set by git clone — authoritative)
# 2. rev-parse --abbrev-ref origin/HEAD     (alternative notation)
# 3. git config init.defaultBranch          (locally configured default)
# 4. probe for conventional local branches: main, then master
# 5. fail-closed: cannot determine default branch (data-loss safety)
DEFAULT_BRANCH=""

# Step 1: symbolic-ref (set by clone, authoritative when present)
if [[ -z "$DEFAULT_BRANCH" ]]; then
  if SYMREF=$(git -C "$REPO_PATH" symbolic-ref refs/remotes/origin/HEAD 2>/dev/null); then
    DEFAULT_BRANCH="${SYMREF#refs/remotes/origin/}"
  fi
fi

# Step 2: rev-parse --abbrev-ref origin/HEAD (skip if it returns "origin/HEAD" literally)
if [[ -z "$DEFAULT_BRANCH" ]]; then
  ABBREV=$(git -C "$REPO_PATH" rev-parse --abbrev-ref origin/HEAD 2>/dev/null || true)
  if [[ -n "$ABBREV" && "$ABBREV" != "origin/HEAD" ]]; then
    DEFAULT_BRANCH="${ABBREV#origin/}"
  fi
fi

# Step 3: git config init.defaultBranch
if [[ -z "$DEFAULT_BRANCH" ]]; then
  CFG_DEFAULT=$(git -C "$REPO_PATH" config --get init.defaultBranch 2>/dev/null || true)
  if [[ -n "$CFG_DEFAULT" ]]; then
    DEFAULT_BRANCH="$CFG_DEFAULT"
  fi
fi

# Step 4: probe conventional local branch names (main, then master)
if [[ -z "$DEFAULT_BRANCH" ]]; then
  if git -C "$REPO_PATH" show-ref --verify --quiet refs/heads/main 2>/dev/null; then
    DEFAULT_BRANCH="main"
  elif git -C "$REPO_PATH" show-ref --verify --quiet refs/heads/master 2>/dev/null; then
    DEFAULT_BRANCH="master"
  fi
fi

# Step 5: fail-closed — all detection methods exhausted; cannot confirm the
# branch is NOT the default, so skip to avoid data loss.
if [[ -z "$DEFAULT_BRANCH" ]]; then
  json_skip "$BRANCH" "default-branch" \
    "could not determine repo default branch; keeping ${BRANCH} to avoid data loss"
  exit 0
fi

if [[ "$BRANCH" == "$DEFAULT_BRANCH" ]]; then
  json_skip "$BRANCH" "default-branch" \
    "branch ${BRANCH} is the repo default branch; never deleted"
  exit 0
fi

# ---- Predicate 3: protected set check -------------------------------------
#
# Hardcoded keeper set (mirrors PROTECTED_SESSION_KEEPERS pattern).
# Plus any caller-supplied comma-separated --protected list.
HARDCODED_PROTECTED="main master develop dev"

is_protected() {
  local b="$1"
  # Check hardcoded set
  for p in $HARDCODED_PROTECTED; do
    if [[ "$b" == "$p" ]]; then return 0; fi
  done
  # Check caller-supplied set
  if [[ -n "$EXTRA_PROTECTED" ]]; then
    IFS=',' read -ra extra_arr <<< "$EXTRA_PROTECTED"
    for p in "${extra_arr[@]}"; do
      if [[ "$b" == "$p" ]]; then return 0; fi
    done
  fi
  return 1
}

if is_protected "$BRANCH"; then
  json_skip "$BRANCH" "protected" \
    "branch ${BRANCH} is in the protected set; never deleted"
  exit 0
fi

# ---- Predicate 4: merged agreement (git AND gh must BOTH say merged) -------
#
# git: `git branch --merged <base>` must list the branch
GIT_MERGED=false
if git -C "$REPO_PATH" branch --merged "$BASE" 2>/dev/null \
    | sed 's/^[* ]*//' \
    | grep -qxF "$BRANCH"; then
  GIT_MERGED=true
fi

# gh signal is already validated as "merged" or "not-merged" at this point
GH_MERGED=false
if [[ "$PR_MERGED" == "merged" ]]; then
  GH_MERGED=true
fi

if ! $GIT_MERGED || ! $GH_MERGED; then
  if $GIT_MERGED && ! $GH_MERGED; then
    # git says merged, gh says not-merged — disagreement
    json_skip "$BRANCH" "not-merged" \
      "gh reports not-merged for branch ${BRANCH} (git says merged); treating as not-merged"
  elif ! $GIT_MERGED && $GH_MERGED; then
    # gh says merged, git says not — disagreement
    json_skip "$BRANCH" "not-merged" \
      "gh reports merged but git branch --merged ${BASE} does not list ${BRANCH}; treating as not-merged (sources disagree)"
  else
    # Both say not-merged
    json_skip "$BRANCH" "not-merged" \
      "branch ${BRANCH} is not merged into ${BASE} (both git and gh agree)"
  fi
  exit 0
fi

# ---- Predicate 5: no unpushed local commits ahead of merge ----------------
#
# If --upstream was not supplied, attempt to resolve the tracking ref from git
# config (set by git push -u or git branch --set-upstream-to).  Fall back to
# the conventional origin/<branch> ref if it exists.  Only fail-closed when no
# upstream can be determined at all.
if [[ -z "$UPSTREAM" ]]; then
  # Try git-configured tracking branch first (@{u} notation)
  TRACKED=$(git -C "$REPO_PATH" rev-parse --abbrev-ref "${BRANCH}@{u}" 2>/dev/null || true)
  if [[ -n "$TRACKED" && "$TRACKED" != "${BRANCH}@{u}" ]]; then
    UPSTREAM="$TRACKED"
  fi
fi

if [[ -z "$UPSTREAM" ]]; then
  # Probe the conventional origin/<branch> ref
  if git -C "$REPO_PATH" show-ref --verify --quiet "refs/remotes/origin/${BRANCH}" 2>/dev/null; then
    UPSTREAM="origin/${BRANCH}"
  fi
fi

if [[ -z "$UPSTREAM" ]]; then
  json_skip "$BRANCH" "no-upstream" \
    "no upstream configured for branch ${BRANCH}; cannot confirm all commits are pushed; keeping branch"
  exit 0
fi

UNPUSHED=""
if UNPUSHED=$(git -C "$REPO_PATH" rev-list "${BRANCH}" --not "${UPSTREAM}" 2>/dev/null); then
  : # success
else
  # rev-list errored (upstream ref missing etc.) — fail closed
  json_skip "$BRANCH" "no-upstream" \
    "git rev-list failed for upstream ${UPSTREAM} of ${BRANCH}; cannot confirm pushed; keeping branch"
  exit 0
fi

if [[ -n "$UNPUSHED" ]]; then
  json_skip "$BRANCH" "local-commits-ahead-of-merge" \
    "branch ${BRANCH} has unpushed commits ahead of upstream ${UPSTREAM}; keeping branch to avoid data loss"
  exit 0
fi

# ---- All predicates passed: delete the branch ------------------------------

if ! DEL_ERR=$(git -C "$REPO_PATH" branch -d "$BRANCH" 2>&1); then
  if $JSON_MODE; then
    json_err "GIT_ERROR" "failed to delete branch ${BRANCH}: ${DEL_ERR}"
  else
    echo "ERROR: failed to delete branch ${BRANCH}: ${DEL_ERR}" >&2
    exit 1
  fi
fi

json_deleted "$BRANCH"
