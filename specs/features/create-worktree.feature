Feature: Create worktree from TUI
  As an orchard user
  I want to create new worktrees directly from the TUI with a keybinding
  So that I can start working on a new branch without leaving the dashboard

  # Issue: #57
  #
  # Current state:
  #   - 'n' key opens a NewSession text-entry dialog (creates a bare tmux session)
  #   - Enter on a worktree row creates a tmux session in that worktree's directory
  #   - OrchardConfig (per-repo) has only a `remote` field
  #   - .orchard.json (committable) + .git/orchard.json (local) two-layer config
  #   - Worktree path convention: <repo-parent>/worktrees/worktree-<slug>
  #     (uses derive_local_worktree_path from transfer.rs)
  #   - NewSessionState pattern: text input dialog with Enter/Esc/Backspace handling
  #
  # Design decisions:
  #   - `setup_script` goes in .orchard.json (committable) because it's team-shared
  #   - The keybinding is 'w' (mnemonic: worktree), distinct from 'n' (new session)
  #   - Branch name input reuses the NewSession text-entry dialog pattern
  #   - Worktree path uses derive_local_worktree_path (existing convention)
  #   - setup_script path resolved relative to repo root, executed with cwd = new worktree
  #   - Uses `git worktree add -b <branch> <path>` for new branches,
  #     falls back to `git worktree add <path> <branch>` if branch already exists
  #   - Setup script failure still creates tmux session (worktree is usable)
  #     but shows a warning with the script error

  Background:
    Given a git repository named "myrepo" at "/home/user/myrepo"
    And the orchard TUI is running inside tmux

  # ===================================================================
  # Config: setup_script field in .orchard.json
  # ===================================================================

  @unit
  Scenario: OrchardConfig deserializes setup_script from .orchard.json
    Given an .orchard.json with:
      """json
      {
        "setup_script": "./scripts/setup-worktree.sh"
      }
      """
    When the config is loaded
    Then setup_script is Some("./scripts/setup-worktree.sh")

  @unit
  Scenario: OrchardConfig defaults setup_script to None when omitted
    Given an .orchard.json with:
      """json
      {}
      """
    When the config is loaded
    Then setup_script is None

  @unit
  Scenario: setup_script round-trips through serialize and deserialize
    Given an OrchardConfig with setup_script "./scripts/setup.sh"
    When the config is serialized and deserialized
    Then setup_script is Some("./scripts/setup.sh")

  @unit
  Scenario: setup_script from .git/orchard.json overrides .orchard.json
    Given an .orchard.json with setup_script "./team-setup.sh"
    And a .git/orchard.json with setup_script "./my-setup.sh"
    When the merged config is loaded
    Then setup_script is Some("./my-setup.sh")

  # ===================================================================
  # Keybinding: 'w' opens the create-worktree dialog
  # ===================================================================

  @e2e
  Scenario: Pressing 'w' opens the branch name input dialog
    Given the TUI is in List view
    When I press "w"
    Then a text-entry dialog appears with title "New Worktree"
    And the dialog prompts "Branch name:"
    And the dialog shows hint text "enter confirm  esc cancel"

  @unit
  Scenario: 'w' key is shown in the help overlay
    When I press "?"
    Then the help overlay includes "w" mapped to "new worktree"

  # ===================================================================
  # Branch name input dialog
  # ===================================================================

  @unit
  Scenario: Branch name accepts alphanumeric, hyphens, underscores, and slashes
    Given the create-worktree dialog is open
    When I type "feature/my-branch_123"
    Then the input shows "feature/my-branch_123"

  @unit
  Scenario: Branch name rejects invalid characters
    Given the create-worktree dialog is open
    When I type "feature branch!"
    Then the input shows "featurebranch"
    And spaces and special characters are silently dropped

  @unit
  Scenario: Escape cancels the dialog and returns to list view
    Given the create-worktree dialog is open
    And I have typed "feature/x"
    When I press Escape
    Then the dialog closes
    And the view returns to List
    And no worktree is created

  @unit
  Scenario: Enter on empty input does nothing
    Given the create-worktree dialog is open
    And the input is empty
    When I press Enter
    Then nothing happens
    And the dialog remains open

  @unit
  Scenario: Backspace removes the last character
    Given the create-worktree dialog is open
    And I have typed "feature/xy"
    When I press Backspace
    Then the input shows "feature/x"

  # ===================================================================
  # Happy path: worktree creation
  # ===================================================================

  @e2e
  Scenario: Enter on valid branch name creates worktree, session, and switches
    Given the create-worktree dialog is open
    And I have typed "feature/login"
    When I press Enter
    Then git worktree add is run with branch "feature/login" in "<repo-parent>/worktrees/worktree-feature-login"
    And a tmux session named "myrepo_feature-login" is created in the worktree directory
    And the popup closes
    And my tmux client switches to session "myrepo_feature-login"

  @e2e
  Scenario: Worktree creation with setup_script runs the script after checkout
    Given an .orchard.json with setup_script "./scripts/setup-worktree.sh"
    And the create-worktree dialog is open
    And I have typed "feature/api"
    When I press Enter
    Then git worktree add is run for branch "feature/api"
    And "./scripts/setup-worktree.sh" is resolved relative to the repo root
    And the script is executed with cwd set to the new worktree directory
    And a tmux session is created in the worktree directory
    And my tmux client switches to the new session

  # ===================================================================
  # Worktree path derivation (reuses derive_local_worktree_path)
  # ===================================================================

  @unit
  Scenario: Worktree path follows existing convention
    Given branch name "feature/my-work" and repo root "/home/user/myrepo"
    When the worktree path is derived using derive_local_worktree_path
    Then the directory is "/home/user/worktrees/worktree-feature-my-work"

  @unit
  Scenario: Simple branch name uses the name directly
    Given branch name "hotfix-123" and repo root "/home/user/myrepo"
    When the worktree path is derived
    Then the directory is "/home/user/worktrees/worktree-hotfix-123"

  # ===================================================================
  # Session name derivation (reuses existing logic)
  # ===================================================================

  @unit
  Scenario: Session name follows existing repo_branch convention
    Given repository named "myrepo"
    And branch name "feature/login"
    When the session name is derived for the new worktree
    Then the session name is "myrepo_feature-login"

  # ===================================================================
  # Setup script execution
  # ===================================================================

  @integration
  Scenario: Setup script runs with worktree directory as cwd
    Given an .orchard.json with setup_script "./scripts/setup.sh"
    And the repo root is "/home/user/myrepo"
    And the worktree was created at "/home/user/worktrees/worktree-feature-api"
    When the setup script runs
    Then the script path is resolved to "/home/user/myrepo/scripts/setup.sh"
    And the working directory is "/home/user/worktrees/worktree-feature-api"

  @integration
  Scenario: No setup_script configured skips the script step
    Given an .orchard.json without setup_script
    When a worktree is created for "feature/api"
    Then git worktree add succeeds
    And no setup script is executed
    And session creation proceeds normally

  @integration
  Scenario: Setup script that does not exist shows warning but creates session
    Given an .orchard.json with setup_script "./nonexistent.sh"
    When a worktree is created and the setup script is attempted
    Then a warning is shown in the TUI: "setup script not found: ./nonexistent.sh"
    And the worktree still exists
    And a tmux session is still created for the worktree
    And the user switches to the new session

  @integration
  Scenario: Setup script that exits non-zero shows warning but creates session
    Given an .orchard.json with setup_script "./scripts/setup.sh"
    And the script exits with code 1 and stderr "npm install failed"
    When a worktree is created and the setup script runs
    Then a warning is shown: "setup script failed (exit 1): npm install failed"
    And the worktree still exists
    And a tmux session is still created for the worktree
    And the user switches to the new session

  # ===================================================================
  # Error handling: git worktree add failures
  # ===================================================================

  @integration
  Scenario: Branch already has a worktree shows error
    Given a worktree already exists for branch "feature/login"
    And the create-worktree dialog is open with "feature/login"
    When I press Enter
    Then an error is shown: "worktree already exists for branch 'feature/login'"
    And the dialog closes
    And the user returns to the list view

  @integration
  Scenario: Git worktree add fails shows raw git error
    Given git worktree add fails with an unexpected error
    When the error is displayed
    Then the raw git stderr output is shown in the TUI warning
    And the dialog closes

  # ===================================================================
  # Branch exists behavior
  # ===================================================================

  @integration
  Scenario: Creating worktree for branch that already exists checks it out
    Given a git branch "feature/existing" already exists locally
    And the create-worktree dialog is open with "feature/existing"
    When I press Enter
    Then git worktree add creates the worktree checking out the existing branch
    And a tmux session is created and switched to

  # ===================================================================
  # Error handling: tmux session creation failure
  # ===================================================================

  @integration
  Scenario: Tmux session creation fails after worktree is created
    Given git worktree add succeeded for "feature/x"
    But tmux new-session fails
    When the error is displayed
    Then the error is shown in the TUI warning area
    And the worktree still exists on disk
    And the user remains in the TUI

  # ===================================================================
  # Non-tmux environment
  # ===================================================================

  @integration
  Scenario: 'w' creates worktree but cannot switch session outside tmux
    Given the TUI is running outside tmux
    And the create-worktree dialog is open with "feature/x"
    When I press Enter
    Then git worktree add succeeds
    And the setup script runs if configured
    But no tmux session is created
    And a hint shows "run inside tmux for session switching"
    And the TUI list refreshes to show the new worktree
