Feature: TUI local mutations (no GraphQL mutations used today)
  As the orchard TUI
  I need to create and delete worktrees and tmux sessions in response to keystrokes
  So that operators can manage their workspace without leaving the dashboard.

  Background:
    Given the TUI is in List or dialog view
    And the TUI is running inside a tmux session

  # NOTE: The TUI issues NO GraphQL mutations against the daemon today.
  # All mutations are performed directly via:
  #   - crate::tmux::create_session         ('n' new session, 'w' new worktree confirm)
  #   - worktree_core::create_worktree       ('w' new worktree confirm)
  #   - worktree_core::remove_worktree       ('d' delete confirm, 'c' cleanup confirm)
  #   - crate::tmux::kill_tmux_session_safe  (delete/cleanup)
  #   - crate::remote::*                     (remote delete)
  # These are L7 violations relative to ADR-016 / ADR-017 and must be tracked
  # as future daemon mutation migrations.

  Scenario: 'n' opens new-session dialog; Enter creates a tmux session directly
    When the operator presses 'n' in the list view
    Then the TUI shows the new-session name-entry dialog
    When the operator types a session name and presses Enter
    Then the TUI calls crate::tmux::create_session with the typed name
    And on success the TUI exits to switch to the new session
    And NO GraphQL mutation is issued to the daemon

  Scenario: 'w' opens new-worktree dialog; Enter creates a worktree directly
    When the operator presses 'w' in the list view
    Then the TUI shows the new-worktree branch-entry dialog
    When the operator types a branch name and presses Enter
    Then the TUI calls worktree_core::create_worktree to create the git worktree
    And it calls crate::tmux::create_session to create the associated tmux session
    And on success it fires a full refresh so the daemon-supplied workView reflects the new worktree
    And NO GraphQL mutation is issued to the daemon

  Scenario: Setup script is run after worktree creation
    Given the project config has a setup_script defined
    When a new worktree is created
    Then the setup script is run in the new worktree directory
    And if the script exits non-zero, a non-fatal warning is surfaced
    And the worktree is still created even if the script fails

  Scenario: 'd' deletes a local worktree directly
    Given the cursor is on a local worktree row with an associated tmux session
    When the operator presses 'd' and confirms with 'y'
    Then the TUI calls tmux::kill_tmux_session_safe to kill the session
    And it calls worktree_core::remove_worktree to remove the worktree
    And on success it fires a full refresh
    And NO GraphQL mutation is issued to the daemon

  Scenario: 'c' cleanup deletes multiple stale worktrees
    When the operator presses 'c' and confirms the selection
    Then the TUI calls delete_task_row for each selected stale worktree
    And for each worktree it kills the tmux session and removes the git worktree
    And on completion it fires a full refresh
    And NO GraphQL mutations are issued

  Scenario: After any local mutation the next workView query reflects the change
    Given a worktree was created or deleted
    When the TUI fires its next workView query after the mutation
    Then the daemon's workView response reflects the updated worktree list
    And the dashboard shows the post-mutation state
