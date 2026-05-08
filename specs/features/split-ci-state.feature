Feature: Split CI state into code-failing vs gate-failing
  As an orchardist triaging PRs
  I want CI checks classified into "code" vs "gate" buckets with independent rollups
  So that I can tell "ready, just needs approval" apart from "broken, needs a fix" without drilling into `gh pr checks` for every red PR

  # Issue #218 — data-layer feature. Splits PrState.checks_state (single union)
  # into PrState.ci_code_state + PrState.ci_gate_state, backed by a structured
  # CiChecks { code, gate } breakdown. Gate patterns are configured in
  # GlobalConfig with sensible defaults. Legacy `checks_state` field stays
  # populated for one release as a deprecation path.
  #
  # IMPORTANT coupling: `resolve_pr_status` (types.rs:144) and the display-group
  # helpers `is_needs_attention` / `is_ready_to_merge` (derive.rs:435-447)
  # currently branch on the union `checks_state`. Without updating them, a PR
  # that is code-green but gate-blocked would cascade into the `NeedsAttention`
  # display group — the exact regression this feature is meant to fix. See the
  # @integration "display-group helpers prefer split state" scenario below.
  #
  # The `ignored` bucket from the original issue is SCOPED OUT of v1. No
  # `ignored` field is emitted; it is reserved for a follow-up issue that
  # reads PR comments/labels to reclassify pre-existing main failures.

  Background:
    Given the global config file is "~/.orchard/config.json"
    And the default gate patterns are:
      | pattern                 | match type  |
      | check-approval-or-label | exact       |
      | Mintlify Deployment     | exact       |
      | license/*               | glob        |
    And exact matches are case-insensitive
    And glob matches use `globset` semantics where `*` does NOT cross `/`

  # ===================================================================
  # End-to-end — real-world triage scenarios through `orchard --json`
  # ===================================================================

  @e2e
  Scenario: Orchardist identifies a PR that is code-green but gate-blocked
    Given a PR whose GraphQL statusCheckRollup contexts are:
      | name                    | conclusion |
      | test-unit               | SUCCESS    |
      | test-integration        | SUCCESS    |
      | lint                    | SUCCESS    |
      | check-approval-or-label | FAILURE    |
    When the orchardist runs `orchard --json`
    Then the PR's `ciCodeState` is "passing"
    And the PR's `ciGateState` is "blocked"
    And the PR's `ciChecks.code` contains "test-unit", "test-integration", and "lint" each with state "passing"
    And the PR's `ciChecks.gate` contains "check-approval-or-label" with state "failing"
    And the PR's `displayGroup` is NOT "needsAttention"
    And the orchardist can filter with `jq` on `ciCodeState == "passing" and ciGateState == "blocked"` to surface "ready for Slack review request" PRs

  @e2e
  Scenario: Orchardist distinguishes a gate still running from a gate that failed
    Given a PR whose GraphQL statusCheckRollup contexts are:
      | name                    | conclusion |
      | test-unit               | SUCCESS    |
      | Mintlify Deployment     | PENDING    |
    When the orchardist runs `orchard --json`
    Then the PR's `ciCodeState` is "passing"
    And the PR's `ciGateState` is "pending"
    And the PR is NOT surfaced by the "ready for Slack review request" filter (`ciCodeState == "passing" and ciGateState == "blocked"`)

  # ===================================================================
  # Unit — GraphQL query shape
  # ===================================================================

  @unit
  Scenario: GraphQL query fetches up to 100 per-check contexts with inline fragments on CheckRun and StatusContext
    Given the compiled PR GraphQL query string
    Then the query contains `statusCheckRollup` with `contexts(first: 100)`
    And the query contains an inline fragment `... on CheckRun` selecting `name`, `conclusion`, and `status`
    And the query contains an inline fragment `... on StatusContext` selecting `context` and `state`

  # ===================================================================
  # Integration — Per-check parsing and pagination
  # ===================================================================

  @integration
  Scenario: Parsing normalizes CheckRun and StatusContext into a uniform CheckInfo
    Given the GraphQL response contains a `CheckRun` node with name "test-unit" and conclusion "SUCCESS"
    And the response contains a `StatusContext` node with context "travis-ci" and state "SUCCESS"
    When the contexts are parsed
    Then both are normalized into `CheckInfo { name: ..., state: "passing" }`
    And `CheckRun.name` is used as the check name
    And `StatusContext.context` is used as the check name

  @integration
  Scenario: PRs with more than 100 checks are truncated and a warning is logged
    Given a PR has 120 statusCheckRollup contexts
    When the cache fetcher parses the response
    Then the first 100 contexts are classified and rolled up
    And a warning is logged with the PR number and the truncation count
    And the rollup reflects only the fetched subset (documented limitation)

  # ===================================================================
  # Unit — JSON output contract (JsonPr struct shape)
  # ===================================================================

  @unit
  Scenario: JsonPr struct declares the new ci state fields alongside the legacy field
    Then `JsonPr` defines (with `#[serde(rename_all = "camelCase")]`):
      | rust field       | json key      | type               |
      | ci_code_state    | ciCodeState   | Option<String>     |
      | ci_gate_state    | ciGateState   | Option<String>     |
      | ci_checks        | ciChecks      | CiChecks           |
      | checks_state     | checksState   | Option<String>     |
    And each entry in `ciChecks.code` and `ciChecks.gate` serializes to an object with keys "name" and "state"
    And `JsonPr` does NOT emit an `ignored` field (reserved for a follow-up issue)

  @integration
  Scenario: Legacy checksState reflects code state only when gate is clear or absent
    Given a PR with ciCodeState "passing" and ciGateState "cleared"
    When `orchard --json` is rendered
    Then the legacy `checksState` field is "passing"

  @integration
  Scenario: Legacy checksState reflects code failures
    Given a PR with ciCodeState "failing" and ciGateState "cleared"
    When `orchard --json` is rendered
    Then the legacy `checksState` field is "failing"

  @integration
  Scenario: Legacy checksState does NOT flip to failing when only the gate is blocked
    Given a PR with ciCodeState "passing" and ciGateState "blocked"
    When `orchard --json` is rendered
    Then the legacy `checksState` field is "passing"
    # Rationale: preserves backward-compat for orchestrators that were filtering
    # out `checksState == "failing"` — a code-green gate-blocked PR should not
    # suddenly start being treated as broken by legacy consumers.

  @integration
  Scenario: Legacy checksState is null when the PR has zero checks in either bucket
    Given a PR with ciCodeState null and ciGateState null
    When `orchard --json` is rendered
    Then the legacy `checksState` field is null

  # ===================================================================
  # Integration — Display-group helpers and resolve_pr_status
  # ===================================================================

  @integration
  Scenario: `is_needs_attention` does NOT fire when code is green and only the gate is blocked
    Given a PR with ciCodeState "passing", ciGateState "blocked", and review_decision "APPROVED"
    When display groups are derived
    Then the PR is NOT classified as `NeedsAttention`
    And the PR is NOT classified as `ReadyToMerge` either (waiting on the gate)
    And `resolve_pr_status` returns a status OTHER than `Failing`

  @integration
  Scenario: `is_ready_to_merge` fires when code is green and gate is cleared
    Given a PR with ciCodeState "passing", ciGateState "cleared", review_decision "APPROVED", no conflicts, and zero unresolved threads
    When display groups are derived
    Then the PR is classified as `ReadyToMerge`

  @integration
  Scenario: `is_needs_attention` still fires when code is failing
    Given a PR with ciCodeState "failing" and ciGateState "cleared"
    When display groups are derived
    Then the PR is classified as `NeedsAttention`

  @integration
  Scenario: A docs-only PR with zero CI checks is NOT classified as NeedsAttention
    Given a PR with ciCodeState null and ciGateState null, review_decision "APPROVED", no conflicts, and zero unresolved threads
    When display groups are derived
    Then the PR is NOT classified as `NeedsAttention`
    And `resolve_pr_status` does NOT return `Failing`

  # ===================================================================
  # Integration — GlobalConfig gate patterns
  # ===================================================================

  @integration
  Scenario: Gate patterns are loaded from GlobalConfig with defaults when unset
    Given `~/.orchard/config.json` does not contain a `ci_gate_patterns` field
    When the global config is loaded
    Then `ci_gate_patterns` equals ["check-approval-or-label", "Mintlify Deployment", "license/*"]

  @integration
  Scenario: GlobalConfig serializes ci_gate_patterns in snake_case on disk
    Given a `GlobalConfig` with `ci_gate_patterns` set to ["custom-gate"]
    When the config is serialized to JSON
    Then the JSON contains the key `"ci_gate_patterns"` (snake_case, matching existing GlobalConfig fields like `terminal_app` and `tmux_sessions`)

  @integration
  Scenario: Orchardist adds a custom gate pattern via GlobalConfig
    Given `~/.orchard/config.json` contains:
      """json
      {
        "ci_gate_patterns": [
          "check-approval-or-label",
          "Mintlify Deployment",
          "license/*",
          "security-review"
        ]
      }
      """
    And a PR has a check named "security-review" with conclusion FAILURE
    And all other checks are code checks and passing
    When classification runs
    Then "security-review" is classified as a gate check
    And the PR's ciCodeState is "passing"
    And the PR's ciGateState is "blocked"

  # ===================================================================
  # Integration — Cache migration
  # ===================================================================

  @integration
  Scenario: Old cache files without the new fields deserialize successfully with defaults
    Given a cache file on disk written by orchard 0.6.0 where `CachedPr` lacks `ci_code_state`, `ci_gate_state`, and `ci_checks`
    When orchard 0.7.0 reads the cache file
    Then deserialization succeeds
    And `ci_code_state` is null
    And `ci_gate_state` is null
    And `ci_checks` is `{ "code": [], "gate": [] }`
    And the next refresh populates all three fields from the GraphQL response

  @unit
  Scenario: The watch/diff layer fires a transition when ci_code_state changes
    Given two consecutive `OrchardState` snapshots where the same PR transitions `ci_code_state` from "passing" to "failing"
    When `watch::diff::diff_snapshots` runs
    Then the resulting diff includes a PR transition event for that PR
    And existing `checks_state` transition events continue to fire for one release (documented deprecation)

  # ===================================================================
  # Unit — classification logic per check
  # ===================================================================

  @unit
  Scenario: A check name matching an exact gate pattern is classified as gate
    Given the gate patterns are ["check-approval-or-label"]
    And a check is named "check-approval-or-label"
    When the check is classified
    Then it is placed in the gate bucket

  @unit
  Scenario: Exact gate-pattern match is case-insensitive
    Given the gate patterns are ["Mintlify Deployment"]
    And a check is named "mintlify deployment"
    When the check is classified
    Then it is placed in the gate bucket

  @unit
  Scenario: A check name matching a shallow glob gate pattern is classified as gate
    Given the gate patterns are ["license/*"]
    And a check is named "license/cla"
    When the check is classified
    Then it is placed in the gate bucket

  @unit
  Scenario: A glob gate pattern with single `*` does NOT match across slashes
    Given the gate patterns are ["license/*"]
    And a check is named "license/cla/v2"
    When the check is classified
    Then it is placed in the code bucket
    # Rationale: using `globset` defaults — `*` does not cross `/`. Users wanting
    # recursive match should write `license/**`.

  @unit
  Scenario: A glob gate pattern with `**` matches recursively across slashes
    Given the gate patterns are ["license/**"]
    And a check is named "license/cla/v2"
    When the check is classified
    Then it is placed in the gate bucket

  @unit
  Scenario: A check name matching no gate pattern is classified as code
    Given the gate patterns are ["check-approval-or-label", "license/*"]
    And a check is named "test-unit"
    When the check is classified
    Then it is placed in the code bucket

  # ===================================================================
  # Unit — ci_code_state rollup branches
  # ===================================================================

  @unit
  Scenario: ci_code_state is "passing" when all code checks pass
    Given code checks have states ["passing", "passing", "passing"]
    When ci_code_state is rolled up
    Then ci_code_state is "passing"

  @unit
  Scenario: ci_code_state is "failing" when any code check fails
    Given code checks have states ["passing", "failing", "passing"]
    When ci_code_state is rolled up
    Then ci_code_state is "failing"

  @unit
  Scenario: ci_code_state is "failing" when a code check fails and another is pending
    Given code checks have states ["failing", "pending"]
    When ci_code_state is rolled up
    Then ci_code_state is "failing"

  @unit
  Scenario: ci_code_state is "pending" when there are no failures and at least one pending check
    Given code checks have states ["passing", "pending"]
    When ci_code_state is rolled up
    Then ci_code_state is "pending"

  @unit
  Scenario: ci_code_state is null when there are zero code checks
    Given there are no code checks
    When ci_code_state is rolled up
    Then ci_code_state is null

  # ===================================================================
  # Unit — ci_gate_state rollup branches (four-valued: cleared/blocked/pending/null)
  # ===================================================================

  @unit
  Scenario: ci_gate_state is "cleared" when all gate checks pass
    Given gate checks have states ["passing", "passing"]
    When ci_gate_state is rolled up
    Then ci_gate_state is "cleared"

  @unit
  Scenario: ci_gate_state is "blocked" when any gate check fails
    Given gate checks have states ["passing", "failing"]
    When ci_gate_state is rolled up
    Then ci_gate_state is "blocked"

  @unit
  Scenario: ci_gate_state is "blocked" when a gate check fails even if another is pending
    Given gate checks have states ["failing", "pending"]
    When ci_gate_state is rolled up
    Then ci_gate_state is "blocked"
    # Rationale: hard failure dominates pending. A FAILURE on `check-approval-or-label`
    # is an explicit "no" from a human; a pending Mintlify preview is just slow.

  @unit
  Scenario: ci_gate_state is "pending" when no gate check fails but at least one is pending
    Given gate checks have states ["passing", "pending"]
    When ci_gate_state is rolled up
    Then ci_gate_state is "pending"
    # Rationale: distinguishes "Mintlify preview still building" from
    # "check-approval-or-label failed". Critical for the feature's triage value —
    # otherwise every mid-flight preview looks like a gate failure.

  @unit
  Scenario: ci_gate_state is null when there are zero gate checks
    Given there are no gate checks
    When ci_gate_state is rolled up
    Then ci_gate_state is null

  # ===================================================================
  # Unit — GraphQL conclusion / state mapping
  # ===================================================================

  @unit
  Scenario Outline: CheckRun conclusions map to check states
    Given a CheckRun has conclusion "<conclusion>"
    When the conclusion is mapped to a check state
    Then the check state is <result>

    Examples:
      | conclusion      | result    |
      | SUCCESS         | "passing" |
      | NEUTRAL         | "passing" |
      | FAILURE         | "failing" |
      | TIMED_OUT       | "failing" |
      | ACTION_REQUIRED | "failing" |
      | SKIPPED         | omitted   |
      | CANCELLED       | omitted   |
      | STALE           | omitted   |
    # Rationales:
    # - NEUTRAL is GitHub's "opinionated pass" used by bots that ran but made
    #   no judgment. Treating it as passing avoids false negatives.
    # - ACTION_REQUIRED literally means "a human must act". On a gate pattern
    #   this should surface as blocked, not silently drop.
    # - SKIPPED/CANCELLED/STALE are omitted from rollup — they are neither a
    #   pass nor a fail signal and must not drag the rollup state.

  @unit
  Scenario: CheckRun with null conclusion (still running) maps to "pending"
    Given a CheckRun has status "IN_PROGRESS" and a null conclusion
    When the conclusion is mapped to a check state
    Then the check state is "pending"

  @unit
  Scenario Outline: StatusContext states map to check states
    Given a StatusContext has state "<state>"
    When the state is mapped to a check state
    Then the check state is <result>

    Examples:
      | state    | result    |
      | SUCCESS  | "passing" |
      | EXPECTED | "passing" |
      | FAILURE  | "failing" |
      | ERROR    | "failing" |
      | PENDING  | "pending" |

  # ===================================================================
  # Unit — PrState / CiChecks data model
  # ===================================================================

  @unit
  Scenario: PrState carries separate ci_code_state and ci_gate_state fields
    Then `PrState` defines the fields:
      | field            | type                |
      | ci_code_state    | Option<String>      |
      | ci_gate_state    | Option<String>      |
      | ci_checks        | CiChecks            |
      | checks_state     | Option<String>      |
    And the legacy `checks_state` field is marked `#[deprecated]` in rustdoc with a pointer to `ci_code_state`

  @unit
  Scenario: CiChecks groups classified checks into two buckets
    Then `CiChecks` defines the fields:
      | field | type             |
      | code  | Vec<CheckInfo>   |
      | gate  | Vec<CheckInfo>   |
    And `CiChecks` does NOT define an `ignored` field in v1
    And `CheckInfo` defines the fields:
      | field | type   |
      | name  | String |
      | state | String |
    And `CiChecks` and `CheckInfo` derive `Default`, `Serialize`, `Deserialize`, `Clone`, and `Debug`
    And all new fields on `CachedPr` and `PrState` are annotated `#[serde(default)]` so existing cache files deserialize without the new fields

  @unit
  Scenario: PrInfo carries ci_code_state and ci_gate_state fields for downstream consumers
    Then `PrInfo` defines:
      | field         | type           |
      | ci_code_state | Option<String> |
      | ci_gate_state | Option<String> |
    And existing `PrInfo.checks_state` and `PrInfo.checks_status` remain populated for one release as deprecated fields

  @integration
  Scenario: build_state copies ci_code_state, ci_gate_state, and ci_checks from CachedPr into PrInfo
    Given a `CachedPr` with ci_code_state "passing", ci_gate_state "blocked", and a non-empty `ci_checks.gate` vec
    When `build_state()` joins caches into `OrchardState`
    Then the resulting `PrInfo` has ci_code_state "passing"
    And the resulting `PrInfo` has ci_gate_state "blocked"
    And the resulting `PrState` has the same `ci_checks.gate` entries as the source `CachedPr`
