Feature: Session elapsed time in claude column
  As a developer using the Orchard TUI
  I want to see how long each Claude session has been in its current state
  So that I can quickly assess which sessions need attention

  Background:
    The claude column currently shows state (working/idle/input) but not
    duration. A new `state_changed_at` field in the hook state file tracks
    when the state last transitioned. Duration = now - state_changed_at.
    Format: `Nm` for minutes, `Nh` for hours, `Nd` for days.

  # --------------------------------------------------------------------------
  # Part 1: Hook script — add state_changed_at field
  # --------------------------------------------------------------------------

  @unit
  Scenario: Hook preserves state_changed_at when state is unchanged
    Given the state file has state "working" and state_changed_at "2026-04-13T10:00:00Z"
    When a PreToolUse event fires with state "working"
    Then the state file still has state_changed_at "2026-04-13T10:00:00Z"
    And the timestamp field is updated to now

  @unit
  Scenario: Hook updates state_changed_at when state transitions
    Given the state file has state "working" and state_changed_at "2026-04-13T10:00:00Z"
    When a Stop event fires transitioning state to "idle"
    Then state_changed_at is updated to the current time

  @unit
  Scenario: SessionStart sets state_changed_at fresh
    When a SessionStart event fires
    Then state_changed_at is set to the current time

  @unit
  Scenario: Existing hook fields unchanged
    Given any hook event fires
    Then state, session_id, tmux_session, cwd, event, timestamp are still written
    And session_start_ts, model, last_tool, current_task are still preserved

  # --------------------------------------------------------------------------
  # Part 2: Rust pipeline — parse and propagate state_changed_at
  # --------------------------------------------------------------------------

  @unit
  Scenario: ClaudeStateFile deserializes state_changed_at
    Given a state file JSON with state_changed_at "2026-04-13T10:00:00Z"
    When deserialized into ClaudeStateFile
    Then state_changed_at is Some("2026-04-13T10:00:00Z")

  @unit
  Scenario: ClaudeSessionInfo parses state_changed_at to epoch seconds
    Given a ClaudeStateFile with state_changed_at "2026-04-13T10:00:00Z"
    When ClaudeSessionInfo is constructed via from_state_file
    Then ClaudeSessionInfo.state_changed_at is Some(epoch seconds for that time)

  @unit
  Scenario: ClaudeEnrichment carries state_changed_at
    Given an EnrichedSession with ClaudeSessionInfo.state_changed_at = Some(1713002400)
    When converted to SessionState
    Then ClaudeEnrichment.state_changed_at is Some(1713002400)

  @unit
  Scenario: Missing state_changed_at yields None
    Given a state file JSON without state_changed_at field
    When deserialized and converted to ClaudeSessionInfo
    Then state_changed_at is None

  # --------------------------------------------------------------------------
  # Part 3: Duration formatting
  # --------------------------------------------------------------------------

  @unit
  Scenario: Duration under 1 minute shows 0m
    Given elapsed seconds = 45
    When format_elapsed is called
    Then the result is "0m"

  @unit
  Scenario: Duration formats as minutes for < 1 hour
    Given elapsed seconds = 1380
    When format_elapsed is called
    Then the result is "23m"

  @unit
  Scenario: Duration formats as hours for >= 1 hour
    Given elapsed seconds = 14400
    When format_elapsed is called
    Then the result is "4h"

  @unit
  Scenario: Duration formats as days for >= 24 hours
    Given elapsed seconds = 172800
    When format_elapsed is called
    Then the result is "2d"

  # --------------------------------------------------------------------------
  # Part 4: TUI claude column rendering with elapsed time
  # --------------------------------------------------------------------------

  @unit
  Scenario: Working state shows elapsed time in parentheses
    Given a session with claude status Working and state_changed_at 23 minutes ago
    When claude_status_text renders the row
    Then the text is "⚡ active (23m)" with green style

  @unit
  Scenario: Idle state shows elapsed time in parentheses
    Given a session with claude status Idle and state_changed_at 4 hours ago
    When claude_status_text renders the row
    Then the text is "● idle (4h)" with warning style

  @unit
  Scenario: Input state shows elapsed time in parentheses
    Given a session with claude status Input and state_changed_at 2 minutes ago
    When claude_status_text renders the row
    Then the text is "⚡ input (2m)" with needs-input style

  @unit
  Scenario: No session shows no badge
    Given a worktree row with no sessions
    When claude_status_text renders the row
    Then the text is "◯ none" (no duration)

  @unit
  Scenario: Missing state_changed_at omits duration
    Given a session with claude status Working but state_changed_at is None
    When claude_status_text renders the row
    Then the text is "⚡ active" with no duration suffix

  @unit
  Scenario: Multiple sessions shows most urgent state with oldest duration
    Given 2 sessions: Working (state_changed_at 23m ago) and Idle (4h ago)
    When claude_status_text renders the row
    Then shows the most urgent state (working if any working, input if any input)
    And the duration is from the session that has been in that state longest

  @unit
  Scenario: Standalone session shows elapsed time
    Given a standalone session with claude status Working and state_changed_at 5m ago
    When standalone_claude_status renders
    Then the text includes "5m"

  # --------------------------------------------------------------------------
  # Part 5: Column width accommodation
  # --------------------------------------------------------------------------

  @unit
  Scenario: Claude column width accommodates elapsed time
    Given the TUI layout constraints
    Then the CLAUDE column width is at least 16
    And the fixed-width calculation in render_rows matches

  # --------------------------------------------------------------------------
  # Part 6: JSON output includes state_elapsed_sec
  # --------------------------------------------------------------------------

  @unit
  Scenario: JSON output includes state_elapsed_sec
    Given a ClaudeEnrichment with state_changed_at = 120 seconds ago
    When serialized to JsonClaudeInfo
    Then state_elapsed_sec is approximately 120
