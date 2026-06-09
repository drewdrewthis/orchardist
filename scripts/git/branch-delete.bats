#!/usr/bin/env bats
# T2: branch-delete.sh — safe, fail-closed local branch deletion (AC4 + AC-G2)
# @scenario A fully-merged unprotected branch is deleted
# @scenario The repo default branch is skipped and warned, never deleted
# @scenario A protected branch is skipped and warned
# @scenario An unmerged branch behind a non-merged closed PR is skipped and warned
# @scenario A merged PR with unpushed local commits ahead of the merge does not authorize branch deletion
# @scenario A gh enrichment error skips the branch deletion but still removes the worktree and dir
# @scenario gh and git must AGREE on merged before a branch is deleted

SCRIPT=""
REPO_DIR=""

setup() {
  SCRIPT="$BATS_TEST_DIRNAME/branch-delete.sh"
}

teardown() {
  if [[ -n "$REPO_DIR" && -d "$REPO_DIR" ]]; then
    rm -rf "$REPO_DIR"
  fi
}

# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------

_json_field() {
  python3 -c "import json,sys; d=json.load(sys.stdin); print(d.get('$1','MISSING'))" 2>/dev/null || echo "PARSE_ERROR"
}

_json_data_field() {
  python3 -c "import json,sys; d=json.load(sys.stdin); data=d.get('data',{}); print(data.get('$1','MISSING'))" 2>/dev/null || echo "PARSE_ERROR"
}

# Create a minimal git repo with one commit on a branch called "main".
# Sets REPO_DIR in caller scope.
_make_repo() {
  REPO_DIR="$(mktemp -d)"
  git -C "$REPO_DIR" init -q -b main
  git -C "$REPO_DIR" config user.email "test@example.com"
  git -C "$REPO_DIR" config user.name "Test"
  touch "$REPO_DIR/README"
  git -C "$REPO_DIR" add README
  git -C "$REPO_DIR" commit -q -m "init"
}

# Create a feature branch merged into main.
# $1 = branch name.  Branch is created from main and merged back.
# Sets BRANCH_NAME.
_make_merged_branch() {
  local branch="$1"
  BRANCH_NAME="$branch"
  git -C "$REPO_DIR" checkout -q -b "$branch"
  echo "feature" >> "$REPO_DIR/feature.txt"
  git -C "$REPO_DIR" add feature.txt
  git -C "$REPO_DIR" commit -q -m "feature commit"
  git -C "$REPO_DIR" checkout -q main
  git -C "$REPO_DIR" merge -q --no-ff "$branch" -m "merge $branch"
  # Branch still exists locally (worktree removal happened, branch was not auto-deleted)
}

# Create an unmerged feature branch (committed but NOT merged into main).
_make_unmerged_branch() {
  local branch="$1"
  BRANCH_NAME="$branch"
  git -C "$REPO_DIR" checkout -q -b "$branch"
  echo "wip" >> "$REPO_DIR/wip.txt"
  git -C "$REPO_DIR" add wip.txt
  git -C "$REPO_DIR" commit -q -m "wip commit"
  git -C "$REPO_DIR" checkout -q main
  # Do NOT merge; branch exists but is unmerged
}

# Simulate an upstream remote tracking ref by creating a fake remote branch
# that matches the local branch tip (no unpushed commits).
# $1 = branch name, mimics "origin/<branch>" existing at same SHA
_make_upstream_in_sync() {
  local branch="$1"
  # Create refs/remotes/origin/<branch> pointing at same commit as local branch
  local sha
  sha=$(git -C "$REPO_DIR" rev-parse "$branch")
  git -C "$REPO_DIR" update-ref "refs/remotes/origin/${branch}" "$sha"
}

# Simulate an upstream where local branch is AHEAD by one commit.
# Call AFTER _make_upstream_in_sync — then add another commit locally.
_make_local_ahead_of_upstream() {
  local branch="$1"
  # First set upstream at current tip
  _make_upstream_in_sync "$branch"
  # Now checkout branch, add a commit, go back to main
  git -C "$REPO_DIR" checkout -q "$branch"
  echo "extra" >> "$REPO_DIR/extra.txt"
  git -C "$REPO_DIR" add extra.txt
  git -C "$REPO_DIR" commit -q -m "unpushed extra commit"
  git -C "$REPO_DIR" checkout -q main
  # Merge the original commit into main (simulating PR merged at the earlier SHA)
  # but the extra commit is NOT part of the merge, so it is unpushed
}

