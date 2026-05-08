Feature: Session restore — reconstruct tmux geometry + cwds (#190)

  As a developer who shuts down or reboots their machine
  I want orchard to auto-rebuild my tmux sessions on startup
  So that window layouts, pane splits, and per-pane working directories come back
  — the user re-launches their own tools (including `claude`) themselves.

  Background:
    Given the tmux cache file at ~/.cache/orchard/tmux_sessions.json captures
      | window layouts          | #{window_layout}        |
      | pane working dirs       | #{pane_current_path}    |
      | pane active flags       | #{pane_active}          |
    And CachedTmuxSession deserializes old (pre-#190) cache files without error

  @unit
  Scenario: The pure classifier skips already-running sessions
    Given a cached session named "foo"
    And tmux list-sessions includes "foo"
    When restore() is called
    Then the session is classified Skipped(AlreadyRunning)

  @unit
  Scenario: The pure classifier skips remote sessions in v1
    Given a cached session with host=Some("boxd")
    When restore() is called
    Then the session is classified Skipped(RemoteNotSupported)

  @unit
  Scenario: The pure classifier skips sessions whose worktree no longer exists
    Given a cached session whose path is not on disk
    When restore() is called
    Then the session is classified Skipped(WorktreeGone)

  @unit
  Scenario: The pure classifier produces a plan for restorable sessions
    Given a cached session named "bar" with host=None and worktree on disk
    And tmux list-sessions does not include "bar"
    When restore() is called
    Then a RestorePlan for "bar" is produced

  @integration
  Scenario: restore_session rebuilds a multi-pane window against live tmux
    Given a cached session with 1 window and 2 panes at distinct cwds
    When restore_session() is invoked
    Then tmux has-session returns 0 for the session name
    And tmux list-panes reports 2 panes
    And each pane's current path matches its saved cwd

  # NOTE: Issue #460 removed the OnceLock + cross-process cooldown that
  # used to short-circuit repeated calls to restore_all_local. The user is
  # now the only trigger (`orchard restore` subcommand), so per-process and
  # per-cooldown short-circuits are no longer needed. See
  # specs/features/restore-explicit-subcommand.feature for current contract.

  @unit
  Scenario: live_local_session_names returns None on tmux error
    Given tmux list-sessions exits non-zero with an unexpected error
    When live_local_session_names() is called
    Then the result is None
    And restore_all_local() does NOT recreate any cached sessions
       (avoiding kill-session against a live server)

  @unit
  Scenario: Cache backward-compat — old JSON deserializes
    Given a tmux_sessions.json written before #190 (no new fields)
    When it is deserialized as CachedTmuxSession
    Then window_layouts, pane_paths, pane_active are empty Vec

  @unit
  Scenario: Input validation rejects shell-unsafe cache values
    Given a cached session with a pane path containing a newline
    When restore_session is invoked
    Then the outcome is Failed(InputValidation)
    And no tmux subprocess is spawned

  @unit
  Scenario: JSON output exposes restore-relevant fields
    Given a PaneState with cwd and is_active set
    And a WindowState with a layout string
    When --json is emitted
    Then the JSON pane object contains "cwd" and "isActive"
    And the JSON window object contains "layout"

  Out of scope:
    - Remote session restore (tracked separately)
    - Auto-resuming Claude conversations — the user relaunches `claude` themselves
    - Persisting Claude session IDs in the cache (the hook files in $TMPDIR
      remain the live source of truth for telemetry)
    - Shell history replay
    - TUI "restored" indicator (dropped — user verifies via the session list itself)
    # Restore is now invoked explicitly via `orchard restore` (see #460);
    # the v1 "automatic on startup" model was reversed.
