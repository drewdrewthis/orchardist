@integration
Feature: Daemon sendTextToPane mutation — contract
  As any daemon consumer (GUI SessionComposer)
  I need the sendTextToPane mutation to exec tmux send-keys and return true
  So that consumers can send text to a Claude REPL without direct tmux access.

  Background:
    Given a daemon httptest.Server is running

  @integration
  Scenario: sendTextToPane returns true for a valid pane
    Given a live tmux pane "%26" is reachable
    When sendTextToPane is called with paneId = "%26" and text = "hello"
    Then the response data.sendTextToPane = true
    And no GraphQL errors are present

  @integration
  Scenario: sendTextToPane returns GraphQL error for non-existent pane
    Given pane "%999" does not exist
    When sendTextToPane is called with paneId = "%999" and text = "hello"
    Then the daemon returns a GraphQL error with a descriptive message
    And the HTTP status is 200

  @integration
  Scenario: sendTextToPane response is HTTP 200 with GraphQL errors — not HTTP 4xx/5xx
    Given any error condition when calling sendTextToPane
    When the mutation executes
    Then the HTTP status code is 200
    And errors are reported in the GraphQL errors array
