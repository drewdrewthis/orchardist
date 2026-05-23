@integration
Feature: Daemon OpenPanel query — PanelData response shape
  As any daemon consumer (GUI SessionPane)
  I need the OpenPanel query to return panes, claudeInstances, conversations, and worktrees
  So that consumers can render the panel without client-side joins.

  Background:
    Given a daemon httptest.Server is running

  @integration
  Scenario: OpenPanel by paneId resolves all panel data fields
    Given at least one live Claude REPL is running in a tmux pane
    When SessionPane fires OpenPanel with paneIds = ["%26"]
    Then tmuxPanes returns the pane matching %26 with PaneCard fields
    And claudeInstances includes the SessionCard for the Claude process in that pane
    And conversations includes the Conversation matching the instance sessionUuid
    And { repos { worktrees { id path branch } } } returns a WorktreeEnrichment matching the session cwd
    And no GraphQL errors are present

  @integration
  Scenario: Conversation fields for header rendering
    Given OpenPanel returns a conversation
    Then conversation.jsonlPath is a non-empty string
    And conversation.firstSeenAt and lastSeenAt are RFC3339 timestamps or null
    And conversation.messageCount is a non-negative integer
    And conversation.open is a boolean
    And conversation.recap is a string or null
    And conversation.agentName and customTitle are strings or null
    And no GraphQL errors are present

  @integration
  Scenario: OpenPanel returns empty when nothing resolves
    When paneId and sessionUuid both fail to match any daemon state
    Then tmuxPanes is empty
    And claudeInstances is empty
    And no GraphQL errors are present
