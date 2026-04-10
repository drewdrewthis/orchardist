Feature: CI failure details and labels in core state model
  As a developer using orchard
  I want to see which CI checks failed and what labels are on issues/PRs
  So that I can decide whether to investigate, ignore, or prioritize at a glance

  Background:
    Given a repo with owner "acme" and name "webapp"
    And a PR #42 on branch "feat/auth" with checks in "FAILURE" state

  # ===================================================================
  # Data model — FailedCheck struct and PrState enrichment
  # ===================================================================

  @unit
  Scenario: PrState includes failing check details
    Given PR #42 has the following check runs:
      | name                     | conclusion     |
      | e2e-tests                | FAILURE        |
      | unit-tests               | SUCCESS        |
      | lint                     | SUCCESS        |
      | check-approval-or-label  | FAILURE        |
    When the state is built
    Then PrState.failing_checks contains:
      | name       | conclusion |
      | e2e-tests  | FAILURE    |
    And PrState.failing_checks does NOT contain "check-approval-or-label"
    And PrState.checks_state is "failing"

  @unit
  Scenario: Failing checks list is empty when all checks pass
    Given PR #42 has the following check runs:
      | name        | conclusion |
      | e2e-tests   | SUCCESS    |
      | unit-tests  | SUCCESS    |
    When the state is built
    Then PrState.failing_checks is empty
    And PrState.checks_state is "passing"

  @unit
  Scenario: Failing checks list is empty when checks are pending
    Given PR #42 has no completed check runs yet
    When the state is built
    Then PrState.failing_checks is empty
    And PrState.checks_state is "pending"

  @unit
  Scenario: Multiple failing checks are all captured
    Given PR #42 has the following check runs:
      | name        | conclusion     |
      | e2e-tests   | FAILURE        |
      | lint         | FAILURE        |
      | unit-tests  | SUCCESS        |
    When the state is built
    Then PrState.failing_checks contains:
      | name       | conclusion |
      | e2e-tests  | FAILURE    |
      | lint        | FAILURE    |

  # ===================================================================
  # Conclusion classification — exhaustive mapping
  # ===================================================================
  # GitHub check conclusions: SUCCESS, FAILURE, CANCELLED, TIMED_OUT,
  # ACTION_REQUIRED, STALE, NEUTRAL, SKIPPED, STARTUP_FAILURE
  #
  # Classification:
  #   Failing  = FAILURE | TIMED_OUT | ACTION_REQUIRED | STARTUP_FAILURE
  #   Passing  = SUCCESS | NEUTRAL | SKIPPED
  #   Ignored  = CANCELLED | STALE (transient, not actionable)

  @unit
  Scenario: TIMED_OUT conclusion is treated as failing
    Given PR #42 has the following check runs:
      | name       | conclusion |
      | e2e-tests  | TIMED_OUT  |
    When the state is built
    Then PrState.failing_checks contains "e2e-tests" with conclusion "TIMED_OUT"

  @unit
  Scenario: ACTION_REQUIRED conclusion is treated as failing
    Given PR #42 has the following check runs:
      | name         | conclusion      |
      | deploy-gate  | ACTION_REQUIRED |
    When the state is built
    Then PrState.failing_checks contains "deploy-gate" with conclusion "ACTION_REQUIRED"

  @unit
  Scenario: CANCELLED and STALE conclusions are not treated as failing
    Given PR #42 has the following check runs:
      | name        | conclusion |
      | e2e-tests   | CANCELLED  |
      | lint        | STALE      |
      | unit-tests  | SUCCESS    |
    When the state is built
    Then PrState.failing_checks is empty

  @unit
  Scenario: NEUTRAL and SKIPPED conclusions are not treated as failing
    Given PR #42 has the following check runs:
      | name        | conclusion |
      | optional    | NEUTRAL    |
      | skipped-job | SKIPPED    |
    When the state is built
    Then PrState.failing_checks is empty

  # ===================================================================
  # Non-blocking check exclusion — applied at build_state, not parse
  # ===================================================================
  # Non-blocking checks are filtered when building PrState from CachedPr,
  # NOT at GraphQL parse time. This preserves all data in cache so that
  # exclusion rules can change without cache invalidation.

  @unit
  Scenario: check-approval-or-label is excluded from failing checks
    Given PR #42 has the following check runs:
      | name                     | conclusion |
      | check-approval-or-label  | FAILURE    |
      | unit-tests               | SUCCESS    |
    When the state is built
    Then PrState.failing_checks is empty

  @unit
  Scenario: CachedPr retains excluded checks for future configurability
    Given PR #42 has the following check runs:
      | name                     | conclusion |
      | check-approval-or-label  | FAILURE    |
      | e2e-tests                | FAILURE    |
    When PRs are parsed from the GraphQL response
    Then CachedPr.failing_checks contains both "check-approval-or-label" and "e2e-tests"
    But when the state is built, PrState.failing_checks contains only "e2e-tests"

  # ===================================================================
  # GraphQL fetching — CheckRun AND StatusContext union types
  # ===================================================================
  # statusCheckRollup.contexts is a union of CheckRun and StatusContext.
  # Both must be handled to avoid silently dropping legacy CI results.

  @unit
  Scenario: GraphQL query fetches CheckRun details
    Given the GraphQL response includes statusCheckRollup.contexts with:
      | __typename   | name       | conclusion |
      | CheckRun     | e2e-tests  | FAILURE    |
      | CheckRun     | lint       | SUCCESS    |
    When PRs are parsed from the GraphQL response
    Then CachedPr.failing_checks contains:
      | name      | conclusion |
      | e2e-tests | FAILURE    |

  @unit
  Scenario: GraphQL query handles StatusContext (legacy commit status)
    Given the GraphQL response includes statusCheckRollup.contexts with:
      | __typename    | context    | state   |
      | StatusContext | ci/travis  | FAILURE |
      | StatusContext | ci/circle  | SUCCESS |
    When PRs are parsed from the GraphQL response
    Then CachedPr.failing_checks contains:
      | name      | conclusion |
      | ci/travis | FAILURE    |

  @unit
  Scenario: Mixed CheckRun and StatusContext are both captured
    Given the GraphQL response includes statusCheckRollup.contexts with:
      | __typename    | name/context | conclusion/state |
      | CheckRun      | e2e-tests    | FAILURE          |
      | StatusContext | ci/travis    | FAILURE          |
    When PRs are parsed from the GraphQL response
    Then CachedPr.failing_checks contains both "e2e-tests" and "ci/travis"

  @unit
  Scenario: Warn when pagination truncates check results
    Given the GraphQL response has statusCheckRollup.contexts with pageInfo.hasNextPage = true
    When PRs are parsed from the GraphQL response
    Then a warning is logged: "PR #42 has more than 100 checks; some may be missing"

  # ===================================================================
  # Cache layer — serialization round-trip
  # ===================================================================

  @unit
  Scenario: Failing checks survive cache serialization round-trip
    Given a CachedPr with failing_checks:
      | name       | conclusion |
      | e2e-tests  | FAILURE    |
    When the CachedPr is serialized to JSON and deserialized back
    Then the deserialized failing_checks matches the original

  @unit
  Scenario: Existing cache files without failing_checks deserialize with empty vec
    Given a cached PR JSON without a "failingChecks" field
    When the JSON is deserialized to CachedPr
    Then CachedPr.failing_checks is an empty vec

  # ===================================================================
  # JSON output — orchard --json (version bump to 5)
  # ===================================================================
  # Adding failingChecks to JsonPr is an additive schema change.
  # Bump version from 4 to 5 per the module's forward-compat contract.

  @unit
  Scenario: orchard --json schema version is 5
    When the state is serialized to JSON output
    Then the top-level "version" field is 5

  @unit
  Scenario: orchard --json PR object includes failingChecks
    Given a WorktreeState with a PR that has failing checks:
      | name       | conclusion |
      | e2e-tests  | FAILURE    |
    When the state is serialized to JSON output
    Then the JSON PR object contains:
      """json
      "failingChecks": [
        { "name": "e2e-tests", "conclusion": "FAILURE" }
      ]
      """

  @unit
  Scenario: orchard --json PR object has empty failingChecks when passing
    Given a WorktreeState with a PR that has no failing checks
    When the state is serialized to JSON output
    Then the JSON PR object contains:
      """json
      "failingChecks": []
      """

  # ===================================================================
  # TUI — failing check names in status row
  # ===================================================================

  @unit
  Scenario: TUI status row shows failing check names
    Given a WorktreeRow with PR failing checks:
      | name       |
      | e2e-tests  |
      | lint       |
    When pr_status_text is rendered
    Then the text contains "failing: e2e-tests, lint"

  @unit
  Scenario: TUI status row shows single failing check name
    Given a WorktreeRow with PR failing checks:
      | name      |
      | e2e-tests |
    When pr_status_text is rendered
    Then the text contains "failing: e2e-tests"

  @unit
  Scenario: TUI status row truncates long check lists
    Given a WorktreeRow with 5 failing checks
    When pr_status_text is rendered
    Then the text shows at most 3 check names followed by "+2 more"

  @unit
  Scenario: TUI status row falls back to generic failing when checks list is empty
    Given a WorktreeRow with checks_state "failing" but empty failing_checks
    When pr_status_text is rendered
    Then the text contains "failing" without specific check names

  # ===================================================================
  # Watch system — CiFailed event enrichment
  # ===================================================================
  # Field name: failing_checks (consistent with PrState)

  @unit
  Scenario: CiFailed event includes which checks failed
    Given old state has PR #42 with checks_state "passing"
    And new state has PR #42 with checks_state "failing" and failing_checks:
      | name       | conclusion |
      | e2e-tests  | FAILURE    |
    When the watch diff is computed
    Then a CiFailed event is emitted with failing_checks:
      | name       | conclusion |
      | e2e-tests  | FAILURE    |

  @unit
  Scenario: CiPassed event does not include failing checks
    Given old state has PR #42 with checks_state "failing"
    And new state has PR #42 with checks_state "passing" and no failing checks
    When the watch diff is computed
    Then a CiPassed event is emitted without failing_checks

  # ===================================================================
  # PR labels — flow through state model
  # ===================================================================
  # PR labels are fetched via GraphQL and stored in CachedPr, PrState, JsonPr.

  @unit
  Scenario: PrState includes PR labels
    Given PR #42 has labels: "low-risk-change", "P0"
    When the state is built
    Then PrState.labels contains "low-risk-change" and "P0"

  @unit
  Scenario: PrState has empty labels when PR has none
    Given PR #42 has no labels
    When the state is built
    Then PrState.labels is empty

  @unit
  Scenario: GraphQL query fetches PR labels
    Given the GraphQL response includes labels.nodes with:
      | name              |
      | low-risk-change   |
      | needs-plan        |
    When PRs are parsed from the GraphQL response
    Then CachedPr.labels contains "low-risk-change" and "needs-plan"

  @unit
  Scenario: CachedPr without labels field deserializes with empty vec
    Given a cached PR JSON without a "labels" field
    When the JSON is deserialized to CachedPr
    Then CachedPr.labels is an empty vec

  @unit
  Scenario: orchard --json PR object includes labels
    Given a WorktreeState with a PR that has labels: "low-risk-change"
    When the state is serialized to JSON output
    Then the JSON PR object contains:
      """json
      "labels": ["low-risk-change"]
      """

  # ===================================================================
  # Issue labels — flow through state model
  # ===================================================================
  # Issue labels are already in CachedIssue. Just need to flow to IssueInfo and JSON.

  @unit
  Scenario: IssueInfo includes issue labels
    Given issue #10 has labels: "bug", "P1"
    When the state is built
    Then IssueInfo.labels contains "bug" and "P1"

  @unit
  Scenario: IssueInfo has empty labels when issue has none
    Given issue #10 has no labels
    When the state is built
    Then IssueInfo.labels is empty

  @unit
  Scenario: orchard --json issue object includes labels
    Given a WorktreeState with an issue that has labels: "bug", "priority:high"
    When the state is serialized to JSON output
    Then the JSON issue object contains:
      """json
      "labels": ["bug", "priority:high"]
      """
