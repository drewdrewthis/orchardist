@integration
Feature: Daemon conversations query — RecentLens response shape
  As any daemon consumer (GUI RecentLens)
  I need the conversations query to return all known conversations ordered by lastSeenAt
  So that consumers can render session history without client-side JSONL scanning.

  Background:
    Given a daemon httptest.Server is running with the claude projects directory wired

  @integration
  Scenario: conversations returns all conversations ordered latest-first
    Given the claude projects directory contains at least one JSONL file
    When the conversations query runs
    Then conversations is a non-empty list
    And the list is ordered latest-first by lastSeenAt
    And no GraphQL errors are present

  @integration
  Scenario: conversation entry carries required fields
    When the conversations query runs
    Then each conversation has id, sessionUuid, agentName, customTitle, cwd
    And each conversation has firstSeenAt, lastSeenAt, messageCount, open, recap
    And no GraphQL errors are present

  @integration
  Scenario: conversations is empty when no JSONL files exist
    Given the claude projects directory is empty
    When the conversations query runs
    Then conversations is an empty list
    And no GraphQL errors are present
