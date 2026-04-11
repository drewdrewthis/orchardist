Feature: Phase field on PRs and issues in orchard --json
  As a script consuming `orchard --json`
  I want a first-class `phase` field on each issue and PR
  So that I can filter, sort, and route work by workflow phase without re-parsing label lists

  Background:
    Given the `/gh-tag` skill defines 8 mutually-exclusive phase labels:
      | label         |
      | investigating |
      | needs-plan    |
      | needs-repro   |
      | planned       |
      | in-progress   |
      | in-ai-review  |
      | pr-ready      |
      | blocked       |
    And these labels are mirrored in a hardcoded `PHASE_LABELS` constant in `crates/orchard/src/derive.rs`
    And `phase` is computed from the `labels` array by a pure function `phase_from_labels`
    And when multiple phase labels are present, resolution follows a priority order:
      | rank | label         |
      | 1    | blocked       |
      | 2    | in-ai-review  |
      | 3    | pr-ready      |
      | 4    | in-progress   |
      | 5    | needs-repro   |
      | 6    | needs-plan    |
      | 7    | investigating |
      | 8    | planned       |

  # ===================================================================
  # Parser — pure function `phase_from_labels(&[String]) -> Option<&'static str>`
  # ===================================================================

  @unit
  Scenario: phase_from_labels returns None for an empty label list
    When `phase_from_labels(&[])` is called
    Then it returns `None`

  @unit
  Scenario: phase_from_labels returns None when no phase labels are present
    When `phase_from_labels(&["bug", "enhancement", "good-first-issue"])` is called
    Then it returns `None`

  @unit
  Scenario: phase_from_labels returns the matching phase when one phase label is present
    When `phase_from_labels(&["in-progress"])` is called
    Then it returns `Some("in-progress")`

  @unit
  Scenario: phase_from_labels returns the phase when mixed with unrelated labels
    When `phase_from_labels(&["bug", "planned", "priority-high"])` is called
    Then it returns `Some("planned")`

  @unit
  Scenario: phase_from_labels returns the higher-priority phase for two phase labels
    When `phase_from_labels(&["planned", "in-progress"])` is called
    Then it returns `Some("in-progress")`

  @unit
  Scenario: phase_from_labels returns blocked over in-progress
    When `phase_from_labels(&["in-progress", "blocked"])` is called
    Then it returns `Some("blocked")`

  @unit
  Scenario: phase_from_labels returns blocked over in-ai-review
    When `phase_from_labels(&["in-ai-review", "blocked"])` is called
    Then it returns `Some("blocked")`

  @unit
  Scenario: phase_from_labels resolves three simultaneous phase labels by priority
    When `phase_from_labels(&["investigating", "needs-plan", "blocked"])` is called
    Then it returns `Some("blocked")`

  @unit
  Scenario: phase_from_labels returns in-ai-review over pr-ready
    When `phase_from_labels(&["pr-ready", "in-ai-review"])` is called
    Then it returns `Some("in-ai-review")`

  @unit
  Scenario Outline: phase_from_labels recognizes every known phase label in isolation
    When `phase_from_labels(&[<label>])` is called
    Then it returns `Some(<label>)`

    Examples:
      | label            |
      | "investigating"  |
      | "needs-plan"     |
      | "needs-repro"    |
      | "planned"        |
      | "in-progress"    |
      | "in-ai-review"   |
      | "pr-ready"       |
      | "blocked"        |

  @unit
  Scenario: phase_from_labels ignores unknown labels
    When `phase_from_labels(&["wontfix", "duplicate", "question"])` is called
    Then it returns `None`

  @unit
  Scenario: phase_from_labels requires exact lowercase match
    When `phase_from_labels(&["In-Progress"])` is called
    Then it returns `None`

  # ===================================================================
  # CachedPr migration — labels field with serde default
  # ===================================================================

  @unit
  Scenario: Deserializing a pre-upgrade CachedPr without labels key yields empty vec
    Given a JSON string representing a CachedPr without a `labels` key:
      """
      {
        "number": 55,
        "branch": "issue/example",
        "linked_issue": null,
        "state": "open",
        "review_decision": null,
        "checks_state": null,
        "has_conflicts": false,
        "unresolved_threads": 0
      }
      """
    When the string is deserialized with `serde_json::from_str::<CachedPr>`
    Then deserialization succeeds
    And the resulting `labels` field is an empty vector

  @unit
  Scenario: Deserializing a CachedPr with labels key preserves the labels
    Given a JSON string representing a CachedPr with `labels: ["in-progress", "bug"]`
    When the string is deserialized with `serde_json::from_str::<CachedPr>`
    Then deserialization succeeds
    And the resulting `labels` field equals `["in-progress", "bug"]`

  @unit
  Scenario: CachedIssue labels field remains required (no migration regression)
    Given a JSON string representing a CachedIssue with `labels: ["enhancement"]`
    When the string is deserialized with `serde_json::from_str::<CachedIssue>`
    Then deserialization succeeds
    And the resulting `labels` field equals `["enhancement"]`

  # ===================================================================
  # build_state — labels reach the state layer from caches
  # ===================================================================

  @unit
  Scenario: build_state threads PR labels from cache into PrState
    Given an in-memory PRs cache with PR #55 having labels `["planned"]`
    And matching worktree and issues caches
    When `build_state` is invoked
    Then the resulting `PrState` for PR #55 has `labels` equal to `["planned"]`

  @unit
  Scenario: build_state threads issue labels from cache into IssueInfo
    Given an in-memory issues cache with issue #47 having labels `["in-progress", "enhancement"]`
    And matching worktree and PRs caches
    When `build_state` is invoked
    Then the resulting `IssueInfo` for issue #47 has `labels` equal to `["in-progress", "enhancement"]`

  @unit
  Scenario: build_state emits empty labels vec when a PR has no labels
    Given an in-memory PRs cache with PR #99 having no labels
    When `build_state` is invoked
    Then the resulting `PrState` for PR #99 has `labels` equal to `vec![]`

  # ===================================================================
  # JsonIssue / JsonPr — serialized shape
  # ===================================================================

  @unit
  Scenario: JsonIssue serializes phase as null when no phase label is set
    Given an `IssueInfo` with labels `["bug"]`
    When it is converted to `JsonIssue` and serialized with serde_json
    Then the serialized object contains `"phase": null`

  @unit
  Scenario: JsonIssue serializes phase as the matched label
    Given an `IssueInfo` with labels `["in-progress", "bug"]`
    When it is converted to `JsonIssue` and serialized with serde_json
    Then the serialized object contains `"phase": "in-progress"`

  @unit
  Scenario: JsonPr serializes phase as null when no phase label is set
    Given a `PrState` with labels `["enhancement"]`
    When it is converted to `JsonPr` and serialized with serde_json
    Then the serialized object contains `"phase": null`

  @unit
  Scenario: JsonPr serializes phase as the matched label
    Given a `PrState` with labels `["pr-ready"]`
    When it is converted to `JsonPr` and serialized with serde_json
    Then the serialized object contains `"phase": "pr-ready"`

  @unit
  Scenario: JsonPr resolves multi-phase labels by priority
    Given a `PrState` with labels `["in-progress", "blocked"]`
    When it is converted to `JsonPr` and serialized with serde_json
    Then the serialized object contains `"phase": "blocked"`

  @unit
  Scenario: JsonIssue phase key is present even when value is null
    Given an `IssueInfo` with empty labels
    When it is serialized with serde_json
    Then the serialized object contains the key `"phase"` with value `null`
    And the key is always present (not omitted via skip_serializing_if)

  @unit
  Scenario: JsonPr phase key is present even when value is null
    Given a `PrState` with empty labels
    When it is serialized with serde_json
    Then the serialized object contains the key `"phase"` with value `null`

  @unit
  Scenario: JsonIssue preserves existing fields when phase is added
    Given an `IssueInfo` numbered 219, titled "phase field", state "open", labels `["planned"]`
    When it is serialized with serde_json
    Then the output contains `"phase": "planned"`
    And the output contains `"number": 219`
    And the output contains `"title": "phase field"`
    And the output contains `"state": "open"`

  @unit
  Scenario: JsonPr preserves existing fields when phase is added
    Given a `PrState` with number 220, branch "issue219/phase-field", labels `["in-ai-review"]`
    When it is serialized with serde_json
    Then the output contains `"phase": "in-ai-review"`
    And the output contains `"number": 220`
    And the output contains `"branch": "issue219/phase-field"`
    And the output contains keys `reviewDecision`, `checksState`, `hasConflicts`, `unresolvedThreads`

  # ===================================================================
  # End-to-end integration — orchard --json binary output
  # ===================================================================
  # These scenarios share a single fixture cache with many worktrees
  # and assert each phase variant in one binary invocation per scenario
  # group. Harness may bootstrap one cache per feature run.

  @integration
  Scenario: orchard --json exposes phase on an issue with a single phase label
    Given a fixture cache with issue #47 having labels `["in-progress"]`
    When `orchard --json` is run against the fixture
    Then the output contains a worktree whose `issue.phase` is `"in-progress"`
    And `issue.state` and `issue.title` are unchanged from the input cache

  @integration
  Scenario: orchard --json exposes phase on a PR with a single phase label
    Given a fixture cache with PR #55 having labels `["pr-ready"]`
    When `orchard --json` is run against the fixture
    Then the output contains a worktree whose `pr.phase` is `"pr-ready"`

  @integration
  Scenario: orchard --json emits phase null when an issue has no phase labels
    Given a fixture cache with issue #10 having labels `["bug"]`
    When `orchard --json` is run against the fixture
    Then the output contains a worktree whose `issue.phase` is `null`

  @integration
  Scenario: orchard --json resolves a multi-phase PR by priority
    Given a fixture cache with PR #60 having labels `["in-progress", "blocked"]`
    When `orchard --json` is run against the fixture
    Then the output contains a worktree whose `pr.phase` is `"blocked"`

  @integration
  Scenario: orchard --json exposes phase on a PR with no linked issue
    Given a fixture cache with PR #70 having `linked_issue: null` and labels `["in-ai-review"]`
    When `orchard --json` is run against the fixture
    Then the output contains a worktree whose `pr.phase` is `"in-ai-review"`

  @integration
  Scenario: orchard --json allows issue and PR on the same worktree to have different phases
    Given a fixture cache where issue #47 has labels `["planned"]`
    And PR #55 on the same branch has labels `["in-ai-review"]`
    When `orchard --json` is run against the fixture
    Then `issue.phase` on that worktree is `"planned"`
    And `pr.phase` on that worktree is `"in-ai-review"`
