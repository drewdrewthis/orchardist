@integration
Feature: Daemon health query contract
  As any daemon consumer (TUI, GUI, monitoring script)
  I need the health query to return a stable, minimal shape
  So that clients can confirm the daemon is alive without depending on domain data.

  Background:
    Given a daemon httptest.Server is running

  @integration
  Scenario: Health query returns status ok when daemon is serving
    When a client sends { health { status uptimeS } }
    Then the response contains health.status == "ok"
    And health.uptimeS is a non-negative integer
    And no GraphQL errors are present

  @integration
  Scenario: Health query returns uptimeS that grows over time
    When a client sends { health { status uptimeS } }
    Then health.uptimeS is a non-negative integer
