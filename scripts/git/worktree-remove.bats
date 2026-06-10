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

# ---------------------------------------------------------------------------
# AC (#693 daemon-owned cleanup): a worktree whose <projectId> slug is NOT in
# the orchard config must NOT hard-fail with REPO_NOT_FOUND. It returns
# ok:true with skipped:true / skipReason:repo-unregistered and performs ZERO
# filesystem mutation (the repo is unresolvable, so nothing is safe to remove).
# This mirrors the hosts-active-session skip envelope.
# @scenario An unregistered-repo worktree is skipped (repo-unregistered) instead of erroring
# ---------------------------------------------------------------------------

@test "repo not found in config: ok=true, skipped=true, skipReason=repo-unregistered" {
  config="$TMPDIR_CFG/config.json"
  printf '{"repos":[]}' > "$config"
  output="$(ORCHARD_CONFIG="$config" "$SCRIPT" --json --worktree-id "unknownrepo:mybranch" 2>/dev/null || true)"

  # Envelope must be ok=true (skip is non-fatal, NOT a REPO_NOT_FOUND error).
  [ "$(echo "$output" | _json_field ok)" = "True" ]

  # data.skipped must be true.
  skipped="$(echo "$output" | python3 -c "
import json,sys
d=json.load(sys.stdin)
print(d.get('data',{}).get('skipped','MISSING'))
" 2>/dev/null || echo "PARSE_ERROR")"
  [ "$skipped" = "True" ]

  # data.skipReason must be repo-unregistered.
  skip_reason="$(echo "$output" | python3 -c "
import json,sys
d=json.load(sys.stdin)
print(d.get('data',{}).get('skipReason','MISSING'))
" 2>/dev/null || echo "PARSE_ERROR")"
  [ "$skip_reason" = "repo-unregistered" ]

  # No REPO_NOT_FOUND substring anywhere in the output.
  [[ "$output" != *"REPO_NOT_FOUND"* ]]
}

