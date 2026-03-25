Feature: Claude session state detection via status line
  As an orchard user
  I want accurate Claude session state (active, idle, input)
  So that I can tell at a glance whether Claude is working, finished, or blocked on me

  Background:
    Given the cache directory is "~/.cache/orchard/"
    And Claude status line files are written to "/tmp/orchard-claude-<session_name>.json"

  # ===================================================================
  # Status line script — configuration and output
  # ===================================================================

  @unit
  Scenario: Status line script writes JSON to a known file path
    Given a Claude session running in tmux session "repo_47_claude"
    And the status line script is configured for that session
    When Claude outputs an assistant message
    Then the script writes to "/tmp/orchard-claude-repo_47_claude.json"
    And the file contains valid JSON with at minimum:
      | field              | type   | description                                |
      | timestamp          | string | ISO 8601 UTC when status line last updated |
      | session_id         | string | Claude session identifier                  |
      | model              | string | Model name (e.g., "opus", "sonnet")        |
      | context_window_pct | number | Percentage of context window used (0-100)  |
      | cost_usd           | number | Cumulative session cost in USD             |
      | cwd                | string | Current working directory                  |

  @unit
  Scenario: Status line file uses session name from tmux
    Given a Claude session in tmux session "orchard-rs_47_main"
    When the status line script writes a status update
    Then the file path is "/tmp/orchard-claude-orchard-rs_47_main.json"

  @unit
  Scenario: Status line updates on each assistant message
    Given a Claude session that has sent 3 assistant messages
    Then the status line file has been written 3 times
    And the timestamp reflects the most recent assistant message

  @unit
  Scenario: Status line updates on permission mode changes
    Given Claude enters permission mode (e.g., tool approval prompt)
    Then the status line file is updated with the current timestamp

  # ===================================================================
  # Cache source — reading status line files
  # ===================================================================

  @unit
  Scenario: Claude status line cache file is global
    Then the following cache file is global (not per-repo):
      | source                | filename                  |
      | Claude Status Lines   | claude_status_lines.json  |

  @integration
  Scenario: Cache source reads all status line files from /tmp
    Given the following files exist in /tmp:
      | file                                         |
      | orchard-claude-repo_47_claude.json            |
      | orchard-claude-repo_48_main.json              |
      | orchard-claude-api_server_12_claude.json      |
    When the Claude status line cache source refreshes
    Then it reads and parses all 3 files
    And stores the parsed data in the claude_status_lines cache

  @unit
  Scenario: Cache source ignores malformed status line files
    Given "/tmp/orchard-claude-broken.json" contains invalid JSON
    When the Claude status line cache source refreshes
    Then the malformed file is skipped without error
    And other valid status line files are still parsed

  @unit
  Scenario: Cache source ignores non-orchard files in /tmp
    Given "/tmp/other-file.json" exists
    When the Claude status line cache source refreshes
    Then only files matching "orchard-claude-*.json" are read

  @integration
  Scenario: Missing status line files do not block tmux session display
    Given a tmux session "repo_47_claude" exists with Claude running
    And no status line file exists for "repo_47_claude"
    When the derive step runs
    Then the session still appears in the TUI
    And state detection falls back to terminal scraping

  # ===================================================================
  # State derivation — three-way distinction
  # ===================================================================

  @unit
  Scenario: Active state when status line timestamp is recent
    Given a status line file for session "repo_47_claude"
    And the timestamp is 5 seconds ago
    When the Claude session state is derived
    Then the state is "active"

  @unit
  Scenario: Active state at the 30-second boundary
    Given a status line file for session "repo_47_claude"
    And the timestamp is 29 seconds ago
    When the Claude session state is derived
    Then the state is "active"

  @unit
  Scenario: Idle state when timestamp is stale and no prompt patterns
    Given a status line file for session "repo_47_claude"
    And the timestamp is 45 seconds ago
    And the terminal output does not contain prompt patterns like "(y/n)" or "?"
    When the Claude session state is derived
    Then the state is "idle"

  @unit
  Scenario: Input state when timestamp is stale and permission prompt detected
    Given a status line file for session "repo_47_claude"
    And the timestamp is 45 seconds ago
    And the terminal output contains "(y/n)"
    When the Claude session state is derived
    Then the state is "input"

  @unit
  Scenario: Input state when timestamp is stale and question prompt detected
    Given a status line file for session "repo_47_claude"
    And the timestamp is 60 seconds ago
    And the terminal output contains "Do you want to proceed?"
    When the Claude session state is derived
    Then the state is "input"

  @unit
  Scenario: Idle prompt character no longer triggers input state
    Given a status line file for session "repo_47_claude"
    And the timestamp is 45 seconds ago
    And the terminal output last line is ">"
    When the Claude session state is derived
    Then the state is "idle"
    And not "input"

  @unit
  Scenario: None state when no status line file exists and no Claude process
    Given no status line file exists for session "repo_47_main"
    And the tmux pane is running "zsh" (not Claude)
    When the Claude session state is derived
    Then the state is "none"

  @unit
  Scenario: Staleness threshold is 30 seconds
    Given a status line file with timestamp exactly 30 seconds ago
    When the Claude session state is derived
    Then the state is "active"

  @unit
  Scenario: Timestamp 31 seconds ago is stale
    Given a status line file with timestamp 31 seconds ago
    And no prompt patterns in terminal output
    When the Claude session state is derived
    Then the state is "idle"

  # ===================================================================
  # Fallback — terminal scraping still works without status line
  # ===================================================================

  @unit
  Scenario: Fallback to terminal scraping when no status line file
    Given no status line file exists for session "repo_47_claude"
    And the tmux pane is running Claude
    And the terminal output contains "(y/n)"
    When the Claude session state is derived
    Then the state is "input"
    And the detection used terminal scraping as the source

  @unit
  Scenario: Fallback to terminal scraping when status line file is very old
    Given a status line file for session "repo_47_claude"
    And the timestamp is 10 minutes ago
    And the tmux pane is running Claude
    And the terminal output contains "(y/n)"
    When the Claude session state is derived
    Then the state is "input"
    And the detection used terminal scraping as the source

  # ===================================================================
  # SessionInfo enrichment — replacing boolean flags
  # ===================================================================

  @unit
  Scenario: SessionInfo includes derived Claude state enum
    When session_info_from is called for a session with status line data
    Then the SessionInfo contains a claude_state field
    And the possible values are: "active", "idle", "input", "none"

  @unit
  Scenario: SessionInfo includes context and cost from status line
    Given a status line file with context_window_pct 73 and cost_usd 0.42
    When session_info_from is called
    Then the SessionInfo contains context_window_pct 73
    And the SessionInfo contains cost_usd 0.42

  @unit
  Scenario: SessionInfo without status line has no context or cost
    Given no status line file for the session
    When session_info_from is called
    Then context_window_pct is None
    And cost_usd is None

  # ===================================================================
  # TUI display — accurate state indicators
  # ===================================================================

  @e2e
  Scenario: Active Claude shows green active indicator
    Given a Claude session with recent status line timestamp (< 30s)
    When the TUI renders
    Then the session shows "active" in green

  @e2e
  Scenario: Idle Claude shows yellow idle indicator
    Given a Claude session with stale status line timestamp (> 30s)
    And no prompt patterns in terminal output
    When the TUI renders
    Then the session shows "idle" in yellow

  @e2e
  Scenario: Claude waiting for permission shows red input indicator
    Given a Claude session with stale status line timestamp (> 30s)
    And terminal output contains "(y/n)"
    When the TUI renders
    Then the session shows "input" in red

  @e2e
  Scenario: Context window percentage shown in session detail
    Given a Claude session with context_window_pct 85
    When the TUI renders the session in expanded view
    Then the context window usage "85%" is displayed

  @e2e
  Scenario: Session cost shown in session detail
    Given a Claude session with cost_usd 1.23
    When the TUI renders the session in expanded view
    Then the cost "$1.23" is displayed

  # ===================================================================
  # Display group derivation — using new three-way state
  # ===================================================================

  @unit
  Scenario: Claude active maps to ClaudeWorking display group
    Given a session with claude_state "active"
    And the PR is not in a needs_attention state
    When the display group is derived
    Then the display group is "claude_working"

  @unit
  Scenario: Claude input maps to NeedsAttention display group
    Given a session with claude_state "input"
    When the display group is derived
    Then the display group is "needs_attention"

  @unit
  Scenario: Claude idle does not override other display group logic
    Given a session with claude_state "idle"
    And the PR has review_decision "approved" and checks_state "passing"
    When the display group is derived
    Then the display group is "ready_to_merge"

  # ===================================================================
  # Notifications — using accurate state transitions
  # ===================================================================

  @integration
  Scenario: Notification fires on active-to-input transition
    Given the previous refresh showed claude_state "active" for "repo_47_claude"
    And the current refresh shows claude_state "input"
    When the notification check runs
    Then a notification "Claude needs input" is sent

  @integration
  Scenario: Notification fires on active-to-idle transition
    Given the previous refresh showed claude_state "active" for "repo_47_claude"
    And the current refresh shows claude_state "idle"
    When the notification check runs
    Then a notification "Claude finished" is sent

  @integration
  Scenario: No notification on idle-to-idle (no false positives)
    Given the previous refresh showed claude_state "idle" for "repo_47_claude"
    And the current refresh shows claude_state "idle"
    When the notification check runs
    Then no notification is sent

  @integration
  Scenario: No notification on startup with stale status line
    Given this is the first refresh after TUI startup
    And a status line file exists with stale timestamp for "repo_47_claude"
    When the notification check runs
    Then no notification is sent

  # ===================================================================
  # Cleanup — stale status line files
  # ===================================================================

  @unit
  Scenario: Status line files for dead sessions are ignored
    Given a status line file "/tmp/orchard-claude-old_session.json" exists
    And no tmux session named "old_session" is running
    When the Claude status line cache source refreshes
    Then the file is read but produces no active state
    And no error is raised

  @integration
  Scenario: Status line files are cleaned up when session ends
    Given a tmux session "repo_47_claude" was killed
    And "/tmp/orchard-claude-repo_47_claude.json" still exists
    When the next cache refresh runs
    Then the stale file does not create phantom sessions
    And the file may be cleaned up by the OS or a periodic sweep
