#!/usr/bin/env bats
# T2: L2 envelope assertions for git/worktree-remove.sh
# Success AND failure paths.

setup() {
  SCRIPT="$BATS_TEST_DIRNAME/worktree-remove.sh"
  TMPDIR_CFG="$(mktemp -d)"
}

teardown() {
  rm -rf "$TMPDIR_CFG"
}

_json_field() {
  python3 -c "import json,sys; d=json.load(sys.stdin); print(d.get('$1','MISSING'))" 2>/dev/null || echo "PARSE_ERROR"
}

# ---------------------------------------------------------------------------
# Helper: create a minimal git repo with one commit and return its path.
# Sets REPO_DIR in the caller's scope.
# ---------------------------------------------------------------------------
_make_repo() {
  REPO_DIR="$(mktemp -d)"
  git -C "$REPO_DIR" init -q
  git -C "$REPO_DIR" config user.email "test@example.com"
  git -C "$REPO_DIR" config user.name "Test"
  touch "$REPO_DIR/README"
  git -C "$REPO_DIR" add README
  git -C "$REPO_DIR" commit -q -m "init"
}

# Helper: add a worktree named $1 to $REPO_DIR and return the path in WT_DIR.
_add_worktree() {
  local branch="$1"
  WT_DIR="$(mktemp -d)"
  git -C "$REPO_DIR" worktree add -q -b "$branch" "$WT_DIR"
}

# Helper: write an orchard config pointing slug "myrepo" at $REPO_DIR.
_write_config() {
  local cfg="$TMPDIR_CFG/config.json"
  printf '{"repos":[{"slug":"myrepo","path":"%s"}]}' "$REPO_DIR" > "$cfg"
  echo "$cfg"
}

# ---------------------------------------------------------------------------
# Original envelope-only tests (no git repo needed)
# ---------------------------------------------------------------------------

@test "missing --worktree-id: ok=false" {
  output="$("$SCRIPT" --json 2>/dev/null || true)"
  [ "$(echo "$output" | _json_field ok)" = "False" ]
}

@test "malformed worktree-id (no colon): ok=false" {
  output="$("$SCRIPT" --json --worktree-id "nocolon" 2>/dev/null || true)"
  [ "$(echo "$output" | _json_field ok)" = "False" ]
}

@test "repo not found in config: ok=false" {
  config="$TMPDIR_CFG/config.json"
  printf '{"repos":[]}' > "$config"
  output="$(ORCHARD_CONFIG="$config" "$SCRIPT" --json --worktree-id "unknownrepo:mybranch" 2>/dev/null || true)"
  [ "$(echo "$output" | _json_field ok)" = "False" ]
}

# ---------------------------------------------------------------------------
# AC3(i) — clean stale worktree removed by git worktree remove
# @scenario A clean stale worktree is removed and its directory is gone
# ---------------------------------------------------------------------------

@test "AC3(i) clean worktree: ok=true, dir gone, porcelain clean" {
  _make_repo
  _add_worktree "feature-clean"
  cfg="$(_write_config)"

  output="$(ORCHARD_CONFIG="$cfg" "$SCRIPT" --json --worktree-id "myrepo:feature-clean" 2>/dev/null)"

  # Envelope ok
  [ "$(echo "$output" | _json_field ok)" = "True" ]

  # Directory must be gone
  [ ! -d "$WT_DIR" ]

  # Porcelain must not list the worktree path
  refute_in_porcelain "$REPO_DIR" "$WT_DIR"
}

# ---------------------------------------------------------------------------
# AC3(ii) — dirty worktree removed via the force path
# @scenario A dirty stale worktree is removed via the force path
# ---------------------------------------------------------------------------

@test "AC3(ii) dirty worktree with uncommitted change: --force removes it, ok=true, dir gone" {
  _make_repo
  _add_worktree "feature-dirty"
  # Add an uncommitted file to make the worktree dirty
  echo "dirty" > "$WT_DIR/dirty.txt"
  cfg="$(_write_config)"

  output="$(ORCHARD_CONFIG="$cfg" "$SCRIPT" --json --force --worktree-id "myrepo:feature-dirty" 2>/dev/null)"

  [ "$(echo "$output" | _json_field ok)" = "True" ]
  [ ! -d "$WT_DIR" ]
  refute_in_porcelain "$REPO_DIR" "$WT_DIR"
}

