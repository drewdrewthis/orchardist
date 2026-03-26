Feature: Unified service architecture
  As an orchard user and script consumer
  I want a single OrchardState data model powering both the TUI and --json
  So that both outputs are complete, consistent, and the codebase has one code path instead of two

  Background:
    Given a global config with at least one repo configured
    And per-source cache files may or may not exist on disk

  # =======================================================================
  # Step 1: --json uses refresh_and_build -> JsonOutput
  # =======================================================================

  @integration
  Scenario: --json produces output from refresh_and_build pipeline
    Given cache files exist for repo "hopegrace/git-orchard-rs"
    When the user runs "orchard --json"
    Then the output is valid JSON matching the JsonOutput v2 schema
    And the output contains a "version" field equal to 2
    And the output contains a "repos" array with one entry per configured repo
    And each repo entry contains a "worktrees" array

  @integration
  Scenario: --json fetches fresh data before producing output
    Given stale cache files exist for repo "hopegrace/git-orchard-rs"
    When the user runs "orchard --json"
    Then all sources are refreshed before output is produced
    And the JSON output reflects the freshly fetched data, not stale cache

  @unit
  Scenario: --json does not use the legacy collector
    When the handle_json function is invoked
    Then it calls build_state::refresh_and_build
    And it does not call collector::collect_worktree_data

  @unit
  Scenario: JsonOutput includes issue, PR, session, and host data
    Given an OrchardState with a worktree that has an issue, PR, session, and host
    When JsonOutput is derived from the OrchardState
    Then the JSON worktree entry includes "issue" with number, title, and state
    And the JSON worktree entry includes "pr" with number, branch, reviewDecision, checksState, hasConflicts, unresolvedThreads
    And the JSON worktree entry includes "sessions" array with claudeState, contextWindowPct, costUsd
    And the JSON top-level includes "hosts" map with reachability

  @unit
  Scenario: JsonOutput uses camelCase field names
    Given any OrchardState
    When serialized to JSON via JsonOutput
    Then all field names use camelCase (e.g. "displayGroup", "checksState", "costUsd")

  # =======================================================================
  # Step 2: TUI consumes OrchardState exclusively
  # =======================================================================

  @unit
  Scenario: App stores OrchardState instead of dual data structures
    When the App struct is constructed
    Then it contains an OrchardState field
    And it does not contain a Vec<Worktree> field for the legacy collector
    And it does not contain a separate Vec<TaskRow> field

  @unit
  Scenario: App initializes from build_state on startup
    Given cache files exist on disk
    When App::new is called
    Then build_state() is called to produce the initial OrchardState
    And the TUI renders immediately from that state without waiting for network

  @unit
  Scenario: TUI renders worktrees from OrchardState.all_worktrees
    Given an OrchardState with worktrees across two repos
    When the TUI list view renders
    Then it calls OrchardState::all_worktrees() to get a sorted flat list
    And worktrees are sorted by display_group then by issue number

  # =======================================================================
  # Step 3: TUI rendering uses WorktreeState
  # =======================================================================

  @unit
  Scenario: visible_tasks filters and returns WorktreeState references
    Given an OrchardState with 10 worktrees in various display groups
    When visible_tasks is computed with filter_mode All and backlog collapsed
    Then it returns WorktreeState references from OrchardState
    And Other-group worktrees are replaced by a backlog summary line

  @unit
  Scenario: Task list renders issue number, title, and status from WorktreeState
    Given a WorktreeState with issue number 47, title "Add auth", and PR with checks "passing"
    When the task list row is rendered
    Then the row displays "#47", "Add auth", and the checks status badge

  @unit
  Scenario: Session info renders from WorktreeState.sessions
    Given a WorktreeState with two sessions, one with claude_is_working true
    When the task list row is rendered
    Then the Claude column shows the working badge
    And the session count is visible

  @unit
  Scenario: Pane content preview works with WorktreeState sessions
    Given a WorktreeState with a session named "orchard_47"
    When the user selects that row
    Then the pane preview fetches content for session "orchard_47"
    And the preview renders in the right panel

  @unit
  Scenario: Enter key joins or creates tmux session from WorktreeState
    Given a WorktreeState with a session at path "/workspace/orchard-47"
    When the user presses Enter on that row
    Then the TUI exits with the session name as the switch target

  @unit
  Scenario: Enter key on a WorktreeState with no session creates one
    Given a WorktreeState with no sessions and path "/workspace/orchard-47"
    When the user presses Enter on that row
    Then a new tmux session is created at that worktree path
    And the TUI exits with the new session name as the switch target

  # =======================================================================
  # Step 4: Two-phase refresh in TUI
  # =======================================================================

  @integration
  Scenario: Phase 1 refreshes fast local sources
    When start_refresh is called
    Then Phase 1 runs first, refreshing:
      | source              |
      | local worktrees     |
      | tmux sessions       |
      | claude state files  |
    And Phase 1 completes in under 1 second for typical workloads
    And build_state() is called after Phase 1 completes
    And the TUI re-renders with the Phase 1 data

  @integration
  Scenario: Phase 2 refreshes slow remote and network sources
    When Phase 1 completes
    Then Phase 2 begins, refreshing:
      | source              |
      | GitHub issues       |
      | GitHub PRs          |
      | remote worktrees    |
      | host probes         |
      | remote tmux         |
    And build_state() is called after Phase 2 completes
    And the TUI re-renders with the Phase 2 data

  @unit
  Scenario: TUI renders exactly twice per refresh cycle
    Given a refresh cycle is triggered
    When both Phase 1 and Phase 2 complete
    Then the TUI re-renders exactly twice: once after Phase 1 and once after Phase 2
    And no intermediate staggered re-renders occur between individual source completions

  @integration
  Scenario: Remote worktrees dim when host is unreachable
    Given a remote worktree on host "ubuntu@10.0.0.1"
    And the host probe for "ubuntu@10.0.0.1" returns unreachable
    When build_state_with_hosts is called with that host marked unreachable
    Then the worktree is still present in OrchardState (not removed)
    And hosts["ubuntu@10.0.0.1"].reachable is false
    And the TUI renders the worktree row with dimmed styling

  @unit
  Scenario: Previous state is retained for sources that have not updated
    Given the TUI has rendered from Phase 1 data
    And Phase 2 has not completed yet
    When the TUI renders
    Then GitHub-enriched data (issues, PRs) from the initial cache is still displayed
    And local data reflects Phase 1 updates

  @integration
  Scenario: On startup, build_state reads existing caches then kicks off both phases
    Given stale cache files exist on disk
    When the TUI starts
    Then build_state() is called immediately from cached data (no network)
    And the TUI renders instantly
    And start_refresh() is called to begin Phase 1 then Phase 2

  # =======================================================================
  # Step 5: Remove legacy collector
  # =======================================================================

  @unit
  Scenario: collector module is removed from the codebase
    Then no module named "collector" exists in src/
    And lib.rs does not declare "pub mod collector"

  @unit
  Scenario: types::Worktree is no longer used in the TUI
    Then the tui/ module files do not import types::Worktree
    And no function in tui/ accepts or returns types::Worktree

  @unit
  Scenario: Legacy AppMsg::Worktrees variant is removed
    Then the AppMsg enum does not contain a Worktrees variant carrying Vec<Worktree>
    And the check_updates handler does not process legacy worktree messages

  @unit
  Scenario: derive_from_all_caches is replaced by build_state
    Then no function named "derive_from_all_caches" exists in the codebase
    And cache refresh completion triggers build_state() to produce OrchardState

  @unit
  Scenario: Legacy refresh thread for collector is removed
    Then start_refresh does not spawn a thread calling collector::refresh_worktrees
    And start_refresh only spawns threads for Phase 1 and Phase 2 cache refreshes

  # =======================================================================
  # Step 6: Clean build with no dead code
  # =======================================================================

  @e2e
  Scenario: Project compiles with no warnings
    When "cargo build --release" is run
    Then the build succeeds with exit code 0
    And there are no unused import warnings
    And there are no dead code warnings

  @e2e
  Scenario: All tests pass
    When "cargo test" is run
    Then all tests pass with exit code 0

  # =======================================================================
  # Notification transitions still work with OrchardState
  # =======================================================================

  @unit
  Scenario: Claude needs-input notification fires on state transition
    Given the previous OrchardState had a worktree where claude_needs_input was false
    And the new OrchardState has that worktree where claude_needs_input is true
    When state transitions are checked after refresh
    Then a desktop notification fires with title "Claude needs input"

  @unit
  Scenario: CI failure notification fires on state transition
    Given the previous OrchardState had a worktree with checks_state "passing"
    And the new OrchardState has that worktree with checks_state "failing"
    When state transitions are checked after refresh
    Then a desktop notification fires with title "CI Failed"

  @unit
  Scenario: Session manifest is written from OrchardState after refresh
    Given the OrchardState contains worktrees with active sessions
    When a cache refresh completes and build_state produces the new OrchardState
    Then the session manifest is written with entries for each worktree that has sessions
    And each manifest entry includes session_name, worktree_path, branch, had_claude, and host

  # =======================================================================
  # Existing TUI features preserved
  # =======================================================================

  @unit
  Scenario: Backlog collapse works with OrchardState
    Given an OrchardState where 12 worktrees have display_group Other
    When the task list renders with backlog collapsed (default)
    Then Other-group worktrees are replaced with a summary line "12 backlog items -- press b to expand"

  @unit
  Scenario: Filter modes work with OrchardState
    Given an OrchardState with worktrees that have sessions, PRs, and Claude activity
    When filter_mode is set to "Has Session"
    Then only worktrees with non-empty sessions are visible
    When filter_mode is set to "Has Claude"
    Then only worktrees where a session has claude_is_working or claude_needs_input are visible
    When filter_mode is set to "Has PR"
    Then only worktrees with a non-None pr field are visible

  @unit
  Scenario: Shepherd rows are always visible regardless of filter
    Given an OrchardState with a worktree marked is_shepherd true
    And filter_mode is set to "Has PR"
    And the shepherd worktree has no PR
    When the visible worktrees are computed
    Then the shepherd worktree is included in the results

  @unit
  Scenario: Cleanup view works with OrchardState
    Given an OrchardState with worktrees where some have merged PRs
    When the cleanup view is activated
    Then stale worktrees are identified from the OrchardState
    And the user can select and delete them

  @unit
  Scenario: Delete and transfer dialogs work with WorktreeState
    Given a WorktreeState selected in the task list
    When the user presses 'd' to delete
    Then the delete confirmation dialog shows the worktree path and branch from WorktreeState
    When the user presses 'T' to transfer
    Then the transfer dialog uses the worktree path from WorktreeState
