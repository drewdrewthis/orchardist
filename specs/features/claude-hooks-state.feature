Feature: Claude Code hooks for session state detection
  As an orchard user
  I want Claude session state detected via Claude Code hooks instead of terminal scraping
  So that state detection is reliable, structured, and enriched with cost/context data

  Background:
    Given the cache directory is "~/.cache/orchard/"
    And Claude hook state files are written to "/tmp/orchard-claude-{tmux_session_name}.json"

  # ===================================================================
  # Hook script — event handling and state file writes
  # ===================================================================

  @unit
  Scenario: Hook script writes state file on PreToolUse event
    Given a Claude session running in tmux session "repo_47_claude"
    And the hook script receives a PreToolUse event on stdin
    Then the script writes to "/tmp/orchard-claude-repo_47_claude.json"
    And the file contains JSON with:
      | field      | value     |
      | state      | "working" |
    And the file contains a valid ISO 8601 timestamp field
    And the file contains the session_id from the event
    And the file contains the cwd from the event

  @unit
  Scenario: Hook script writes state file on PostToolUse event
    Given a Claude session running in tmux session "repo_47_claude"
    And the hook script receives a PostToolUse event on stdin
    Then the state file contains:
      | field | value     |
      | state | "working" |

  @unit
  Scenario: Hook script writes idle state on Stop event
    Given a Claude session running in tmux session "repo_47_claude"
    And the hook script receives a Stop event on stdin
    Then the state file contains:
      | field | value  |
      | state | "idle" |

  @unit
  Scenario: Hook script writes input state on permission_prompt Notification
    Given a Claude session running in tmux session "repo_47_claude"
    And the hook script receives a Notification event with type "permission_prompt"
    Then the state file contains:
      | field | value   |
      | state | "input" |

  @unit
  Scenario: Hook script writes input state on idle_prompt Notification
    Given a Claude session running in tmux session "repo_47_claude"
    And the hook script receives a Notification event with type "idle_prompt"
    Then the state file contains:
      | field | value   |
      | state | "input" |

  @unit
  Scenario: Hook script deletes state file on SessionEnd event
    Given a state file exists at "/tmp/orchard-claude-repo_47_claude.json"
    And the hook script receives a SessionEnd event
    Then the file "/tmp/orchard-claude-repo_47_claude.json" no longer exists

  @unit
  Scenario: Hook script derives tmux session name from TMUX_PANE environment
    Given TMUX_PANE is set to "%5"
    And tmux reports the session name for pane "%5" is "repo_47_claude"
    When the hook script runs
    Then the state file path is "/tmp/orchard-claude-repo_47_claude.json"

  @unit
  Scenario: Hook script is a no-op when not running inside tmux
    Given TMUX_PANE is not set
    When the hook script receives any event
    Then no state file is written
    And the script exits 0

  @unit
  Scenario: State file contains required fields
    Given a Claude session running in tmux session "repo_47_claude"
    When the hook script writes a state file
    Then the file contains valid JSON with at minimum:
      | field      | type   | description                        |
      | state      | string | "working", "idle", "input"         |
      | session_id | string | Claude session identifier          |
      | timestamp  | string | ISO 8601 UTC when last updated     |
      | cwd        | string | Current working directory           |
      | event      | string | Hook event name that triggered this |

  # ===================================================================
  # StatusLine enrichment — merging cost/context into state file
  # ===================================================================

  @unit
  Scenario: StatusLine script merges enriched data into existing state file
    Given a state file exists at "/tmp/orchard-claude-repo_47_claude.json" with state "working"
    When the statusline script runs for session "repo_47_claude"
    Then the state file is updated with additional fields:
      | field              | type   | description                        |
      | context_window_pct | number | Percentage of context window used  |
      | cost_usd           | number | Cumulative session cost in USD     |
      | model              | string | Model name (e.g., "opus", "sonnet")|
    And the existing state and session_id fields are preserved

  @unit
  Scenario: StatusLine script creates state file when none exists
    Given no state file exists for session "repo_47_claude"
    When the statusline script runs for session "repo_47_claude"
    Then a state file is created at "/tmp/orchard-claude-repo_47_claude.json"
    And the state field defaults to "working"
    And the enriched fields (context_window_pct, cost_usd, model) are present

  @unit
  Scenario: StatusLine updates on each assistant message
    Given a Claude session that has sent 3 assistant messages
    Then the state file has been updated 3 times by the statusline script
    And the timestamp reflects the most recent assistant message

  # ===================================================================
  # Cache source — reading hook state files
  # ===================================================================

  @unit
  Scenario: Claude states cache file is global
    Then the following cache file is global (not per-repo):
      | source        | filename           |
      | Claude States | claude_states.json |

  @integration
  Scenario: Cache source reads all hook state files from /tmp
    Given the following files exist in /tmp:
      | file                                      |
      | orchard-claude-repo_47_claude.json         |
      | orchard-claude-repo_48_main.json           |
      | orchard-claude-api_server_12_claude.json   |
    When the Claude state cache source refreshes
    Then it reads and parses all 3 files
    And stores the parsed data in the claude_states cache

  @unit
  Scenario: Cache source ignores malformed state files
    Given "/tmp/orchard-claude-broken.json" contains invalid JSON
    When the Claude state cache source refreshes
    Then the malformed file is skipped without error
    And other valid state files are still parsed

  @unit
  Scenario: Cache source ignores non-orchard files in /tmp
    Given "/tmp/other-file.json" exists
    When the Claude state cache source refreshes
    Then only files matching "orchard-claude-*.json" are read

  @integration
  Scenario: Missing state files do not block tmux session display
    Given a tmux session "repo_47_claude" exists with Claude running
    And no state file exists for "repo_47_claude"
    When the derive step runs
    Then the session still appears in the TUI
    And state detection falls back to terminal scraping

  # ===================================================================
  # State derivation — hook-first with terminal scraping fallback
  # ===================================================================

  @unit
  Scenario: State is read directly from hook state file
    Given a hook state file for session "repo_47_claude" with state "working"
    When the Claude session state is derived
    Then the state is "working"
    And no terminal scraping is performed

  @unit
  Scenario: Idle state from hook file
    Given a hook state file for session "repo_47_claude" with state "idle"
    When the Claude session state is derived
    Then the state is "idle"

  @unit
  Scenario: Input state from hook file
    Given a hook state file for session "repo_47_claude" with state "input"
    When the Claude session state is derived
    Then the state is "input"

  @unit
  Scenario: Very old state file is treated as stale and falls back to scraping
    Given a hook state file for session "repo_47_claude"
    And the timestamp is 10 minutes ago
    And the tmux pane is running Claude
    And the terminal output contains "(y/n)"
    When the Claude session state is derived
    Then the state is "input"
    And the detection used terminal scraping as the source

  @unit
  Scenario: Staleness threshold for hook state files is 5 minutes
    Given a hook state file with timestamp 4 minutes 59 seconds ago
    When the Claude session state is derived
    Then the hook state is used directly (not stale)

  @unit
  Scenario: Hook state file older than 5 minutes triggers fallback
    Given a hook state file with timestamp 5 minutes 1 second ago
    When the Claude session state is derived
    Then the detection falls back to terminal scraping

  @unit
  Scenario: Fallback to terminal scraping when no hook state file exists
    Given no state file exists for session "repo_47_claude"
    And the tmux pane is running Claude
    And the terminal output contains "(y/n)"
    When the Claude session state is derived
    Then the state is "input"
    And the detection used terminal scraping as the source

  @unit
  Scenario: No Claude process and no state file yields "none" state
    Given no state file exists for session "repo_47_main"
    And the tmux pane is running "zsh" (not Claude)
    When the Claude session state is derived
    Then the state is "none"

  # ===================================================================
  # SessionInfo enrichment — replacing boolean flags
  # ===================================================================

  @unit
  Scenario: SessionInfo includes claude_state enum from hook data
    When session_info_from is called for a session with hook state data
    Then the SessionInfo contains a claude_state field
    And the possible values are: "working", "idle", "input", "none"

  @unit
  Scenario: SessionInfo includes context and cost from hook state file
    Given a hook state file with context_window_pct 73 and cost_usd 0.42
    When session_info_from is called
    Then the SessionInfo contains context_window_pct 73
    And the SessionInfo contains cost_usd 0.42

  @unit
  Scenario: SessionInfo includes model from hook state file
    Given a hook state file with model "opus"
    When session_info_from is called
    Then the SessionInfo contains model "opus"

  @unit
  Scenario: SessionInfo without hook state has no context, cost, or model
    Given no state file for the session
    When session_info_from is called
    Then context_window_pct is None
    And cost_usd is None
    And model is None

  # ===================================================================
  # Display group derivation — using hook state
  # ===================================================================

  @unit
  Scenario: Claude working state maps to ClaudeWorking display group
    Given a session with claude_state "working"
    And the PR is not in a needs_attention state
    When the display group is derived
    Then the display group is "claude_working"

  @unit
  Scenario: Claude input state maps to NeedsAttention display group
    Given a session with claude_state "input"
    When the display group is derived
    Then the display group is "needs_attention"

  @unit
  Scenario: Claude idle state does not override other display group logic
    Given a session with claude_state "idle"
    And the PR has review_decision "approved" and checks_state "passing"
    When the display group is derived
    Then the display group is "ready_to_merge"

  # ===================================================================
  # TUI display — richer Claude info
  # ===================================================================

  @e2e
  Scenario: Working Claude shows green active indicator with context percentage
    Given a Claude session with hook state "working"
    And context_window_pct is 73
    When the TUI renders
    Then the CLAUDE column shows "active 73%" in green

  @e2e
  Scenario: Idle Claude shows yellow idle indicator
    Given a Claude session with hook state "idle"
    When the TUI renders
    Then the CLAUDE column shows "idle" in yellow

  @e2e
  Scenario: Claude waiting for input shows red input indicator
    Given a Claude session with hook state "input"
    When the TUI renders
    Then the CLAUDE column shows "input" in red

  @e2e
  Scenario: Preview pane shows cost and model when available
    Given a Claude session with cost_usd 1.23 and model "opus"
    When the user selects the session and the preview pane renders
    Then the preview pane shows "Model: opus"
    And the preview pane shows "Cost: $1.23"

  @e2e
  Scenario: Context percentage not shown when no hook state exists
    Given a Claude session detected via terminal scraping (no hook state)
    When the TUI renders
    Then the CLAUDE column shows state without a percentage

  # ===================================================================
  # Notifications — using hook state transitions
  # ===================================================================

  @integration
  Scenario: Notification fires on working-to-input transition
    Given the previous refresh showed claude_state "working" for "repo_47_claude"
    And the current refresh shows claude_state "input"
    When the notification check runs
    Then a notification "Claude needs input" is sent

  @integration
  Scenario: Notification fires on working-to-idle transition
    Given the previous refresh showed claude_state "working" for "repo_47_claude"
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
  Scenario: No notification on startup with existing state file
    Given this is the first refresh after TUI startup
    And a state file exists with state "idle" for "repo_47_claude"
    When the notification check runs
    Then no notification is sent

  # ===================================================================
  # Hook registration — orchard init
  # ===================================================================

  @integration
  Scenario: orchard init registers the hook script in Claude settings
    When the user runs "orchard init"
    Then the hook script is installed at "~/.claude/hooks/orchard-state.sh"
    And the script is executable
    And "~/.claude/settings.json" contains hook registrations for:
      | event         |
      | PreToolUse    |
      | PostToolUse   |
      | Stop          |
      | Notification  |
      | SessionEnd    |

  @integration
  Scenario: orchard init is idempotent for hook registration
    Given the hook script is already registered in "~/.claude/settings.json"
    When the user runs "orchard init" again
    Then the hook registration is not duplicated
    And the script file is updated to the latest version

  @unit
  Scenario: Hook script is self-contained with no external dependencies
    Given the hook script at "~/.claude/hooks/orchard-state.sh"
    Then it uses only POSIX shell and standard tools (jq, tmux)
    And it does not depend on the orchard binary at runtime

  # ===================================================================
  # Cleanup — stale state files
  # ===================================================================

  @unit
  Scenario: State files for dead sessions are ignored
    Given a state file "/tmp/orchard-claude-old_session.json" exists
    And no tmux session named "old_session" is running
    When the Claude state cache source refreshes
    Then the file is read but produces no active state
    And no error is raised

  @integration
  Scenario: SessionEnd hook cleans up state file immediately
    Given a Claude session in tmux session "repo_47_claude" ends
    And the SessionEnd hook fires
    Then "/tmp/orchard-claude-repo_47_claude.json" is deleted
    And the next cache refresh does not see a phantom session

  @unit
  Scenario: Orphaned state files do not create phantom sessions
    Given "/tmp/orchard-claude-repo_47_claude.json" exists
    And no tmux session "repo_47_claude" is running
    When the derive step runs
    Then the state file is ignored (no matching tmux session)
    And no phantom session row appears in the TUI

  # ===================================================================
  # Cache refresh pipeline integration
  # ===================================================================

  @integration
  Scenario: Claude state refresh is part of the cache refresh pipeline
    When the background cache refresh runs
    Then refresh_claude_state is called alongside other refresh functions
    And it reads /tmp/orchard-claude-*.json files
    And writes to the claude_states cache file

  @unit
  Scenario: Claude state refresh does not block other cache sources
    When the background refresh runs
    Then the Claude state refresh runs concurrently with:
      | source            |
      | GitHub Issues     |
      | GitHub PRs        |
      | Git Worktrees     |
      | Tmux Sessions     |
