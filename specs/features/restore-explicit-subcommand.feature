Feature: Restore is an explicit subcommand, not a read-path side effect (#460)

  As a developer who deliberately kills tmux sessions
  I want orchard reads (--json, TUI, daemon poll) to never resurrect them
  So that killed means killed, and restore happens only when I ask for it.

  Background:
    Given the session manifest at ~/.cache/orchard/session_manifest.json contains
      a dead-but-restorable cached session named "ghost"
    And tmux list-sessions does not include "ghost"
    And the worktree path for "ghost" still exists on disk

  # AC 1 — No restore from read paths
  @unit
  Scenario: Static check — restore_all_local is not called from read paths
    Given the orchard source tree
    When I grep for "restore_all_local(" under crates/orchard/src
    Then the only matches are inside crates/orchard/src/restore.rs
      and the new restore subcommand handler
    And neither crates/orchard/src/build_state.rs
      nor crates/orchard/src/tui/mod.rs contains a call to restore_all_local

  # AC 2 — Explicit subcommand exists and reports
  @e2e
  Scenario: orchard-tui restore invokes restore_all_local once and prints a report
    Given the manifest contains "ghost" and "phantom"
    And tmux list-sessions does not include either
    When I run "orchard-tui restore"
    Then restore_all_local is invoked exactly once
    And stdout contains a human-readable per-session line for "ghost"
      classified as Restored, Skipped(<reason>), or Failed(<step>)
    And stdout contains the same shape of line for "phantom"
    And the exit code is 0 on partial success
    And the exit code is non-zero only when classification or IO setup fails

  @integration
  Scenario: orchard restore (dispatcher shortcut) routes to orchard-tui restore
    Given the orchard-dispatcher binary is built
    When I run "orchard restore"
    Then orchard-tui restore is exec'd by the dispatcher
    And the same RestoreReport summary is printed

  # AC 3 — Killed sessions stay killed across every read path
  @e2e
  Scenario Outline: Read paths never resurrect killed sessions
    Given tmux kill-session -t "ghost" was just run
    When I run "<read_command>"
    Then "ghost" is not present in tmux list-sessions afterwards
    And restore_session was not invoked

    Examples:
      | read_command          |
      | orchard-tui --json    |
      | orchard-tui refresh   |
      | orchard-tui           |

  @integration
  Scenario: The watch daemon poll loop does not resurrect killed sessions
    Given tmux kill-session -t "ghost" was just run
    And the watch daemon is running
    When the daemon's next poll cycle completes
    Then "ghost" is not present in tmux list-sessions
    And restore_session was not invoked by the daemon

  @e2e
  Scenario: Explicit restore brings the session back
    Given tmux kill-session -t "ghost" was just run
    And several read commands have run since (none resurrected it)
    When I run "orchard restore"
    Then "ghost" is present in tmux list-sessions
    And the report classifies "ghost" as Restored

  # AC 4 — Regression test
  @integration
  Scenario: refresh_and_build does not invoke restore_session for cached dead entries
    Given the manifest contains a dead-but-restorable session
    And a tmux spy (or behavioural counter) records calls to restore_session
    When refresh_and_build is invoked
    Then the spy reports zero calls to restore_session
    And the test fails on main (where the read-path call still exists)
      and passes on this branch

  # AC 5 — Capture semantics unchanged
  @integration
  Scenario: tmux::refresh_local still writes session_manifest.json on session create
    Given session_manifest.json is empty
    When a new tmux session "fresh" is created
    And tmux::refresh_local runs
    Then session_manifest.json contains an entry for "fresh"
    And no other manifest-writer behaviour has changed

  @unit
  Scenario: Manifest writers continue to record session metadata
    Given a freshly captured TmuxSessionInfo
    When the manifest is written
    Then window layouts, pane working dirs, and pane active flags are persisted
      as before this change

  # AC 6 — Cooldown sentinel removed or repurposed
  @unit
  Scenario: Cooldown defenses are removed once read-path call storm is gone
    Given the restore module no longer needs to defend against per-read calls
    When I grep crates/orchard/src/restore.rs
    Then RESTORE_COOLDOWN, RESTORE_RAN, recently_attempted_restore_at,
      record_restore_attempt, and sentinel_path are absent
    And no code references the ~/.cache/orchard/restore_last_run sentinel file

  @unit
  Scenario: Two back-to-back explicit restores both run (no in-process guard)
    Given restore_all_local() has already been invoked once in this process
    When restore_all_local() is invoked again from the subcommand handler
    Then it executes the classifier and (where applicable) restore_session again
    And it does not short-circuit to an empty RestoreReport on the basis
      of having "already run"

  Out of scope:
    - Manifest expiry / hygiene (option 2 from issue body)
    - A restore_on_read config flag (option 3 from issue body)
    - Remote session restore (SkipReason::RemoteNotSupported is unchanged)
    - Changing capture semantics (when/how session_manifest.json is written)
    - TUI key binding for restore (subcommand-only for v1)

# --- AC Coverage Map ---
# AC 1 "No restore from read paths" → Scenario: Static check — restore_all_local is not called from read paths
# AC 2 "orchard-tui restore subcommand prints RestoreReport summary" → Scenarios:
#   - orchard-tui restore invokes restore_all_local once and prints a report
#   - orchard restore (dispatcher shortcut) routes to orchard-tui restore
# AC 3 "Killed sessions stay killed across --json, refresh, daemon, fresh TUI; reappear only after explicit restore" → Scenarios:
#   - Read paths never resurrect killed sessions (Outline: --json, refresh, TUI cold start)
#   - The watch daemon poll loop does not resurrect killed sessions
#   - Explicit restore brings the session back
# AC 4 "Regression test: read path does not invoke restore_session" → Scenario: refresh_and_build does not invoke restore_session for cached dead entries
# AC 5 "Capture path unchanged — tmux::refresh_local still writes session_manifest.json" → Scenarios:
#   - tmux::refresh_local still writes session_manifest.json on session create
#   - Manifest writers continue to record session metadata
# AC 6 "Cooldown sentinel removed or repurposed" → Scenarios:
#   - Cooldown defenses are removed once read-path call storm is gone
#   - Two back-to-back explicit restores both run (no in-process guard)