# Point origin/HEAD at origin/main so default-branch detection works.
_set_origin_head() {
  git -C "$REPO_DIR" symbolic-ref refs/remotes/origin/HEAD refs/remotes/origin/main 2>/dev/null || true
}

# ---------------------------------------------------------------------------
# @scenario A fully-merged unprotected branch is deleted
# Scenario ~110: merged, not default, not protected, no unpushed commits
# ---------------------------------------------------------------------------

@test "AC4(i) fully-merged unprotected branch is deleted" {
  _make_repo
  _make_merged_branch "feature-merged"
  _make_upstream_in_sync "feature-merged"
  _set_origin_head

  output="$(bash "$SCRIPT" \
    --repo-path "$REPO_DIR" \
    --branch "feature-merged" \
    --base "main" \
    --pr-merged "merged" \
    --upstream "origin/feature-merged" \
    --json 2>/dev/null)"

  # L2 envelope ok=true
  [ "$(echo "$output" | _json_field ok)" = "True" ]
  # deleted=true
  [ "$(echo "$output" | _json_data_field deleted)" = "True" ]
  # Branch must no longer exist
  refute_branch_exists "feature-merged"
}

# ---------------------------------------------------------------------------
# @scenario The repo default branch is skipped and warned, never deleted
# Scenario ~116: branch IS the default branch
# ---------------------------------------------------------------------------

@test "AC4(ii) default branch skipped with reason default-branch" {
  _make_repo
  _set_origin_head

  # "main" is the default branch in our fixture
  output="$(bash "$SCRIPT" \
    --repo-path "$REPO_DIR" \
    --branch "main" \
    --base "main" \
    --pr-merged "merged" \
    --upstream "origin/main" \
    --json 2>/dev/null)"

  # ok=true (skip is not an error)
  [ "$(echo "$output" | _json_field ok)" = "True" ]
  # deleted=false
  [ "$(echo "$output" | _json_data_field deleted)" = "False" ]
  # skipReason=default-branch
  [ "$(echo "$output" | _json_data_field skipReason)" = "default-branch" ]
  # Branch still exists
  assert_branch_exists "main"
}

# ---------------------------------------------------------------------------
# @scenario A protected branch is skipped and warned
# Scenario ~123: branch in hardcoded or caller-supplied protected set
# ---------------------------------------------------------------------------

@test "AC4(iii) hardcoded protected branch (master) skipped with reason protected" {
  _make_repo
  # Create a "master" branch from main
  git -C "$REPO_DIR" checkout -q -b "master"
  echo "master-file" >> "$REPO_DIR/master.txt"
  git -C "$REPO_DIR" add master.txt
  git -C "$REPO_DIR" commit -q -m "master commit"
  git -C "$REPO_DIR" checkout -q main
  git -C "$REPO_DIR" merge -q --no-ff master -m "merge master"
  _set_origin_head
  _make_upstream_in_sync "master"

  output="$(bash "$SCRIPT" \
    --repo-path "$REPO_DIR" \
    --branch "master" \
    --base "main" \
    --pr-merged "merged" \
    --upstream "origin/master" \
    --json 2>/dev/null)"

  [ "$(echo "$output" | _json_field ok)" = "True" ]
  [ "$(echo "$output" | _json_data_field deleted)" = "False" ]
  [ "$(echo "$output" | _json_data_field skipReason)" = "protected" ]
  assert_branch_exists "master"
}

@test "AC4(iii) caller-supplied protected branch skipped with reason protected" {
  _make_repo
  _make_merged_branch "release-1.0"
  _make_upstream_in_sync "release-1.0"
  _set_origin_head

  output="$(bash "$SCRIPT" \
    --repo-path "$REPO_DIR" \
    --branch "release-1.0" \
    --base "main" \
    --pr-merged "merged" \
    --upstream "origin/release-1.0" \
    --protected "release-1.0,hotfix" \
    --json 2>/dev/null)"

  [ "$(echo "$output" | _json_field ok)" = "True" ]
  [ "$(echo "$output" | _json_data_field deleted)" = "False" ]
  [ "$(echo "$output" | _json_data_field skipReason)" = "protected" ]
  assert_branch_exists "release-1.0"
}

