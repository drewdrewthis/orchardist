Feature: Shepherd persistent global session
  As an orchard user managing multiple repos
  I want standalone tmux sessions (e.g. a shepherd) that aren't tied to any worktree
  So that I can run cross-repo orchestration agents alongside my worktree sessions

  # Issue: #47
  # Two-part implementation:
  #   Part 1 — Session data model refactor (no new features, better types)
  #   Part 2 — Standalone tmux sessions (the actual shepherd feature)
  #
  # Current state (what exists today):
  #   - SessionInfo struct: flat fields (name, host, has_claude_active, claude_is_working, etc.)
  #   - WorktreeRow: sessions: Vec<SessionInfo>, is_shepherd: bool
  #   - DisplayGroup::Shepherd — refers to the repo's main worktree, not a global shepherd
  #   - TmuxSession in types.rs: name, path, attached, pane_title
  #   - GlobalConfig: repos + terminal_app (no tmux_sessions field)
  #   - JSON output v2: flat session fields within worktrees
  #   - No concept of standalone sessions or ListEntry enum

  # =========================================================================
  # PART 1: Session Data Model Refactor
  # =========================================================================

  # -------------------------------------------------------------------
  # Host enum
  # -------------------------------------------------------------------

  @unit
  Scenario: Host enum represents local and remote variants
    Given the Host enum is defined
    Then it has a Local variant with no fields
    And it has a Remote variant containing an SshTarget

  # -------------------------------------------------------------------
  # TmuxSession struct — pure tmux, no enrichment
  # -------------------------------------------------------------------

  @unit
  Scenario: TmuxSession contains only tmux-native fields
    Given a TmuxSession struct
    Then it has fields: host (Host), name (String), status (SessionStatus), created (Option<DateTime>), last_activity (Option<DateTime>)
    And it does not contain any Claude-related fields

  @unit
  Scenario: SessionStatus represents running and dead states
    Given the SessionStatus enum
    Then Running carries an attached boolean
    And Dead has no additional data

  # -------------------------------------------------------------------
  # ClaudeSessionInfo — enrichment grouped together
  # -------------------------------------------------------------------

  @unit
  Scenario: ClaudeSessionInfo groups status and cost
    Given a ClaudeSessionInfo struct
    Then it has a status field (ClaudeState)
    And it has a cost field (Option<Cost>)
    And it does not contain tmux session fields

  # -------------------------------------------------------------------
  # EnrichedSession — tmux + optional enrichment
  # -------------------------------------------------------------------

  @unit
  Scenario: EnrichedSession composes TmuxSession with optional Claude info
    Given an EnrichedSession struct
    Then it has a tmux field of type TmuxSession
    And it has a claude field of type Option<ClaudeSessionInfo>

  # -------------------------------------------------------------------
  # WorktreeRow refactored to use Vec<EnrichedSession>
  # -------------------------------------------------------------------

  @unit
  Scenario: WorktreeRow uses sessions as Vec of EnrichedSession
    Given a WorktreeRow with two sessions
    When the sessions field is accessed
    Then it returns a Vec<EnrichedSession>
    And each element contains a TmuxSession and optional ClaudeSessionInfo

  @unit
  Scenario: WorktreeRow no longer has flat session fields
    Given the refactored WorktreeRow struct
    Then there is no session_name field
    And there is no claude_status field
    And there is no has_claude_active field

  # -------------------------------------------------------------------
  # StandaloneSessionRow and StandaloneConfig defined (not yet constructed)
  # -------------------------------------------------------------------

  @unit
  Scenario: StandaloneSessionRow pairs an EnrichedSession with config
    Given a StandaloneSessionRow struct
    Then it has a session field of type EnrichedSession
    And it has a config field of type StandaloneConfig

  @unit
  Scenario: StandaloneConfig holds command, cwd, and start_on_launch
    Given a StandaloneConfig struct
    Then it has a command field (String)
    And it has a cwd field (PathBuf)
    And it has a start_on_launch field (bool)

  # -------------------------------------------------------------------
  # ListEntry enum defined with both variants
  # -------------------------------------------------------------------

  @unit
  Scenario: ListEntry enum has Worktree and Standalone variants
    Given the ListEntry enum
    Then it has a Worktree variant containing WorktreeRow
    And it has a Standalone variant containing StandaloneSessionRow

  @unit
  Scenario: ListEntry::Standalone is defined but never constructed in Part 1
    Given the codebase after Part 1
    Then no code constructs a ListEntry::Standalone value
    And the Standalone variant exists for forward compatibility

  # -------------------------------------------------------------------
  # Rename: is_shepherd -> is_main_worktree, Shepherd -> RepoMain
  # -------------------------------------------------------------------

  @unit
  Scenario: is_shepherd renamed to is_main_worktree
    Given the refactored WorktreeRow
    Then it has an is_main_worktree field
    And there is no is_shepherd field anywhere in the codebase

  @unit
  Scenario: DisplayGroup::Shepherd renamed to DisplayGroup::RepoMain
    Given the refactored DisplayGroup enum
    Then it has a RepoMain variant
    And there is no Shepherd variant
    And RepoMain still sorts first among display groups

  # -------------------------------------------------------------------
  # TUI display aggregates across Vec<EnrichedSession>
  # -------------------------------------------------------------------

  @unit
  Scenario: Claude column shows active when any session has active Claude
    Given a WorktreeRow with two EnrichedSessions
    And the first session has claude status None
    And the second session has claude status Working
    When the Claude column is rendered
    Then it shows the working state

  @unit
  Scenario: Claude column shows idle when all sessions are idle
    Given a WorktreeRow with two EnrichedSessions
    And both sessions have claude status Idle
    When the Claude column is rendered
    Then it shows the idle state

  @unit
  Scenario: Claude column is empty when no sessions exist
    Given a WorktreeRow with zero sessions
    When the Claude column is rendered
    Then it shows no Claude indicator

  # -------------------------------------------------------------------
  # Shared EnrichedSession display functions
  # -------------------------------------------------------------------

  @unit
  Scenario: EnrichedSession display functions are reusable across render paths
    Given an EnrichedSession with Claude status Working and cost 1.23
    When the shared status display function is called
    Then it returns the same formatted output regardless of calling context

  # -------------------------------------------------------------------
  # JSON v3: worktree sessions use EnrichedSession shape
  # -------------------------------------------------------------------

  @unit
  Scenario: JSON output version bumps to 3
    Given an OrchardState with worktree data
    When converted to JSON output
    Then the version field is 3

  @unit
  Scenario: JSON worktree sessions use EnrichedSession shape
    Given a WorktreeRow with one EnrichedSession
    And the session has host Local, name "myrepo_main", status Running, claude Working, cost 1.23
    When serialized to JSON
    Then the session object has "name", "host", "status", and "claude" fields
    And "claude" is an object with "status" and "costUsd" fields
    And "host" is "local"
    And "status" is "running"

  @unit
  Scenario: JSON worktree session omits claude when no Claude info
    Given a WorktreeRow with one EnrichedSession and no ClaudeSessionInfo
    When serialized to JSON
    Then the session object has "claude" as null

  @unit
  Scenario: JSON output uses is_main_worktree instead of is_shepherd
    Given a worktree that is the repo main worktree
    When serialized to JSON
    Then the field is named "isMainWorktree" (camelCase)
    And there is no "isShepherd" field

  # -------------------------------------------------------------------
  # All existing tests pass, no behavior change
  # -------------------------------------------------------------------

  @integration
  Scenario: Existing worktree display behavior is preserved after refactor
    Given the same cached data as before the refactor
    When build_state processes the data
    Then the TUI rows contain the same information as before
    And display groups sort in the same order
    And Claude status is derived from the same underlying data

  @e2e
  Scenario: Orchard builds and all tests pass after Part 1
    When cargo test is run
    Then all tests pass
    And cargo build --release succeeds

  # =========================================================================
  # PART 2: Standalone Tmux Sessions (Shepherd)
  # =========================================================================

  # -------------------------------------------------------------------
  # Global config: tmux_sessions array
  # -------------------------------------------------------------------

  @unit
  Scenario: GlobalConfig deserializes tmux_sessions array
    Given a config.json with:
      """json
      {
        "repos": [],
        "tmux_sessions": [
          {
            "name": "shepherd",
            "command": "claude --agent shepherd",
            "cwd": "~/.config/orchard",
            "start_on_launch": true
          }
        ]
      }
      """
    When the config is loaded via load_from_path
    Then tmux_sessions has 1 entry
    And the entry name is "shepherd"
    And the entry command is "claude --agent shepherd"
    And the entry cwd is "~/.config/orchard"
    And start_on_launch is true

  @unit
  Scenario: GlobalConfig defaults tmux_sessions to empty when omitted
    Given a config.json with:
      """json
      {
        "repos": []
      }
      """
    When the config is loaded via load_from_path
    Then tmux_sessions is an empty array

  @unit
  Scenario: GlobalConfig preserves tmux_sessions through save round-trip
    Given a GlobalConfig with one tmux_session entry named "shepherd"
    When the config is saved and reloaded
    Then tmux_sessions still has 1 entry named "shepherd"

  @unit
  Scenario: Multiple tmux_sessions preserve config array order
    Given a config.json with tmux_sessions ["shepherd", "monitor", "logs"]
    When the config is loaded
    Then the entries appear in order: "shepherd", "monitor", "logs"

  # -------------------------------------------------------------------
  # ListEntry::Standalone constructed from config
  # -------------------------------------------------------------------

  @unit
  Scenario: Standalone sessions are constructed from global config
    Given a GlobalConfig with a tmux_session named "shepherd"
    And a running tmux session named "shepherd"
    When the state is built
    Then a ListEntry::Standalone is created for "shepherd"
    And its EnrichedSession reflects the running tmux session

  @unit
  Scenario: Standalone session shows as not-running when tmux session is dead
    Given a GlobalConfig with a tmux_session named "shepherd"
    And no running tmux session named "shepherd"
    When the state is built
    Then a ListEntry::Standalone is created for "shepherd"
    And its session status is Dead

  # -------------------------------------------------------------------
  # TUI rendering: standalone as regular rows
  # -------------------------------------------------------------------

  @integration
  Scenario: Standalone sessions render with same columns as worktree rows
    Given a standalone session named "shepherd" that is running
    When the TUI renders the row
    Then the session name column shows "shepherd"
    And the branch column is empty
    And the PR column is empty
    And the CI column is empty
    And the Claude column shows Claude status if detected

  @integration
  Scenario: Standalone sessions appear before worktree rows
    Given a standalone session "shepherd" and a worktree "myrepo/main"
    When the TUI renders the list
    Then "shepherd" appears in row 0
    And "myrepo/main" appears after it

  @integration
  Scenario: Multiple standalone sessions preserve config array order in TUI
    Given standalone sessions ["shepherd", "monitor"] in config order
    And worktree rows for "myrepo/main"
    When the TUI renders the list
    Then rows appear in order: "shepherd", "monitor", "myrepo/main"

  # -------------------------------------------------------------------
  # Enter key: attach or restart
  # -------------------------------------------------------------------

  @e2e
  Scenario: Enter on running standalone session attaches to it
    Given a standalone session "shepherd" that is running
    When the user presses Enter on the "shepherd" row
    Then orchard switches to the "shepherd" tmux session

  @e2e
  Scenario: Enter on not-running standalone session restarts it
    Given a standalone session "shepherd" configured with command "claude --agent shepherd"
    And the "shepherd" tmux session is not running
    When the user presses Enter on the "shepherd" row
    Then a new tmux session "shepherd" is created with the configured command
    And orchard switches to the "shepherd" tmux session

  # -------------------------------------------------------------------
  # Inapplicable key handlers
  # -------------------------------------------------------------------

  @integration
  Scenario: Hint bar dims inapplicable keys for standalone sessions
    Given the cursor is on a standalone session row
    When the hint bar renders
    Then the 'd' (delete worktree) hint is dimmed
    And the 'o' (open PR) hint is dimmed
    And the 'i' (open issue) hint is dimmed
    And the 'p' (priority) hint is dimmed
    And the Enter (attach) hint is not dimmed

  @integration
  Scenario: Pressing inapplicable key on standalone session shows warning modal
    Given the cursor is on a standalone session row
    When the user presses 'd'
    Then a brief dismissible warning modal appears with text like "This action requires a worktree"

  @integration
  Scenario: Warning modal is dismissible
    Given a warning modal is showing "This action requires a worktree"
    When the user presses any key
    Then the modal dismisses
    And the TUI returns to normal state

  # -------------------------------------------------------------------
  # Claude status detection for standalone sessions
  # -------------------------------------------------------------------

  @unit
  Scenario: Claude status detected for standalone sessions by session name
    Given a standalone session named "shepherd"
    And Claude hook state files exist for session "shepherd" showing Working status
    When the enrichment runs
    Then the standalone session's claude field has status Working

  @unit
  Scenario: Standalone session without Claude shows claude as None
    Given a standalone session named "monitor"
    And no Claude hook state files exist for session "monitor"
    When the enrichment runs
    Then the standalone session's claude field is None

  # -------------------------------------------------------------------
  # start_on_launch behavior
  # -------------------------------------------------------------------

  @e2e
  Scenario: start_on_launch creates tmux session on orchard startup
    Given a standalone session "shepherd" with start_on_launch true
    And no tmux session named "shepherd" exists
    When orchard starts
    Then a tmux session "shepherd" is created with the configured command and cwd

  @integration
  Scenario: start_on_launch skips creation when session already exists
    Given a standalone session "shepherd" with start_on_launch true
    And a tmux session named "shepherd" already exists
    When orchard starts
    Then no new tmux session is created for "shepherd"

  @integration
  Scenario: start_on_launch false does not auto-create session
    Given a standalone session "monitor" with start_on_launch false
    And no tmux session named "monitor" exists
    When orchard starts
    Then no tmux session is created for "monitor"
    And the "monitor" row shows as not-running

  # -------------------------------------------------------------------
  # Command failure handling
  # -------------------------------------------------------------------

  @integration
  Scenario: Orchard exits with error when start_on_launch command fails immediately
    Given a standalone session "broken" with start_on_launch true
    And command "nonexistent-binary --flag" that will fail immediately
    When orchard starts
    Then orchard exits with a non-zero exit code
    And the error message includes the session name "broken"
    And the error message includes the failure reason

  # -------------------------------------------------------------------
  # Session name collision validation
  # -------------------------------------------------------------------

  @unit
  Scenario: Config validation rejects standalone name that collides with worktree session name
    Given a GlobalConfig with a tmux_session named "myrepo_main"
    And a worktree whose derived session name is "myrepo_main"
    When config validation runs at parse time
    Then an error is raised mentioning the name collision
    And the error identifies both the standalone config and the worktree

  @unit
  Scenario: Config validation accepts standalone names that don't collide
    Given a GlobalConfig with a tmux_session named "shepherd"
    And worktrees with session names ["myrepo_main", "myrepo_feature-login"]
    When config validation runs
    Then no collision error is raised

  @unit
  Scenario: Config validation rejects duplicate standalone session names
    Given a GlobalConfig with two tmux_sessions both named "shepherd"
    When config validation runs
    Then an error is raised about duplicate standalone session names

  # -------------------------------------------------------------------
  # JSON v3: top-level tmux_sessions array
  # -------------------------------------------------------------------

  @unit
  Scenario: JSON v3 includes top-level tmux_sessions array
    Given a standalone session "shepherd" that is running with Claude active and cost 0.42
    When the state is serialized to JSON
    Then the output has a top-level "tmuxSessions" array
    And it contains one entry with name "shepherd"
    And the entry uses the same EnrichedSession shape as worktree sessions

  @unit
  Scenario: JSON v3 tmux_sessions uses same shape as worktree sessions
    Given a standalone session "shepherd" with host Local, status Running, claude Working, cost 0.42
    And a worktree session "myrepo_main" with host Local, status Running, claude Idle, cost 1.23
    When both are serialized to JSON
    Then the standalone session object has the same keys as the worktree session object
    And both have "name", "host", "status", "claude" fields

  @unit
  Scenario: JSON v3 tmux_sessions is empty array when no standalone sessions configured
    Given no standalone sessions in config
    When the state is serialized to JSON
    Then "tmuxSessions" is an empty array

  # -------------------------------------------------------------------
  # orchard init: shepherd session suggestion
  # -------------------------------------------------------------------

  @integration
  Scenario: Init wizard offers shepherd session setup
    Given the user runs "orchard init"
    When the wizard reaches the tmux sessions step
    Then it asks if the user wants to configure a shepherd session
    And it explains the shepherd concept briefly

  @integration
  Scenario: User accepts shepherd suggestion during init
    Given the wizard is at the tmux sessions step
    When the user accepts the shepherd suggestion
    Then tmux_sessions is set with a "shepherd" entry
    And the entry has sensible defaults for command and cwd

  @integration
  Scenario: User skips shepherd suggestion during init
    Given the wizard is at the tmux sessions step
    When the user declines the shepherd suggestion
    Then tmux_sessions remains empty in the config

  @integration
  Scenario: Existing configs without tmux_sessions still load after init changes
    Given a config.json from before this feature (no tmux_sessions field)
    When the config is loaded
    Then repos load correctly
    And tmux_sessions defaults to empty
    And no error occurs

  # -------------------------------------------------------------------
  # Full system integration
  # -------------------------------------------------------------------

  @e2e
  Scenario: Complete shepherd workflow from config to TUI display
    Given a global config with a shepherd session configured:
      """json
      {
        "repos": [{"slug": "acme/webapp", "path": "/home/user/webapp"}],
        "tmux_sessions": [
          {
            "name": "shepherd",
            "command": "claude --agent shepherd",
            "cwd": "/home/user/.config/orchard",
            "start_on_launch": true
          }
        ]
      }
      """
    When orchard starts in TUI mode
    Then the "shepherd" tmux session is created (start_on_launch)
    And the TUI shows "shepherd" as the first row
    And worktree rows for "acme/webapp" appear below it
    And the "shepherd" row shows Claude status if a Claude process is detected

  @e2e
  Scenario: Orchard builds and all tests pass after Part 2
    When cargo test is run
    Then all tests pass
    And cargo build --release succeeds
