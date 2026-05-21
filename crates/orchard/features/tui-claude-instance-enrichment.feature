Feature: TUI Claude instance enrichment
  As the orchard TUI
  I need to display the state of Claude Code sessions running in worktree panes
  So that operators can see at a glance which worktrees have active Claude sessions and their status.

  Background:
    Given the daemon is running and returns a workView snapshot
    And the snapshot contains claudeInstances

  # ClaudeInstance fields read by the TUI:
  #   id, pane, process, state, sessionUuid, rcEnabled,
  #   lastActivityAt, model, inflightToolCount
  #
  # Client-side join: pane "TmuxPane:<host>:<session>:<window>:<pane>"
  # → extract session name → match to WorkViewTmuxSession
  # The daemon does NOT provide a pre-joined ClaudeInstance→Session field.

  @integration
  Scenario: Claude state is extracted from the pane reference
    Given a claudeInstance has pane "TmuxPane:local:issue429:editor:0"
    When the TUI adapter parses the pane reference
    Then the extracted session name is "issue429"
    And the claude enrichment is attached to the "issue429" session row

  @integration
  Scenario: Pane reference with colon in session name is handled
    Given a claudeInstance has pane "TmuxPane:local:my:session:1:0"
    When the TUI adapter parses the pane reference
    Then the extracted session name is "my:session"
    And no parse error occurs

  @integration
  Scenario: Malformed pane reference is silently dropped
    Given a claudeInstance has pane "not-a-pane-id"
    When the TUI adapter processes the instance
    Then no claude enrichment is attached to any session
    And no crash occurs

  @integration
  Scenario: Claude state values drive the activity indicator
    Given a claudeInstance has state "working"
    When the TUI renders the session row
    Then the row shows the "working" activity indicator (animated)

    Given state is "idle"
    Then the row shows the "idle" indicator

    Given state is "waiting"
    Then the row shows the "waiting" indicator

  @integration
  Scenario: Model name is displayed when present
    Given a claudeInstance has model "claude-opus-4-7"
    When the TUI renders the session row
    Then the model name is visible in the session detail

    Given model is null (daemon has not yet seen an assistant message)
    Then no model indicator is rendered

  @integration
  Scenario: inflightToolCount drives the in-flight tool badge
    Given a claudeInstance has inflightToolCount 3
    When the TUI renders the session row
    Then the row shows 3 in-flight tool calls

    Given inflightToolCount is 0
    Then no in-flight tool indicator is shown

  @integration
  Scenario: Freshness check on lastActivityAt
    Given a claudeInstance has lastActivityAt more than the stale threshold ago
    When the TUI adapter builds the ClaudeStateFile
    Then the enrichment is considered stale and is not attached to the session

    Given lastActivityAt is within the freshness window
    Then the enrichment is attached normally

  @integration
  Scenario: Standalone session with Claude enrichment
    Given a tmux session is not matched to any worktree path
    And it has an associated claudeInstance
    When the TUI builds standalone sessions
    Then the session appears in the standalone list with claude enrichment attached