# ---------------------------------------------------------------------------
# @scenario An unmerged branch behind a non-merged closed PR is skipped
# Scenario ~131: PR closed-not-merged, branch not merged into base
# ---------------------------------------------------------------------------

@test "AC4(iv) unmerged branch with pr-merged=not-merged skipped with reason not-merged" {
  _make_repo
  _make_unmerged_branch "feature-unmerged"
  _make_upstream_in_sync "feature-unmerged"
  _set_origin_head

  output="$(bash "$SCRIPT" \
    --repo-path "$REPO_DIR" \
    --branch "feature-unmerged" \
    --base "main" \
    --pr-merged "not-merged" \
    --upstream "origin/feature-unmerged" \
    --json 2>/dev/null)"

  [ "$(echo "$output" | _json_field ok)" = "True" ]
  [ "$(echo "$output" | _json_data_field deleted)" = "False" ]
  [ "$(echo "$output" | _json_data_field skipReason)" = "not-merged" ]
  assert_branch_exists "feature-unmerged"
}

# ---------------------------------------------------------------------------
# @scenario Merged PR with unpushed local commits — does not authorize deletion
# Scenario ~138: gh=merged, git=merged, but rev-list shows local commits ahead
# ---------------------------------------------------------------------------

@test "AC4(v) merged PR with unpushed local commits skipped with reason local-commits-ahead-of-merge" {
  _make_repo
  _set_origin_head

  # Build the fixture so:
  # 1. feature-ahead has two commits (first = PR commit, second = unpushed extra)
  # 2. origin/feature-ahead points at the FIRST commit (what was merged)
  # 3. main fast-forward merges feature-ahead's full tip (so git branch --merged lists it)
  # 4. rev-list feature-ahead --not origin/feature-ahead is non-empty (the extra commit)
  git -C "$REPO_DIR" checkout -q -b "feature-ahead"
  echo "feature" >> "$REPO_DIR/feature.txt"
  git -C "$REPO_DIR" add feature.txt
  git -C "$REPO_DIR" commit -q -m "PR commit"
  # Add extra local commit BEFORE merge — upstream is set at the PR commit SHA
  UPSTREAM_SHA=$(git -C "$REPO_DIR" rev-parse HEAD)
  git -C "$REPO_DIR" update-ref "refs/remotes/origin/feature-ahead" "$UPSTREAM_SHA"
  echo "extra" >> "$REPO_DIR/extra.txt"
  git -C "$REPO_DIR" add extra.txt
  git -C "$REPO_DIR" commit -q -m "unpushed local commit"
  # Fast-forward merge the full tip (both commits) into main
  git -C "$REPO_DIR" checkout -q main
  git -C "$REPO_DIR" merge -q --ff-only "feature-ahead"

  # Verify fixture: git branch --merged main must list feature-ahead
  # and rev-list must be non-empty (unpushed commit)

  output="$(bash "$SCRIPT" \
    --repo-path "$REPO_DIR" \
    --branch "feature-ahead" \
    --base "main" \
    --pr-merged "merged" \
    --upstream "origin/feature-ahead" \
    --json 2>/dev/null)"

  [ "$(echo "$output" | _json_field ok)" = "True" ]
  [ "$(echo "$output" | _json_data_field deleted)" = "False" ]
  [ "$(echo "$output" | _json_data_field skipReason)" = "local-commits-ahead-of-merge" ]
  assert_branch_exists "feature-ahead"
}

# ---------------------------------------------------------------------------
# @scenario A gh enrichment error skips branch deletion but worktree+dir removed
# Scenario ~180: pr-merged=unknown → skip branch, worktree already removed by caller
#
# The branch-delete script receives pr-merged=unknown (gh errored).
# It must skip the branch. The caller (worktree-remove.sh) is responsible for
# the worktree+dir removal; here we verify the branch SURVIVES when pr-merged=unknown.
# ---------------------------------------------------------------------------

