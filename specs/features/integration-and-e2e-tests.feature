Feature: Integration and E2E test implementation
  As a developer
  I want automated integration and e2e tests that exercise real system interactions
  So that bugs like the Enter key issue are caught before delivery

  Background:
    Given the orchard binary is built at target/debug/orchard
    And tmux is available on the system

  # ===================================================================
  # Prerequisite: lib.rs extraction
  # ===================================================================
  #
  # The crate must be split into lib + binary so that tests/ can access
  # public functions. src/lib.rs re-exports the public API; src/main.rs
  # becomes a thin entry point calling into the library.
  #
  # Modules exposed via lib.rs: tmux, collector, shell, git, config, types

  # ===================================================================
  # Phase 1: Test Harness (tests/common/mod.rs)
  # ===================================================================

  @integration
  Scenario: Git fixture creates a disposable repo with worktrees
    When a GitFixture is created
    Then a temporary directory contains an initialized git repo with branch "main"
    And the repo has at least one commit
    And git worktree list returns the main worktree

  @integration
  Scenario: Git fixture can add worktrees
    Given a GitFixture exists
    When a worktree is added for branch "feature/test-branch"
    Then git worktree list shows both the main and feature worktree

  @integration
  Scenario: Tmux fixture creates and cleans up sessions
    When a TmuxFixture is created with prefix "orchard_test_"
    Then any pre-existing sessions matching "orchard_test_*" are killed first
    When a session "orchard_test_abc" is created
    Then tmux has-session reports the session exists
    When the TmuxFixture is dropped
    Then all sessions with prefix "orchard_test_" are killed

  @e2e
  Scenario: Binary runner captures stdout and stderr
    When the orchard binary is run with "--help"
    Then stderr contains "Usage:"
    And the exit code is 0

  # ===================================================================
  # Phase 2: Integration Tests — Tmux Session Management
  # Traces to: tmux-session-management.feature @integration
  # ===================================================================

  @integration
  Scenario: Session creation at worktree directory via real tmux
    Given a GitFixture with a worktree on branch "feature/login"
    When create_session is called for the worktree
    Then a tmux session exists with name "{sanitized_repo}_feature-login"
    And the session's working directory matches the worktree path

  @integration
  Scenario: Existing session is reused on repeat create_session call
    Given a tmux session was created for branch "feature/login"
    When create_session is called again for the same worktree
    Then tmux still has exactly one session for that worktree
    And no error is raised

  @integration
  Scenario: list_tmux_sessions returns real tmux sessions
    Given tmux sessions "orchard_test_a" and "orchard_test_b" exist
    When list_tmux_sessions is called
    Then the result includes both "orchard_test_a" and "orchard_test_b"
    And each session has a name, path, and attached flag

  @integration
  Scenario: find_session_for_worktree matches by path against real sessions
    Given a tmux session exists at path "/tmp/orchard_test_repo"
    When find_session_for_worktree is called with that path
    Then the matching session is returned

  # ===================================================================
  # Phase 2: Integration Tests — Main Session at Worktree Origin
  # Traces to: main-session-at-worktree-origin.feature @integration
  # ===================================================================

  @integration
  Scenario: ensure_main_session creates session at worktree origin
    Given a GitFixture with main worktree on branch "main"
    And no tmux session named "{repo}_main" exists
    When ensure_main_session is called with the worktree list
    Then a tmux session named "{repo}_main" exists
    And its working directory is the repo root

  @integration
  Scenario: ensure_main_session is idempotent
    Given a tmux session named "{repo}_main" already exists
    When ensure_main_session is called again with the same worktree list
    Then still only one session named "{repo}_main" exists

  # ===================================================================
  # Phase 2: Integration Tests — Wrapper Script Filesystem
  # Traces to: popup-mode.feature @integration
  # ===================================================================

  @integration
  Scenario: Wrapper script is created with correct permissions
    Given a temporary home directory
    When the wrapper script is written to {home}/.local/bin/orchard-popup
    Then the file exists and is executable (mode includes 0o111)
    And the file content contains "tmux switch-client"

  # ===================================================================
  # Phase 2: Integration Tests — Key Event Dispatch
  # Traces to: popup-mode.feature @e2e (Enter key scenarios)
  # This is the critical gap — the Enter key bug class.
  # ===================================================================

  @unit
  Scenario: Enter key on worktree with existing session returns session name
    Given an AppState with a selected worktree that has tmux_session "myrepo_main"
    When an Enter key event is dispatched
    Then the action is SwitchToSession("myrepo_main")

  @unit
  Scenario: Enter key on worktree without session returns CreateAndSwitch
    Given an AppState with a selected worktree that has no tmux_session
    When an Enter key event is dispatched
    Then the action is CreateAndSwitch with the worktree path and derived session name

  @unit
  Scenario: Q key returns Quit action
    Given an AppState with any selected worktree
    When a Q key event is dispatched
    Then the action is Quit

  @unit
  Scenario: Escape key returns Quit action
    Given an AppState with any selected worktree
    When an Escape key event is dispatched
    Then the action is Quit

  # ===================================================================
  # Phase 3: E2E Tests — Binary Invocation
  # Traces to: popup-mode.feature @e2e,
  #            main-session-at-worktree-origin.feature @e2e
  # ===================================================================

  @e2e
  Scenario: orchard --json outputs valid JSON worktree array
    Given a GitFixture repo with at least one worktree
    When I run "orchard --json" in the repo directory
    Then the exit code is 0
    And stdout is valid JSON
    And the JSON is an array of worktree objects
    And each object has "path" and "isBare" fields

  @e2e
  Scenario: orchard --json includes branch information
    Given a GitFixture repo with main worktree on branch "main"
    When I run "orchard --json" in the repo directory
    Then at least one worktree in the output has branch "main"

  @e2e
  Scenario: orchard --help exits successfully
    When I run "orchard --help"
    Then the exit code is 0
    And stderr contains usage instructions

  @e2e
  Scenario: orchard --json does not create main tmux session
    Given a GitFixture repo with no associated tmux sessions
    When I run "orchard --json" in the repo directory
    Then no tmux session matching the repo name is created

  # ===================================================================
  # Implementation constraints
  # ===================================================================

  # Structural:
  # - Add src/lib.rs that re-exports pub modules (tmux, collector, shell,
  #   git, config, types) so tests/ can access them
  # - src/main.rs becomes thin: calls orchard::tui::run(), etc.
  #
  # Dependencies:
  # - Add assert_cmd to [dev-dependencies] for binary invocation tests
  #
  # Tmux safety:
  # - All test tmux sessions use "orchard_test_{uuid}" prefix
  # - TmuxFixture::new() kills pre-existing sessions with its prefix (idempotent startup)
  # - TmuxFixture::drop() kills all sessions with its prefix (cleanup)
  # - Tests that require tmux: skip gracefully when tmux is unavailable
  #   (check at runtime, not via #[ignore]) so CI visibility is maintained
  #
  # Git fixture safety:
  # - GitFixture uses tempfile::TempDir for auto-cleanup
  # - Always init with -b main to avoid init.defaultBranch variance
  # - Binary invocation tests set current_dir to fixture, unset TMUX env var
  #
  # Test naming:
  # - Test function names map to scenario names for traceability
  # - Example: "Session creation at worktree directory via real tmux"
  #   → fn session_creation_at_worktree_directory_via_real_tmux()
