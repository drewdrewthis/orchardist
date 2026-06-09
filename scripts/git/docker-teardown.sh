#!/usr/bin/env bash
# scripts/git/docker-teardown.sh — collision-safe docker compose teardown (L1, L2, L3)
#
# Implements AC5 + AC6 of issue #693 (Step 4).
#
# Usage:
#   docker-teardown.sh \
#     --worktree-dir <abs-path-to-worktree-dir> \
#     --worktree-id  <projectId:worktreeName>   \
#     [--json]
#
# Outputs L2 envelope on --json:
#   success (ran): {"ok":true,"data":{"worktreeId":"<id>","projectKey":"<k>","action":"down"}}
#   success (no-op): {"ok":true,"data":{"worktreeId":"<id>","action":"no-op","reason":"<reason>"}}
#   failure: {"ok":false,"error":{"code":"<code>","message":"<msg>"}}
#
# No-op reasons (clean successes — AC6):
#   docker-absent     — docker binary not on PATH or docker compose v2 unavailable
#   no-compose-file   — no compose file found in the worktree dir
#
# Key derivation (AC5 — collision-safe):
#   The default docker compose project name is the directory basename; two
#   worktrees with the same leaf name under different repos would collide.
#   This script derives a collision-safe project key from the STABLE worktree
#   identity: sha1 of the absolute path, truncated to 12 hex chars, prefixed
#   with "orchard-".  Paths that share only the basename get different keys.
#
# Teardown command (AC5): two-phase teardown
#   Phase 1: docker compose -p <key> down --volumes
#     -- removes containers, networks, named+anonymous volumes; NO --rmi flag
#        so registry-pulled images are left untouched.
#   Phase 2: explicitly remove images BUILT by this compose project.
#     -- identify services that have a build: key via docker compose config
#        output; collect their image names; docker rmi those images.
#        This correctly handles both auto-named and custom-tagged (image: field)
#        built images, unlike --rmi local which skips custom-tagged images.
#
# Exit code 0 on ok:true, non-zero on ok:false.
set -euo pipefail

WORKTREE_DIR=""
WORKTREE_ID=""
JSON_MODE=false

while [[ $# -gt 0 ]]; do
  case "$1" in
    --worktree-dir) WORKTREE_DIR="$2"; shift 2 ;;
    --worktree-id)  WORKTREE_ID="$2";  shift 2 ;;
    --json)         JSON_MODE=true;    shift   ;;
    *) echo "Unknown argument: $1" >&2; exit 2 ;;
  esac
done

# ---- L2 envelope helpers ----------------------------------------------------

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

json_noop() {
  local id="$1" reason="$2"
  id="${id//\"/\\\"}"
  reason="${reason//\"/\\\"}"
  if $JSON_MODE; then
    json_ok "{\"worktreeId\":\"${id}\",\"action\":\"no-op\",\"reason\":\"${reason}\"}"
  else
    echo "docker-teardown: no-op (${reason}) for ${id}"
  fi
}

# ---- Input validation (M4) --------------------------------------------------

if [[ -z "$WORKTREE_DIR" ]]; then
  if $JSON_MODE; then json_err "INVALID_INPUT" "worktree-dir is required"; else echo "worktree-dir is required" >&2; exit 1; fi
fi
if [[ -z "$WORKTREE_ID" ]]; then
  if $JSON_MODE; then json_err "INVALID_INPUT" "worktree-id is required"; else echo "worktree-id is required" >&2; exit 1; fi
fi

# ---- AC6: detect docker compose v2 availability ----------------------------
#
# Check 1: docker binary must be on PATH.
# Check 2: docker compose (the v2 plugin form) must work.
# If EITHER is absent: clean no-op, no docker-stage error.
#
# We deliberately do NOT test for the legacy v1 docker-compose binary (AC6:
# "Do NOT assume v1 docker-compose").
_docker_compose_available() {
  command -v docker >/dev/null 2>&1 && docker compose version >/dev/null 2>&1
}

if ! _docker_compose_available; then
  json_noop "$WORKTREE_ID" "docker-absent"
  exit 0
fi

# ---- AC6: detect compose file in the worktree dir --------------------------
#
# Supported filenames (all four canonical variants):
#   docker-compose.yml  docker-compose.yaml  compose.yml  compose.yaml
_find_compose_file() {
  local dir="$1"
  for name in docker-compose.yml docker-compose.yaml compose.yml compose.yaml; do
    if [[ -f "${dir}/${name}" ]]; then
      echo "${dir}/${name}"
      return 0
    fi
  done
  return 1
}

if ! _find_compose_file "$WORKTREE_DIR" >/dev/null 2>&1; then
  json_noop "$WORKTREE_ID" "no-compose-file"
  exit 0
