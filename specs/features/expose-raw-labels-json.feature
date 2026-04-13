Feature: Expose raw labels in orchard --json output
  As a script consuming `orchard --json`
  I want access to the full label array on issues and PRs
  So that I can filter by any label, not just the computed phase

  Background:
    Given labels are already fetched via GraphQL and stored in `IssueInfo.labels` and `PrState.labels`
    And `phase` is derived from labels — the existing `phase` field must remain unchanged

  # ===================================================================
  # JsonIssue — labels field added
  # ===================================================================

  @unit
  Scenario: JsonIssue includes labels array
    Given an `IssueInfo` with labels `["bug", "in-progress", "priority-high"]`
    When it is converted to `JsonIssue` and serialized
    Then the JSON contains `"labels": ["bug", "in-progress", "priority-high"]`
    And `"phase"` is still present as `"in-progress"`

  @unit
  Scenario: JsonIssue labels is empty array when issue has no labels
    Given an `IssueInfo` with empty labels
    When it is converted to `JsonIssue` and serialized
    Then the JSON contains `"labels": []`
    And `"phase"` is `null`

  # ===================================================================
  # JsonPr — labels field added
  # ===================================================================

  @unit
  Scenario: JsonPr includes labels array
    Given a `PrState` with labels `["enhancement", "pr-ready"]`
    When it is converted to `JsonPr` and serialized
    Then the JSON contains `"labels": ["enhancement", "pr-ready"]`
    And `"phase"` is still present as `"pr-ready"`

  @unit
  Scenario: JsonPr labels is empty array when PR has no labels
    Given a `PrState` with empty labels
    When it is converted to `JsonPr` and serialized
    Then the JSON contains `"labels": []`
    And `"phase"` is `null`

  @unit
  Scenario: JsonPr labels preserves original order from GitHub
    Given a `PrState` with labels `["z-label", "a-label", "m-label"]`
    When it is converted to `JsonPr` and serialized
    Then the JSON `"labels"` array preserves order: `["z-label", "a-label", "m-label"]`

  # ===================================================================
  # Backward compatibility — phase unchanged
  # ===================================================================

  @unit
  Scenario: Adding labels field does not change phase computation
    Given a `PrState` with labels `["blocked", "in-progress"]`
    When it is converted to `JsonPr` and serialized
    Then `"phase"` is `"blocked"` (priority wins)
    And `"labels"` contains both `"blocked"` and `"in-progress"`

  # ===================================================================
  # Integration — orchard --json binary output
  # ===================================================================

  @integration
  Scenario: orchard --json includes labels on issue objects
    Given a fixture cache with issue #47 having labels `["in-progress", "enhancement"]`
    When `orchard --json` is run against the fixture
    Then the worktree's `issue.labels` is `["in-progress", "enhancement"]`
    And `issue.phase` is `"in-progress"`

  @integration
  Scenario: orchard --json includes labels on PR objects
    Given a fixture cache with PR #55 having labels `["pr-ready", "needs-review"]`
    When `orchard --json` is run against the fixture
    Then the worktree's `pr.labels` is `["pr-ready", "needs-review"]`
    And `pr.phase` is `"pr-ready"`