@test "unregistered repo with a real config: ok=true, skipReason=repo-unregistered, zero fs mutation, exit 0" {
  # Build a config that registers ONE repo (slug myrepo) but the worktree-id
  # below targets a DIFFERENT, unregistered slug. The registered repo and its
  # on-disk worktree must be left completely untouched.
  _make_repo
  _add_worktree "feature-untouched"
  UNTOUCHED_WT_DIR="$WT_DIR"
  cfg="$(_write_config)"

  # Sentinel directory: a path the script must never create or remove.
  SENTINEL_DIR="$(mktemp -d)"
  touch "$SENTINEL_DIR/keep"

  # Run against an unregistered slug, capturing BOTH stdout and the exit code.
  # The `|| status=$?` form (same idiom the other tests use with `|| true`)
  # keeps bats from aborting the test on the CURRENT exit-1 behavior so the
  # explicit assertions below run. Under the NEW contract the script exits 0.
  status=0
  output="$(ORCHARD_CONFIG="$cfg" bash "$SCRIPT" --json --worktree-id "langwatch/langwatch-saas:issue510" 2>/dev/null)" || status=$?

  # Exit code 0 (skip is success, not failure).
  [ "$status" -eq 0 ]

  # Envelope ok=true.
  [ "$(echo "$output" | _json_field ok)" = "True" ]

  # skipReason repo-unregistered.
  skip_reason="$(echo "$output" | python3 -c "
import json,sys
d=json.load(sys.stdin)
print(d.get('data',{}).get('skipReason','MISSING'))
" 2>/dev/null || echo "PARSE_ERROR")"
  [ "$skip_reason" = "repo-unregistered" ]

  # No REPO_NOT_FOUND error.
  [[ "$output" != *"REPO_NOT_FOUND"* ]]

  # ZERO filesystem mutation: the registered repo's unrelated worktree dir and
  # the sentinel must both still exist (nothing was removed).
  [ -d "$UNTOUCHED_WT_DIR" ]
  [ -d "$SENTINEL_DIR" ]
  [ -f "$SENTINEL_DIR/keep" ]

  rm -rf "$SENTINEL_DIR"
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
# AC-G1(i) — ~150: active worktree is excluded; sibling is cleaned
# @scenario The active-session worktree is excluded from all destruction
#           while siblings are cleaned
# ---------------------------------------------------------------------------

@test "AC-G1(i) active-cwd match: active worktree skipped with hosts-active-session; sibling removed" {
  _make_repo
  _add_worktree "feature-active"
  ACTIVE_WT_DIR="$WT_DIR"
  _add_worktree "feature-sibling"
  SIBLING_WT_DIR="$WT_DIR"
  cfg="$(_write_config)"

  # Remove the ACTIVE worktree — passing its path as --active-cwd
  output="$(ORCHARD_CONFIG="$cfg" "$SCRIPT" \
    --json \
    --worktree-id "myrepo:feature-active" \
    --active-cwd "$ACTIVE_WT_DIR" \
    2>/dev/null)"

  # Envelope ok=true (skip is non-fatal)
  [ "$(echo "$output" | _json_field ok)" = "True" ]

  # skipReason must be hosts-active-session
  skip_reason="$(echo "$output" | python3 -c "
import json,sys
d=json.load(sys.stdin)
print(d.get('data',{}).get('skipReason','MISSING'))
" 2>/dev/null || echo "PARSE_ERROR")"
  [ "$skip_reason" = "hosts-active-session" ]

  # Active worktree directory MUST still exist
  [ -d "$ACTIVE_WT_DIR" ]

  # Active worktree MUST still be registered.
  # Use the realpath-resolved path for the porcelain grep — macOS mktemp returns
  # /var/... but git worktree list --porcelain shows /private/var/... (resolved).
  ACTIVE_WT_DIR_REAL="$(python3 -c "import os,sys; print(os.path.realpath(sys.argv[1]))" "$ACTIVE_WT_DIR" 2>/dev/null || echo "$ACTIVE_WT_DIR")"
  git -C "$REPO_DIR" worktree list --porcelain 2>/dev/null | grep -qF "worktree $ACTIVE_WT_DIR_REAL"

  # Now remove the sibling (no active-cwd constraint) — it must succeed
  output2="$(ORCHARD_CONFIG="$cfg" "$SCRIPT" \
    --json \
    --worktree-id "myrepo:feature-sibling" \
    2>/dev/null)"
  [ "$(echo "$output2" | _json_field ok)" = "True" ]
  [ ! -d "$SIBLING_WT_DIR" ]
  refute_in_porcelain "$REPO_DIR" "$SIBLING_WT_DIR"
}

# ---------------------------------------------------------------------------
# AC-G1(ii) — ~161: daemon does NOT infer active-session from its own $TMUX
# @scenario The daemon does not infer active-session identity from its own
#           process environment
# ---------------------------------------------------------------------------

@test "AC-G1(ii) daemon env has fake TMUX but exclusion keys off passed --active-cwd only" {
  _make_repo
  _add_worktree "feature-envtest"
  ENVTEST_WT_DIR="$WT_DIR"
  cfg="$(_write_config)"

  # Set a fake $TMUX in the daemon-process env — must NOT influence the guard.
  # Pass --active-cwd that does NOT match the worktree path.
  # Result: worktree must be REMOVED (the fake $TMUX value is irrelevant).
  output="$(TMUX="/tmp/fake-tmux-socket,1,0" ORCHARD_CONFIG="$cfg" "$SCRIPT" \
    --json \
    --worktree-id "myrepo:feature-envtest" \
    --active-cwd "/totally/different/path" \
    2>/dev/null)"

  # Worktree must be removed — env $TMUX is ignored
  [ "$(echo "$output" | _json_field ok)" = "True" ]
  [ ! -d "$ENVTEST_WT_DIR" ]
  refute_in_porcelain "$REPO_DIR" "$ENVTEST_WT_DIR"

  # Confirm no skip entry in output (worktree was not excluded)
  skipped="$(echo "$output" | python3 -c "
import json,sys
d=json.load(sys.stdin)
print(d.get('data',{}).get('skipped','false'))
" 2>/dev/null || echo "false")"
  [ "$skipped" != "True" ]
}

# ---------------------------------------------------------------------------
# AC-G1(iii) — ~168: when no worktree hosts active session, full set cleaned
# @scenario When no worktree in the stale set hosts the active session,
#           the full set is cleaned
# ---------------------------------------------------------------------------

@test "AC-G1(iii) active-cwd matches nothing: worktree fully removed, no skip" {
  _make_repo
  _add_worktree "feature-nomatch"
  cfg="$(_write_config)"

  # Pass --active-cwd that does NOT match this worktree
  output="$(ORCHARD_CONFIG="$cfg" "$SCRIPT" \
    --json \
    --worktree-id "myrepo:feature-nomatch" \
    --active-cwd "/does/not/match/anything" \
    2>/dev/null)"

  # Worktree must be fully removed
  [ "$(echo "$output" | _json_field ok)" = "True" ]
  [ ! -d "$WT_DIR" ]
  refute_in_porcelain "$REPO_DIR" "$WT_DIR"

  # No skip in the envelope
  skipped="$(echo "$output" | python3 -c "
import json,sys
d=json.load(sys.stdin)
print(d.get('data',{}).get('skipped','false'))
" 2>/dev/null || echo "false")"
  [ "$skipped" != "True" ]
}

# ---------------------------------------------------------------------------
# AC-G3 — ~201: tmux-kill failure is non-fatal; worktree still removed
# @scenario A tmux-kill failure becomes a non-fatal warning while the
#           worktree is still removed
# ---------------------------------------------------------------------------

@test "AC-G3 tmux-kill failure: non-fatal warning in envelope, worktree still removed" {
  _make_repo
  _add_worktree "feature-tmuxfail"
  cfg="$(_write_config)"

  # Pass a --tmux-session name that does NOT exist in the test environment.
  # tmux has-session will fail (no such session), which maps to the
  # session-not-found no-op path (killed:false, reason:session-not-found).
  #
  # To test the FAILURE path (kill attempt that fails), we create a real
  # tmux session and then pass a name that tmux has-session finds but
  # kill-session will handle.  Since we cannot inject a kill failure easily
  # in a unit test, we assert the warning path by using a session name whose
  # kill succeeds — the important invariant is that the worktree is STILL
  # removed regardless.  The bats test proves the envelope shape.
  #
  # Actually assert the non-fatal WARNING shape by passing a fake session
  # name that does not exist: tmuxKill.reason == "session-not-found" is
  # the clean no-op path.  The AC says "failure becomes non-fatal warning
  # while worktree still removed" — both outcomes (kill-failed + kill-notfound)
  # satisfy "does not abort removal".
  NONEXISTENT_SESSION="__bats_nonexistent_session_$$"

  output="$(ORCHARD_CONFIG="$cfg" "$SCRIPT" \
    --json \
    --worktree-id "myrepo:feature-tmuxfail" \
    --tmux-session "$NONEXISTENT_SESSION" \
    2>/dev/null)"

  # Worktree must still be removed (tmux-kill non-fatal)
  [ "$(echo "$output" | _json_field ok)" = "True" ]
  [ ! -d "$WT_DIR" ]
  refute_in_porcelain "$REPO_DIR" "$WT_DIR"

  # tmuxKill must be present in envelope (not null)
  tmux_kill_stage="$(echo "$output" | python3 -c "
import json,sys
d=json.load(sys.stdin)
tk=d.get('data',{}).get('tmuxKill',None)
print('null' if tk is None else 'present')
" 2>/dev/null || echo "PARSE_ERROR")"
  [ "$tmux_kill_stage" = "present" ]

  # tmuxKill.stage must be tmux-kill
  tmux_kill_stage_name="$(echo "$output" | python3 -c "
import json,sys
d=json.load(sys.stdin)
tk=d.get('data',{}).get('tmuxKill',{}) or {}
print(tk.get('stage','MISSING'))
" 2>/dev/null || echo "PARSE_ERROR")"
  [ "$tmux_kill_stage_name" = "tmux-kill" ]

  # Cleanup result must NOT be marked failed (ok=true proves this)
  [ "$(echo "$output" | _json_field ok)" = "True" ]
}

# ---------------------------------------------------------------------------
# AC-G3 kill-FAILURE: tmux has-session succeeds but kill-session fails;
# worktree is still removed and the warning is surfaced in the envelope.
# @scenario Stubs tmux so kill-session exits 1 without a real tmux server.
# ---------------------------------------------------------------------------

@test "AC-G3: tmux kill-session failure is non-fatal — worktree still removed, warning surfaced" {
  _make_repo
  _add_worktree "feature-killerfail"
  cfg="$(_write_config)"

  # Create a fake tmux binary: has-session exits 0 (session "exists"),
  # kill-session exits 1 with an error message on stderr.
  FAKE_BIN="$(mktemp -d)"
  cat > "$FAKE_BIN/tmux" <<'TMUX_STUB'
#!/usr/bin/env bash
if [[ "$1" == "has-session" ]]; then
  exit 0
fi
if [[ "$1" == "kill-session" ]]; then
  echo "simulated kill-session failure" >&2
  exit 1
fi
exit 0
TMUX_STUB
  chmod +x "$FAKE_BIN/tmux"

  output="$(PATH="$FAKE_BIN:$PATH" ORCHARD_CONFIG="$cfg" "$SCRIPT" \
    --json \
    --worktree-id "myrepo:feature-killerfail" \
    --tmux-session "any-session-name" \
    2>/dev/null)"

  # Worktree must still be removed despite the kill failure
  [ "$(echo "$output" | _json_field ok)" = "True" ]
  [ ! -d "$WT_DIR" ]
  refute_in_porcelain "$REPO_DIR" "$WT_DIR"

  # tmuxKill must carry the warning field (non-fatal kill failure)
  tmux_kill_warning="$(echo "$output" | python3 -c "
import json,sys
d=json.load(sys.stdin)
tk=d.get('data',{}).get('tmuxKill',{}) or {}
print(tk.get('warning','MISSING'))
" 2>/dev/null || echo "PARSE_ERROR")"
  [[ "$tmux_kill_warning" == *"tmux kill-session failed"* ]]

  rm -rf "$FAKE_BIN"
}

# ---------------------------------------------------------------------------
# Helper: assert the given worktree path is NOT in git worktree list --porcelain
#
# On macOS, mktemp creates paths under /var/... but git worktree list --porcelain
# resolves them to /private/var/... (the physical path under the symlink).
# We normalize both the needle and the porcelain output to their /private/...
# form so a stale registration is never silently missed.
#
# The directory may already be removed (that is the point of calling refute).
# When the dir is gone `cd` fails, so we do pure-string normalization:
# if the path starts with /var/ and /private/var exists, prepend /private.
# ---------------------------------------------------------------------------
_normalize_path() {
  local p="$1"
  # Try physical resolution first (works when dir still exists).
  local resolved
  resolved="$(cd "$p" 2>/dev/null && pwd -P 2>/dev/null)" && { printf '%s' "$resolved"; return; }
  # Dir is gone — fall back to string-level macOS symlink normalization.
  if [[ "$p" == /var/* ]] && [[ -d /private/var ]]; then
    printf '%s' "/private${p}"
  else
    printf '%s' "$p"
  fi
}

refute_in_porcelain() {
  local repo="$1" wt_path="$2"
  local wt_real
  wt_real="$(_normalize_path "$wt_path")"
  ! git -C "$repo" worktree list --porcelain 2>/dev/null | grep -qF "worktree $wt_real"
}
