Feature: Daemon-owned complete LOCAL cleanup of stale worktrees (worktree + dir + safe-branch + docker), TUI as interface
  As an orchardist clearing out worktrees whose PR is merged/closed or whose issue is done
  I want the daemon to perform a complete LOCAL cleanup — remove the worktree, its directory, its branch (only when safe), and its docker compose project — invoked by the TUI through a GraphQL mutation
  So that destruction stops happening in the TUI process (ADR-018), the active session is never destroyed, and a branch is never deleted off a stale or unconfirmed merged signal

  # Issue #693 — Phase 1 [P1] ONLY: complete LOCAL cleanup. The federated
  # remote-host cleanup (AC11-AC13) and the docker label-sweep (AC14), marked
  # [P2-DEFER] in the issue body, are explicitly OUT OF SCOPE here and are
  # NOT mapped below — they ship in their own follow-up issues. This file maps
  # AC1-AC10, AC-G1, AC-G2, AC-G3, AC-G5 (the Phase 1 done-bar) and ONLY those.
  #
  # Build approach (from the issue ## Plan): wire the orphaned daemon
  # `worktreeRemove` resolver into the schema, extend its script (and granular
  # per-op scripts) for dir/branch/docker, add the FIRST mutation method to the
  # read-only Rust daemon client, and rewire the LOCAL branch of
  # delete_task_row to call it. The two data-loss guards (AC-G1 active-session
  # exclusion, AC-G2 fail-closed branch-delete) are designed-in.
  #
  # Wire-shape note: scenarios are written to BEHAVIOR, not the wire shape. The
  # implementer may realize the cleanup as a batch `worktreesCleanup` mutation
  # OR as `worktreeRemove` looped client-side (see Plan "Batch vs single"); the
  # scenarios hold either way. Where a scenario says "the cleanup mutation" it
  # means whichever shape was chosen.
  #
  # Test-tree note (testing-philosophy.md): the daemon scenarios below
  # (resolver/script/schema) are bound by `daemon/**/*_test.go` + `*.bats`
  # (parity is zero-tolerance via `make check-feature-parity-daemon`); the TUI
  # rewire scenario (AC7) is bound by `crates/orchard/**/*.rs`. This file is the
  # create-issue chain's contract; coders bind the scenarios in the matching
  # test tree per the @scenario annotation convention.

  Background:
    Given the daemon serves a GraphQL schema at 127.0.0.1:7777
    And the daemon already carries an orphaned WorktreeRemove resolver and a scripts/git/worktree-remove.sh that today only runs `git worktree remove`
    And the TUI today destroys local worktrees IN-PROCESS via delete_task_row (worktree_core::remove_worktree + tmux::kill_tmux_session_safe), in violation of ADR-018
    And a worktree's stale-ness is "PR state in {merged,closed} OR issue state in {completed,closed}" per the existing filter_stale
    And "the cleanup mutation" denotes the served daemon mutation that performs complete local cleanup for a set of stale worktrees

  # =======================================================================
  # AC1 — The worktree cleanup mutation is served (wire the orphaned resolver)
  # =======================================================================

  @integration @issue-693
  Scenario: Live-daemon introspection lists the worktree cleanup mutation field
    Given the daemon schema with the worktree mutation block wired in
    When the GraphQL introspection query "{ __type(name: \"Mutation\") { fields { name } } }" is executed against the live daemon
    Then the returned fields array contains the worktree cleanup mutation field
    And the field is absent before this change, where introspection returned only "launchSession" and "sendTextToPane"

  @unit @issue-693
  Scenario: The wired resolver execs the script at its real on-disk path, not a non-existent sibling
    Given the resolver path-builder previously constructed "<scriptRoot>/git-worktree-remove.sh"
    And the script actually lives at "<scriptRoot>/git/worktree-remove.sh"
    When the cleanup mutation runs against a stale worktree fixture
    Then the resolver execs the script at "<scriptRoot>/git/worktree-remove.sh"
    And the mutation does not fail with a missing-script error

  # =======================================================================
  # AC2 — Stale set = closed PR OR closed Issue; open-PR worktree untouched
  # =======================================================================

  @integration @issue-693
  Scenario: Cleanup operates on exactly the stale set and leaves an open-PR worktree fully intact
    Given a fixture of local worktrees with mixed PR and issue states
    And one worktree has an OPEN PR and an OPEN issue
    When the cleanup mutation runs over the fleet
    Then the set of worktrees acted on equals the filter_stale output (PR in {merged,closed} OR issue in {completed,closed})
    And after the run "git worktree list" still lists the open-PR worktree
    And "git branch" still lists the open-PR worktree's branch
    And the open-PR worktree's directory and containers are still present

  # =======================================================================
  # AC3 — Worktree + directory removed, with the force / prune fallback
  # =======================================================================

  @integration @issue-693
  Scenario: A clean stale worktree is removed and its directory is gone
    Given a clean stale local worktree at path "<wt>"
    When the cleanup mutation runs
    Then "git worktree list --porcelain" does not list "<wt>"
    And "test ! -d <wt>" exits 0

  @integration @issue-693
  Scenario: A dirty stale worktree is removed via the force path
    Given a stale local worktree at "<wt>" with uncommitted changes
    When the cleanup mutation runs
    Then the worktree is removed via the force path
    And "<wt>" no longer exists on disk

  @integration @issue-693
  Scenario: A locked or submodule-gitlink worktree falls back to rm -rf plus git worktree prune
    Given a stale local worktree at "<wt>" for which "git worktree remove --force" itself fails because the worktree is locked or has submodule gitlinks
    When the cleanup mutation runs
    Then cleanup falls back to "rm -rf <wt>" followed by "git worktree prune"
    And "git worktree list --porcelain" no longer lists "<wt>"
    And "test ! -d <wt>" exits 0

  @integration @issue-693
  Scenario: A worktree dir deleted out-of-band but still registered is reconciled by prune
    Given a stale local worktree whose directory was already manually deleted but is still registered in git
    When the cleanup mutation runs
    Then "git worktree prune" reconciles the registration
    And "git worktree list --porcelain" no longer lists it

  # =======================================================================
  # AC4 — Branch deleted only when SAFE; else skip-and-warn (local)
  # =======================================================================

  @integration @issue-693
  Scenario: A fully-merged unprotected branch is deleted
    Given a stale local worktree whose branch is not the default, not in the protected set, and fully merged into its base
    When the cleanup mutation runs
    Then "git branch --list <branch>" returns empty after the run

  @integration @issue-693
  Scenario: The repo default branch is skipped and warned, never deleted
    Given a stale local worktree whose branch is the repo default branch
    When the cleanup mutation runs
    Then "git branch --list <branch>" still lists the branch
    And the result carries a branch-skip warning naming the branch with reason "default-branch"

  @integration @issue-693
  Scenario: A protected branch is skipped and warned
    Given a branch-protected set is configured (a NET-NEW config, distinct from the session-name PROTECTED_SESSION_KEEPERS)
    And a stale local worktree whose branch is in that protected set
    When the cleanup mutation runs
    Then "git branch --list <branch>" still lists the branch
    And the result carries a branch-skip warning naming the branch with reason "protected"

  @integration @issue-693
  Scenario: An unmerged branch behind a non-merged closed PR is skipped and warned
    Given a stale local worktree whose PR is closed-not-merged and whose branch is not merged into its base
    When the cleanup mutation runs
    Then "git branch --list <branch>" still lists the branch
    And the result carries a branch-skip warning naming the branch with reason "not-merged"

  @integration @issue-693
  Scenario: A merged PR with unpushed local commits ahead of the merge does not authorize branch deletion
    Given a stale local worktree whose PR state is "merged" but whose local branch is ahead of its upstream merge-base
    And "git rev-list <branch> --not <upstream>" is non-empty
    When the cleanup mutation runs
    Then "git branch --list <branch>" still lists the branch
    And the result carries a branch-skip warning naming the branch with reason "local-commits-ahead-of-merge"

  # =======================================================================
  # AC-G1 — The worktree hosting the active session is never destroyed (data-loss)
  # =======================================================================

  @integration @issue-693
  Scenario: The active-session worktree is excluded from all destruction while siblings are cleaned
    Given a stale fixture worktree "<active>" that hosts the user's active session
    And a sibling stale worktree "<sibling>" that hosts no active session
    And the active-session identity (session name and/or cwd) is passed explicitly from the TUI into the cleanup mutation input
    When the cleanup mutation runs
    Then "git worktree list --porcelain" still lists "<active>"
    And "test -d <active>" exits 0
    And the result carries a skip entry for "<active>" with reason "hosts-active-session"
    And "<sibling>" is gone from "git worktree list --porcelain"

  @unit @issue-693
  Scenario: The daemon does not infer active-session identity from its own process environment
    Given the daemon process whose own current_session_name() reads the daemon's $TMUX and returns None
    When the cleanup mutation decides whether a worktree hosts the active session
    Then the decision uses ONLY the active-session identity passed in the mutation input
    And the #369 self-kill guard is not relied on, because the daemon move defeats it (the guard passes through when current_session_name() is None)

  @integration @issue-693
  Scenario: When no worktree in the stale set hosts the active session, the full set is cleaned
    Given a stale set in which no worktree hosts the active session
    And an active-session identity that matches none of them
    When the cleanup mutation runs
    Then no worktree is skipped for "hosts-active-session"
    And every stale worktree is removed

  # =======================================================================
  # AC-G2 — Branch-delete fails CLOSED when merged-state is unavailable (data-loss)
  # =======================================================================

  @integration @issue-693
  Scenario: A gh enrichment error skips the branch deletion but still removes the worktree and dir
    Given a stale local worktree
    And the daemon's gh service errors or is rate-limited when consulted for PR-merged state
    When the cleanup mutation runs
    Then "git branch --list <branch>" still lists the branch
    And the result carries a branch-skip warning with reason "merged-state-unavailable"
    And the worktree and its directory were still removed

  @integration @issue-693
  Scenario: gh and git must AGREE on merged before a branch is deleted
    Given a stale local worktree whose PR reports "merged" but whose "git branch --merged <base>" disagrees (the branch is not merged locally)
    When the cleanup mutation runs
    Then the branch is treated as NOT-merged
    And "git branch --list <branch>" still lists the branch
    And the result carries a branch-skip warning for the merged-state disagreement

  # =======================================================================
  # AC-G3 — A tmux session-kill failure is reported but does not abort that worktree's removal
  # =======================================================================

  @integration @issue-693
  Scenario: A tmux-kill failure becomes a non-fatal warning while the worktree is still removed
    Given a stale local worktree whose tmux session-kill fails because the session is busy or undead
    When the cleanup mutation runs
    Then the worktree is gone from "git worktree list --porcelain"
    And the result carries a per-worktree NON-FATAL warning with stage "tmux-kill" for that worktree
    And that worktree's cleanup is NOT marked failed, because the destructive stages succeeded

  # =======================================================================
  # AC5 — Docker compose project torn down (mode a), collision-safe
  # =======================================================================

  @integration @issue-693
  Scenario: Two same-basename compose projects under different paths tear down independently
    Given two stale local worktrees sharing the same leaf directory name under different repo paths
    And each worktree directory is a docker-compose project that is currently up
    When the cleanup mutation runs on the first worktree only
    Then "docker compose -p <keyA> ps" is empty for the cleaned worktree
    And "docker compose -p <keyB> ps" still shows the other worktree's containers running
    And the project key is derived from the worktree's stable identity (full-path hash or "<project_id>:<name>"), not the bare directory basename

  @integration @issue-693
  Scenario: Compose teardown removes built images but leaves registry-pulled images intact
    Given a stale local worktree whose directory is a docker-compose project mixing a locally-built image and a registry-pulled image
    When the cleanup mutation runs
    Then "docker compose -p <key> down --volumes --rmi local" removes containers, networks, named and anonymous volumes, and only the locally-built image
    And the registry-pulled image is left intact

  # =======================================================================
  # AC6 — Docker-absent is a clean no-op
  # =======================================================================

  @unit @issue-693
  Scenario: Cleanup on a host with no docker binary completes with no docker error
    Given a stale local worktree on a host with "docker" masked off PATH
    When the cleanup mutation runs
    Then the worktree, its branch, and its directory are handled
    And the result envelope is "ok:true" for the worktree
    And the result carries no entry with stage "docker"

  @unit @issue-693
  Scenario: Cleanup on a non-compose directory emits no docker error
    Given a stale local worktree whose directory contains no docker-compose.y*ml or compose.y*ml
    When the cleanup mutation runs
    Then the result envelope is "ok:true" for the worktree
    And the result carries no entry with stage "docker"

  # =======================================================================
  # AC7 — TUI no longer execs local destruction (ADR-018 invariant)
  # =======================================================================

  @integration @issue-693
  Scenario: The TUI local-cleanup path invokes the daemon mutation and execs no local destruction
    Given a LOCAL stale worktree row in the TUI Cleanup flow
    When the local-row branch of delete_task_row (or its replacement) handles that row
    Then it calls the daemon-client cleanup mutation method by name (the positive route)
    And that branch contains ZERO calls to worktree_core::remove_worktree, tmux::kill_tmux_session_safe, or remote:: destruction (the negative absence)
    And both the positive route and the negative absence are asserted

  # =======================================================================
  # AC8 — Partial failure mid-cleanup is reported, not swallowed; cleanup continues
  # =======================================================================

  @integration @issue-693
  Scenario: A failure on one worktree does not stop the others and is surfaced per-worktree
    Given N stale worktrees where cleanup of worktree K fails (e.g. branch-delete denied or docker teardown errors)
    When the cleanup mutation runs over all N
    Then the remaining N-1 worktrees are still attempted and complete
    And the result surfaces a per-worktree error for K naming its id, the failing stage, and a message
    And the TUI renders this via the existing AppMsg::CleanupDone { deleted, errors } channel

  # =======================================================================
  # AC-G5 — Concurrent cleanup is serialized or each op is individually race-safe
  # =======================================================================

  @integration @issue-693
  Scenario: Two concurrent cleanups over an overlapping set both succeed without a hard race error
    Given two cleanup operations issued concurrently against an overlapping stale set
    When both run against the same fixture
    Then both return "ok:true"
    And the union of removals equals the stale set exactly once (no double-removal hard error)
    And neither returns a hard error for a doubly-targeted worktree (either serialized, or each destructive stage tolerates "already done by a peer" as a skip)

  # =======================================================================
  # AC9 — Idempotent re-run on an already-clean fleet
  # =======================================================================

  @integration @issue-693
  Scenario: A second cleanup run on an already-clean fleet is a clean no-op
    Given a fleet on which the cleanup mutation has already run once to completion
    When the cleanup mutation runs a second time
    Then the second run reports zero worktree removals, zero branch deletions, and zero docker teardowns
    And the result is "ok:true" with an empty deleted set and no "already removed" error
    And "git worktree list" is unchanged between the end of run 1 and the end of run 2

  # =======================================================================
  # AC10 — Mutation declares input validation + idempotency + typed errors (RULES M4/M5/S9)
  # =======================================================================

  @unit @issue-693
  Scenario: Malformed input is rejected at the resolver boundary with a typed error
    Given a cleanup mutation call with malformed input (e.g. an empty or malformed worktree id)
    When the resolver validates the input
    Then it returns a typed INVALID_INPUT error at the resolver boundary
    And it does not crash deep in the script

  @unit @issue-693
  Scenario: Expected failures are returned as typed structured results, not opaque GraphQL errors
    Given cleanup encounters an expected failure (branch-protected, docker-missing-but-required, or worktree-not-found)
    When the mutation returns
    Then the failure is a typed or structured result entry, not an opaque GraphQL errors[] entry

  @unit @issue-693
  Scenario: The schema documents the mutation's idempotency stance with the exact literal
    Given the wired mutation's schema doc string
    When it is inspected with "grep -F \"Idempotency: idempotent\"" against schema.graphql
    Then the literal "Idempotency: idempotent" is present
    And it mirrors the literal already carried by daemon/git/mutations.go

  # --- AC Coverage Map ---
  # (Phase 1 [P1] ACs ONLY. AC11, AC12, AC13, AC14 are [P2-DEFER] in the issue
  #  body — out of scope for this issue, intentionally NOT mapped here.)
  #
  # AC1: "Worktree cleanup mutation is served"
  #   -> @integration "Live-daemon introspection lists the worktree cleanup mutation field"
  #   -> @unit "The wired resolver execs the script at its real on-disk path, not a non-existent sibling"
  #
  # AC2: "Stale set = closed PR OR closed Issue"
  #   -> @integration "Cleanup operates on exactly the stale set and leaves an open-PR worktree fully intact"
  #
  # AC3: "Worktree + directory removed" (clean, dirty, locked/submodule, already-pruned)
  #   -> @integration "A clean stale worktree is removed and its directory is gone"
  #   -> @integration "A dirty stale worktree is removed via the force path"
  #   -> @integration "A locked or submodule-gitlink worktree falls back to rm -rf plus git worktree prune"
  #   -> @integration "A worktree dir deleted out-of-band but still registered is reconciled by prune"
  #
  # AC4: "Branch deleted only when SAFE; else skip-and-warn (local)"
  #   -> @integration "A fully-merged unprotected branch is deleted"
  #   -> @integration "The repo default branch is skipped and warned, never deleted"
  #   -> @integration "A protected branch is skipped and warned"
  #   -> @integration "An unmerged branch behind a non-merged closed PR is skipped and warned"
  #   -> @integration "A merged PR with unpushed local commits ahead of the merge does not authorize branch deletion"
  #
  # AC-G1: "The worktree hosting the active session is never destroyed (data-loss)"
  #   -> @integration "The active-session worktree is excluded from all destruction while siblings are cleaned"
  #   -> @unit "The daemon does not infer active-session identity from its own process environment"
  #   -> @integration "When no worktree in the stale set hosts the active session, the full set is cleaned"
  #
  # AC-G2: "Branch-delete fails CLOSED when merged-state is unavailable (data-loss)"
  #   -> @integration "A gh enrichment error skips the branch deletion but still removes the worktree and dir"
  #   -> @integration "gh and git must AGREE on merged before a branch is deleted"
  #
  # AC-G3: "A tmux session-kill failure is reported but does not abort that worktree's removal"
  #   -> @integration "A tmux-kill failure becomes a non-fatal warning while the worktree is still removed"
  #
  # AC5: "Docker compose project torn down (mode a), collision-safe"
  #   -> @integration "Two same-basename compose projects under different paths tear down independently"
  #   -> @integration "Compose teardown removes built images but leaves registry-pulled images intact"
  #
  # AC6: "Docker-absent is a clean no-op"
  #   -> @unit "Cleanup on a host with no docker binary completes with no docker error"
  #   -> @unit "Cleanup on a non-compose directory emits no docker error"
  #
  # AC7: "TUI no longer execs local destruction (ADR-018 invariant)"
  #   -> @integration "The TUI local-cleanup path invokes the daemon mutation and execs no local destruction"
  #
  # AC8: "Partial failure mid-cleanup is reported, not swallowed; cleanup continues"
  #   -> @integration "A failure on one worktree does not stop the others and is surfaced per-worktree"
  #
  # AC-G5: "Concurrent cleanup is serialized or each op is individually safe under races"
  #   -> @integration "Two concurrent cleanups over an overlapping set both succeed without a hard race error"
  #
  # AC9: "Idempotent re-run on an already-clean fleet"
  #   -> @integration "A second cleanup run on an already-clean fleet is a clean no-op"
  #
  # AC10: "Mutation declares input validation + idempotency + typed errors (RULES M4/M5/S9)"
  #   -> @unit "Malformed input is rejected at the resolver boundary with a typed error"
  #   -> @unit "Expected failures are returned as typed structured results, not opaque GraphQL errors"
  #   -> @unit "The schema documents the mutation's idempotency stance with the exact literal"
