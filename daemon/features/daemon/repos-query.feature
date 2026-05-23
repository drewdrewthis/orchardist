@integration
Feature: Daemon repos query — dashboard data contract
  As any daemon consumer (TUI, GUI)
  I need the { repos { worktrees { ... } } } query to return repos, worktrees, tmuxSessions, and claudeInstances
  So that consumers can render the dashboard without client-side joins.

  Background:
    Given a daemon httptest.Server is running with a real git repo

  @integration
  Scenario: repos query returns array with required fields
    When the consumer fires { repos { id slug path worktrees { id } } }
    Then the response contains a repos array
    And each repo entry has id, slug, and path fields
    And each repo entry has a worktrees array
    And no GraphQL errors are present

  @integration
  Scenario: worktree carries branch, head, bare, host, repo, ahead, behind
    When the consumer fires { repos { worktrees { branch head bare host ahead behind repo { id } } } }
    Then each worktree in repos[].worktrees carries branch, head, bare, host, repo, ahead, behind
    And no GraphQL errors are present

  @integration
  Scenario: worktree carries nullable pr object with number, state, title
    When the consumer fires { repos { worktrees { pr { number state title } } } }
    Then each worktree carries a nullable pr object
    And when pr is non-null it has number, state, and title fields
    And no GraphQL errors are present

  @integration
  Scenario: worktree carries nullable issue object with number, state, title
    When the consumer fires { repos { worktrees { issue { number state title } } } }
    Then each worktree carries a nullable issue object
    And when issue is non-null it has number, state, and title fields
    And no GraphQL errors are present

  @integration
  Scenario: repos returns empty list when no repos configured
    Given the daemon has no repos configured
    When the consumer fires { repos { id slug } }
    Then repos is an empty list
    And no GraphQL errors are present

  @integration
  Scenario: repos query round-trip latency is within interactive budget
    When the consumer fires { repos { worktrees { branch } } } against a local daemon
    Then the response arrives in under 50ms on P95
