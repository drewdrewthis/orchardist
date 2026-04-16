Feature: Session restore — reconstruct tmux sessions from persisted state (#190)

  As a developer who shuts down or reboots their machine
  I want orchard to auto-reconstruct my tmux sessions on startup
  So that pane layouts, cwds, and Claude conversations come back without manual effort

  Background:
    Given the tmux cache file at ~/.cache/orchard/tmux_sessions.json captures
      | window layouts          | #{window_layout}        |
      | pane working dirs       | #{pane_current_path}    |
      | pane active flags       | #{pane_active}          |
      | Claude session IDs      | per-pane map            |
    And CachedTmuxSession deserializes old (pre-#190) cache files without error
    And the SessionManifestEntry gains a claude_session_id fallback

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
    Given a cached session with 1 window and 2 panes at /tmp
    When restore_session() is invoked
    Then tmux has-session returns 0 for the session name
    And tmux list-panes reports 2 panes
    And each pane's current path is /tmp

  @unit
  Scenario: Shell-quote passes safe paths unchanged
    When shell_quote("/home/user/proj") is called
    Then the result is "/home/user/proj"

  @unit
  Scenario: Shell-quote wraps paths with spaces
    When shell_quote("/home/user/my proj") is called
    Then the result is "'/home/user/my proj'"

  @unit
  Scenario: Shell-quote escapes embedded single quotes
    When shell_quote("it's") is called
    Then the result is "'it'\\''s'"

  @unit
  Scenario: Startup restore runs at most once per process
    Given restore_all_local() has already run in this process
    When restore_all_local() is called again
    Then an empty RestoreReport is returned
    And no tmux subprocess is spawned

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
    And claude_session_ids is an empty HashMap

  @unit
  Scenario: populate_claude_session_ids fills only Claude panes
    Given a session with three panes: one bash, one claude command, one claude-titled
    And a matching ClaudeStateFile with session_id="sess-xyz"
    When populate_claude_session_ids is called
    Then the map contains exactly the two Claude panes
    And the bash pane is absent

  @unit
  Scenario: Startup manifest persists claude_session_id as a durable backup
    Given a task row with one Claude pane carrying claude_session_id=Some("abc")
    When the TUI writes the session manifest
    Then the SessionManifestEntry.claude_session_id is Some("abc")

  @unit
  Scenario: JSON output exposes restore-relevant fields
    Given a PaneState with cwd, is_active, claude_session_id
    And a WindowState with a layout string
    When --json is emitted
    Then the JSON pane object contains "cwd", "isActive"
    And when claude_session_id is Some, the JSON contains "claudeSessionId"
    And when claude_session_id is None, the JSON omits "claudeSessionId"
    And the JSON window object contains "layout"

  Out of scope:
    - Remote session restore (tracked separately)
    - Shell history replay
    - An explicit `orchard restore` CLI subcommand (restore is automatic)
    - TUI "restored" badge that fades after first refresh (deferred to follow-up)