fi

# ---- AC5: derive collision-safe project key ---------------------------------
#
# Default compose project name = dir basename.  Two worktrees at:
#   /repos/projectA/feature-x
#   /repos/projectB/feature-x
# would share the basename "feature-x" and collide.
#
# We derive the key from the ABSOLUTE path via sha1/shasum, truncated to 12
# hex chars, prefixed with "orchard-".  Paths that differ in any component
# get statistically distinct keys.
#
# shasum is available on macOS (coreutils SHA1); sha1sum on Linux.  Both read
# from stdin. We try shasum first (macOS), then sha1sum (Linux).
_path_hash() {
  local path="$1"
  local h=""
  if command -v shasum >/dev/null 2>&1; then
    h=$(printf '%s' "$path" | shasum -a 1 | awk '{print $1}')
  elif command -v sha1sum >/dev/null 2>&1; then
    h=$(printf '%s' "$path" | sha1sum | awk '{print $1}')
  else
    # Fallback: use md5 if neither sha tool is available.
    if command -v md5sum >/dev/null 2>&1; then
      h=$(printf '%s' "$path" | md5sum | awk '{print $1}')
    elif command -v md5 >/dev/null 2>&1; then
      h=$(printf '%s' "$path" | md5 -q)
    else
      # Absolute last resort: use the full path, sanitized. Not truly
      # collision-safe for long paths but better than the bare basename.
      h=$(printf '%s' "$path" | tr -c 'a-zA-Z0-9' '_')
    fi
  fi
  printf '%s' "${h:0:12}"
}

PROJECT_KEY="orchard-$(_path_hash "$WORKTREE_DIR")"

# ---- AC5: teardown ----------------------------------------------------------
#
# Two-phase teardown:
#   Phase 1: down --volumes — remove containers, networks, volumes.
#   Phase 2: remove BUILT images — find services with a build: key in the
#     compose config; collect their resolved image names; docker rmi them.
#     Registry-pulled images (no build: key) are left untouched.
#
# Run from the worktree dir so compose reads the file naturally.
COMPOSE_FILE_PATH=$(_find_compose_file "$WORKTREE_DIR")

# Phase 1: bring down containers/networks/volumes (no --rmi).
if ! DOCKER_ERR=$(cd "$WORKTREE_DIR" && docker compose -p "$PROJECT_KEY" down --volumes 2>&1); then
  if $JSON_MODE; then
    DOCKER_ERR="${DOCKER_ERR//\"/\\\"}"
    json_err "DOCKER_ERROR" "docker compose down failed for ${WORKTREE_ID}: ${DOCKER_ERR}"
  else
    echo "ERROR: docker compose down failed for ${WORKTREE_ID}: ${DOCKER_ERR}" >&2
    exit 1
  fi
fi

# Phase 2: remove images built by this compose project.
# Strategy: parse "docker compose config" JSON to find services that have a
# build: key, collect their image names, then docker rmi those images.
# This correctly handles custom-tagged builds (image: foo:tag) that --rmi local
# silently skips.
_remove_built_images() {
  local project_key="$1"
  local wt_dir="$2"

  # Get the resolved compose config as JSON; skip if docker compose config fails.
  local config_json
  if ! config_json=$(cd "$wt_dir" && docker compose -p "$project_key" config --format json 2>/dev/null); then
    return 0
  fi

  # Extract image names for services that have a build: key.
  # Use python3 for JSON parsing (portable; python3 is available on CI Linux).
  local built_images
  built_images=$(printf '%s' "$config_json" | python3 -c "
import json, sys
data = json.load(sys.stdin)
services = data.get('services', {})
for name, svc in services.items():
    if 'build' in svc:
        img = svc.get('image', '')
        if img:
            print(img)
" 2>/dev/null) || return 0

  if [ -z "$built_images" ]; then
    return 0
  fi

  # Remove each built image; tolerate already-removed or in-use errors.
  while IFS= read -r img; do
    [ -z "$img" ] && continue
    docker rmi -f "$img" 2>/dev/null || true
  done <<< "$built_images"
}

_remove_built_images "$PROJECT_KEY" "$WORKTREE_DIR"

if $JSON_MODE; then
  PROJECT_KEY_SAFE="${PROJECT_KEY//\"/\\\"}"
  ID_SAFE="${WORKTREE_ID//\"/\\\"}"
  json_ok "{\"worktreeId\":\"${ID_SAFE}\",\"projectKey\":\"${PROJECT_KEY_SAFE}\",\"action\":\"down\"}"
else
  echo "docker-teardown: down complete for ${WORKTREE_ID} (project=${PROJECT_KEY})"
fi
