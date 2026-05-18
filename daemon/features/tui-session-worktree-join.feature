Feature: TUI session-to-worktree join via path
  As the orchard TUI
  I need to associate each tmux session with its worktree
  So that the dashboard can render sessions inline under their worktree row.

  Background:
    Given the daemon delivers workView with repos, tmuxSessions, and claudeInstances
    And the TUI performs client-side session→worktree matching

  # Match logic: WorkViewTmuxSession.path is used with paths::session_belongs_to_worktree.
  # When path is absent, the session falls back to standalone.

  Scenario: Session with matching path is joined to worktree
    Given a tmux session has path "/repos/owner/repo/.worktrees/issue429"
    And there is a worktree at path "/repos/owner/repo/.worktrees/issue429"
    When the TUI adapter builds the local state
    Then the session is attached to that worktree's sessions list
    And the session does not appear in the standalone_sessions list

  Scenario: Session with non-matching path becomes standalone
    Given a tmux session has path "/home/user/other"
    And no worktree path contains that path
    When the TUI adapter builds the local state
    Then the session appears in standalone_sessions
    And it is not attached to any worktree

  Scenario: Session with absent path (null) becomes standalone
    Given a tmux session has no path field (null or absent)
    When the TUI adapter builds the local state
    Then the session is treated as standalone
    And no crash occurs from a missing path

  Scenario: Session window metadata is forwarded
    Given a tmux session has windows: 3 and currentWindow: "editor"
    When the TUI adapter converts the session
    Then the window count is available for the TUI's expand/collapse UI
    And the current window name is visible in the session row

  Scenario: Configured standalone sessions are emitted first in config order
    Given the global config defines tmux_sessions: ["shepherd", "monitor"]
    And "shepherd" is a live session in the workView snapshot
    And "monitor" is not in the snapshot (dead session)
    When the TUI builds standalone sessions
    Then "shepherd" appears first with status Running
    And "monitor" appears second with status Dead

  Scenario: Discovered sessions not in config appear after configured sessions
    Given a live session "ad-hoc" is in the snapshot but not in global config
    And its path does not match any worktree
    When the TUI builds standalone sessions
    Then "ad-hoc" appears after the configured sessions