# ---------------------------------------------------------------------------
# AC3(iii) — locked worktree: git worktree remove --force fails,
#            script falls back to rm -rf + git worktree prune
# @scenario A locked or submodule-gitlink worktree falls back to rm -rf plus git worktree prune
# ---------------------------------------------------------------------------

@test "AC3(iii) locked worktree: fallback rm -rf + prune, ok=true, dir gone" {
  _make_repo
  _add_worktree "feature-locked"
  cfg="$(_write_config)"

  # Lock the worktree so that git worktree remove --force will fail
  git -C "$REPO_DIR" worktree lock "$WT_DIR"

  output="$(ORCHARD_CONFIG="$cfg" "$SCRIPT" --json --force --worktree-id "myrepo:feature-locked" 2>/dev/null)"

  [ "$(echo "$output" | _json_field ok)" = "True" ]
  [ ! -d "$WT_DIR" ]
  refute_in_porcelain "$REPO_DIR" "$WT_DIR"
}

# ---------------------------------------------------------------------------
# AC3(iv) — dir deleted out-of-band but still registered; prune reconciles
# @scenario A worktree dir deleted out-of-band but still registered is reconciled by prune
# ---------------------------------------------------------------------------

@test "AC3(iv) dir deleted out-of-band: prune reconciles, ok=true, porcelain clean" {
  _make_repo
  _add_worktree "feature-oob"
  cfg="$(_write_config)"

  # Delete the directory out-of-band (simulates manual rm)
  rm -rf "$WT_DIR"

  output="$(ORCHARD_CONFIG="$cfg" "$SCRIPT" --json --worktree-id "myrepo:feature-oob" 2>/dev/null)"

  [ "$(echo "$output" | _json_field ok)" = "True" ]
  [ ! -d "$WT_DIR" ]
  refute_in_porcelain "$REPO_DIR" "$WT_DIR"
}

# ---------------------------------------------------------------------------
# AC-G2: gh error → worktree+dir STILL removed, branch SURVIVES
# @scenario A gh enrichment error skips the branch deletion but still removes the worktree and dir
# ---------------------------------------------------------------------------

@test "AC-G2 gh enrichment error (pr-merged=unknown): worktree+dir removed, branch survives" {
  _make_repo
  _add_worktree "feature-gh-err"
  cfg="$(_write_config)"

  # Point origin/HEAD at main so default-branch detection does not fail
  git -C "$REPO_DIR" symbolic-ref refs/remotes/origin/HEAD refs/remotes/origin/main 2>/dev/null || true

  # Simulate gh error by passing pr-merged=unknown
  output="$(ORCHARD_CONFIG="$cfg" "$SCRIPT" \
    --json \
    --worktree-id "myrepo:feature-gh-err" \
    --pr-merged "unknown" \
    --base "main" \
    2>/dev/null)"

  # Worktree removal must succeed (ok=true)
  [ "$(echo "$output" | _json_field ok)" = "True" ]

  # Worktree directory and registration must be GONE
  [ ! -d "$WT_DIR" ]
  refute_in_porcelain "$REPO_DIR" "$WT_DIR"

  # Branch must SURVIVE (fail-closed proof)
  result="$(git -C "$REPO_DIR" branch --list "feature-gh-err")"
  [[ -n "$result" ]]

  # branchDelete must carry skipReason=merged-state-unavailable
  skip_reason="$(echo "$output" | python3 -c "
import json,sys
d=json.load(sys.stdin)
bd=d.get('data',{}).get('branchDelete',{}) or {}
print(bd.get('skipReason','MISSING'))
" 2>/dev/null || echo "PARSE_ERROR")"
  [ "$skip_reason" = "merged-state-unavailable" ]
}

# ---------------------------------------------------------------------------
# Helper: assert the given worktree path is NOT in git worktree list --porcelain
# ---------------------------------------------------------------------------
refute_in_porcelain() {
  local repo="$1" wt_path="$2"
  ! git -C "$repo" worktree list --porcelain 2>/dev/null | grep -qF "worktree $wt_path"
}
