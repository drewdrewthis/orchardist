@integration
Feature: Daemon tmuxServer query — TmuxLens response shape
  As any daemon consumer (GUI TmuxLens, TUI)
  I need the tmuxServer query to return the full server/session/window/pane tree
  So that consumers can render the tmux topology without client-side tmux calls.

  Background:
    Given a daemon httptest.Server is running

  @integration
  Scenario: tmuxServer returns alive flag and sessions
    When the TmuxLens query runs
    Then the response contains a tmuxServer field
    And tmuxServer has id, alive, sessions, clients
    And no GraphQL errors are present

  @integration
  Scenario: tmuxServer session carries required fields
    Given a tmux server is running with at least one session
    When the TmuxLens query runs
    Then each session has id, name, attached, activeAttached, lastActivityAt, windows
    And no GraphQL errors are present

  @integration
  Scenario: tmuxServer window carries required fields
    Given a tmux server is running with at least one session
    When the TmuxLens query runs
    Then each window has id, index, name, active, panes
    And no GraphQL errors are present

  @integration
  Scenario: PaneCard spreads required fields
    Given a tmux server is running with at least one pane
    When the TmuxLens query runs
    Then each pane has paneId, title, currentCommand, currentPid
    And pane.window.session.name is present
    And no GraphQL errors are present

  @integration
  Scenario: tmuxServer unreachable — alive is false, sessions is empty
    Given no tmux server is running on this host
    When the TmuxLens query runs
    Then tmuxServer.alive is false
    And tmuxServer.sessions is an empty list
    And no GraphQL errors are present

  @integration
  Scenario: TmuxLens does not include pane content
    When the TmuxLens query runs
    Then the response does not include content, contentRange, or contentFull fields on panes
    And no GraphQL errors are present

  @integration
  Scenario: clients field carries tty and currentPane.paneId
    Given at least one tmux client is attached
    When the TmuxLens query runs
    Then tmuxServer.clients contains items with tty and currentPane.paneId
    And no GraphQL errors are present