@test "AC-G2 gh error (pr-merged=unknown) skips branch with reason merged-state-unavailable" {
  _make_repo
  _make_merged_branch "feature-gh-error"
  _make_upstream_in_sync "feature-gh-error"
  _set_origin_head

  # Simulate gh errored: pass pr-merged=unknown
  output="$(bash "$SCRIPT" \
    --repo-path "$REPO_DIR" \
    --branch "feature-gh-error" \
    --base "main" \
    --pr-merged "unknown" \
    --upstream "origin/feature-gh-error" \
    --json 2>/dev/null)"

  # ok=true (skip is not an error — worktree removal must still succeed)
  [ "$(echo "$output" | _json_field ok)" = "True" ]
  [ "$(echo "$output" | _json_data_field deleted)" = "False" ]
  [ "$(echo "$output" | _json_data_field skipReason)" = "merged-state-unavailable" ]
  # FAIL-CLOSED PROOF: branch must survive
  assert_branch_exists "feature-gh-error"
}

# ---------------------------------------------------------------------------
# @scenario gh and git must AGREE on merged before a branch is deleted
# Scenario ~189: gh=merged, but git branch --merged <base> disagrees
# ---------------------------------------------------------------------------

@test "AC-G2 gh says merged but git disagrees: branch skipped, reason not-merged" {
  _make_repo
  _make_unmerged_branch "feature-disagree"
  _make_upstream_in_sync "feature-disagree"
  _set_origin_head

  # gh reports merged, but the branch is NOT in git branch --merged main
  output="$(bash "$SCRIPT" \
    --repo-path "$REPO_DIR" \
    --branch "feature-disagree" \
    --base "main" \
    --pr-merged "merged" \
    --upstream "origin/feature-disagree" \
    --json 2>/dev/null)"

  [ "$(echo "$output" | _json_field ok)" = "True" ]
  [ "$(echo "$output" | _json_data_field deleted)" = "False" ]
  [ "$(echo "$output" | _json_data_field skipReason)" = "not-merged" ]
  assert_branch_exists "feature-disagree"
}

# ---------------------------------------------------------------------------
# No-upstream edge: no --upstream passed → skip with no-upstream reason
# ---------------------------------------------------------------------------

@test "no-upstream: skip with reason no-upstream" {
  _make_repo
  _make_merged_branch "feature-no-upstream"
  _set_origin_head

  # Deliberate: do NOT pass --upstream
  output="$(bash "$SCRIPT" \
    --repo-path "$REPO_DIR" \
    --branch "feature-no-upstream" \
    --base "main" \
    --pr-merged "merged" \
    --json 2>/dev/null)"

  [ "$(echo "$output" | _json_field ok)" = "True" ]
  [ "$(echo "$output" | _json_data_field deleted)" = "False" ]
  [ "$(echo "$output" | _json_data_field skipReason)" = "no-upstream" ]
  assert_branch_exists "feature-no-upstream"
}

# ---------------------------------------------------------------------------
# Input validation
# ---------------------------------------------------------------------------

@test "missing --repo-path: ok=false INVALID_INPUT" {
  output="$(bash "$SCRIPT" --branch foo --base main --pr-merged merged --json 2>/dev/null || true)"
  [ "$(echo "$output" | _json_field ok)" = "False" ]
}

@test "missing --branch: ok=false INVALID_INPUT" {
  _make_repo
  output="$(bash "$SCRIPT" --repo-path "$REPO_DIR" --base main --pr-merged merged --json 2>/dev/null || true)"
  [ "$(echo "$output" | _json_field ok)" = "False" ]
}

@test "missing --base: ok=false INVALID_INPUT" {
  _make_repo
  output="$(bash "$SCRIPT" --repo-path "$REPO_DIR" --branch foo --pr-merged merged --json 2>/dev/null || true)"
  [ "$(echo "$output" | _json_field ok)" = "False" ]
}

@test "missing --pr-merged: ok=false INVALID_INPUT" {
  _make_repo
  output="$(bash "$SCRIPT" --repo-path "$REPO_DIR" --branch foo --base main --json 2>/dev/null || true)"
  [ "$(echo "$output" | _json_field ok)" = "False" ]
}

# ---------------------------------------------------------------------------
# Helpers: assert/refute branch existence
# ---------------------------------------------------------------------------

assert_branch_exists() {
  local branch="$1"
  result="$(git -C "$REPO_DIR" branch --list "$branch")"
  [[ -n "$result" ]]
}

refute_branch_exists() {
  local branch="$1"
  result="$(git -C "$REPO_DIR" branch --list "$branch")"
  [[ -z "$result" ]]
}
