@integration
Feature: Daemon WorktreesList query — response shape for NewConversation modal
  As any daemon consumer (GUI NewConversation modal)
  I need the WorktreesList query to return a flat view of non-bare worktrees
  So that consumers can populate pickers without client-side git calls.

  Background:
    Given a daemon httptest.Server is running with a real git repo

  @integration
  Scenario: WorktreesList response shape — worktree fields present
    When the consumer fires { repos { id slug worktrees { id path branch bare host repo { id } } } }
    Then repos is a list where each entry has id, slug, and worktrees
    And each worktree has id, path, branch, bare, host, repo
    And no GraphQL errors are present

  @integration
  Scenario: WorktreesList includes bare field for client-side filtering
    When the WorktreesList query runs
    Then each worktree carries the bare boolean field
    And no GraphQL errors are present
