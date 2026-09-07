Feature: SessionStart clears stale attention message
  As a claude-session-state consumer (TUI, daemon)
  I want the state file's message cleared when a session restarts
  So that a resumed/cleared/compacted session never shows a stale "needs your permission" message after it goes idle

  Background:
    Given the "claude-session-state" plugin's "fold-state.sh" script
    And a fresh state directory set via "CLAUDE_SESSION_STATE_DIR"

  @integration
  Scenario: SessionStart nulls a stale permission message on resume
    Given a "Notification" event for session "sid-1" with message "Claude needs your permission to use Bash" has been folded, leaving state "input" and a non-null message
    When a "SessionStart" event with source "resume" is folded for the same session "sid-1"
    Then the state file's "state" field is "idle"
    And the state file's "message" field is null

  @integration
  Scenario: SessionStart nulls a stale permission message on clear
    Given a "Notification" event for session "sid-2" with message "Claude needs your permission to use Write" has been folded, leaving state "input" and a non-null message
    When a "SessionStart" event with source "clear" is folded for the same session "sid-2"
    Then the state file's "state" field is "idle"
    And the state file's "message" field is null

  @integration
  Scenario: SessionStart nulls a stale permission message on compact
    Given a "Notification" event for session "sid-3" with message "Claude needs your permission to use Edit" has been folded, leaving state "input" and a non-null message
    When a "SessionStart" event with source "compact" is folded for the same session "sid-3"
    Then the state file's "state" field is "idle"
    And the state file's "message" field is null

  @unit
  Scenario: SessionStart with no prior state still produces state=idle and message=null
    Given no prior state file exists for session "sid-4"
    When a "SessionStart" event with source "startup" is folded for session "sid-4"
    Then the state file's "state" field is "idle"
    And the state file's "message" field is null

  @integration
  Scenario: PreToolUse and PostToolUse still clear a stale message (regression)
    Given a "Notification" event for session "sid-5" with a non-null message has been folded
    When a "PreToolUse" event with tool_name "Bash" is folded for the same session "sid-5"
    Then the state file's "state" field is "working"
    And the state file's "message" field is null
    Given a "Notification" event for session "sid-5" with a non-null message has been folded again
    When a "PostToolUse" event with tool_name "Bash" is folded for the same session "sid-5"
    Then the state file's "state" field is "working"
    And the state file's "message" field is null

  @integration
  Scenario: Stop and UserPromptSubmit still clear a stale message (regression)
    Given a "Notification" event for session "sid-6" with a non-null message has been folded
    When a "Stop" event is folded for the same session "sid-6"
    Then the state file's "state" field is "idle"
    And the state file's "message" field is null
    Given a "Notification" event for session "sid-6" with a non-null message has been folded again
    When a "UserPromptSubmit" event with prompt "do the thing" is folded for the same session "sid-6"
    Then the state file's "state" field is "working"
    And the state file's "message" field is null

  # --- AC Coverage Map ---
  # AC 1: "SessionStart nulls .message" → Scenario: SessionStart nulls a stale permission message on resume / on clear / on compact / with no prior state
  # AC 2: "docs/STATE_SCHEMA.md message row updated" → not a jq-observable scenario; covered by the plan's doc-grep evidence directly, not this feature file (spec.md scope is behavioral contract, not doc content)
  # AC 3: "Live proof by hand" → same scenarios as AC 1; proof is the same fold-state.sh invocation demonstrated in the running system
  # AC 4: "Regression: other clearing branches unchanged" → Scenario: PreToolUse and PostToolUse still clear a stale message (regression); Scenario: Stop and UserPromptSubmit still clear a stale message (regression)
