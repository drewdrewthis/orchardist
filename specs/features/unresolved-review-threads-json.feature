Feature: Expose unresolved review threads and recent review comments in orchard --json
  As an orchardist triaging PRs
  I want unresolved review threads (filtered to actionable ones) and the most recent top-level review comment surfaced in `orchard --json`
  So that I can report every blocker — CI, gate, conflicts, AND unaddressed author feedback — without drilling into `gh pr view` per PR

  # Issue #252 — data-layer feature. Corrects the existing `unresolvedThreads` semantics
  # (currently counts `isResolved != true`, must filter out `isOutdated == true`) and
  # adds three new camelCase fields on `JsonPr`:
  #   - `unresolvedReviewThreads` (replaces `unresolvedThreads`, with corrected semantics)
  #   - `lastReviewCommentAt`     (most recent top-level Review.submittedAt, NOT inline thread)
  #   - `lastReviewCommentAuthor` (same source)
  #   - `hasUnaddressedAuthorComment` (derived)
  #
  # Source data already fetched by `pr_graphql_query_per_branch()`:
  #   - `reviewThreads(first: 100) { nodes { isResolved } }` — MUST ALSO select `isOutdated`
  #   - `reviews(first: 20)        { nodes { author{login} state submittedAt } }`
  #   - `last_commit_pushed_at` — already on `Pr` (used for "subsequent push" detection)
  #
  # NAMING: the issue body writes the forward key as `unresolvedReviewThreads`.
  # Both keys are emitted (same value). The legacy `unresolvedThreads` key is
  # still consumed by scripts/orchard-decide.sh and scripts/orchard-monitor.sh
  # and asserted by the phase-field-in-json and unified-service-architecture
  # feature specs — removing it would break those call sites in-repo. Keep for
  # one release; drop once callers migrate.
  #
  # KNOWN GAPS (out of scope — documenting, not solving):
  # - Inline PR review-thread comments (`reviewThreads[].comments[]`) are NOT
  #   surfaced. `lastReviewCommentAt` sources only the top-level `Review`
  #   array, as the issue body specifies. A reviewer who leaves only inline
  #   comments without submitting a top-level review will not trigger
  #   `hasUnaddressedAuthorComment`. `unresolvedReviewThreads > 0` still
  #   surfaces this case as a blocker — just not the "who commented when."
  # - `hasUnaddressedAuthorComment` partially overlaps with
  #   `reviewDecision == "CHANGES_REQUESTED"`. The new field covers the
  #   `COMMENTED`/`APPROVED`-with-notes case that `reviewDecision` misses.
  #   Downstream consumers should OR both signals, not dedupe.

  Background:
    Given the canonical `Pr` type already carries `unresolved_threads: u32`, `reviews: Vec<Review>`, `author`, and `last_commit_pushed_at`
    And `pr_graphql_query_per_branch()` already fetches `reviewThreads(first: 100) { nodes { isResolved } }` and `reviews(first: 20) { nodes { author { login } state submittedAt } }`
    And `parse_prs_graphql_per_branch()` currently computes `unresolved_threads` by counting `isResolved != true` WITHOUT filtering by `isOutdated`
    And `JsonPr` serializes with `#[serde(rename_all = "camelCase")]`

  # ===================================================================
  # End-to-end — orchardist triage via `orchard --json`
  # (covers issue AC #1, AC #2, AC #3 on the real binary)
  # ===================================================================

  @e2e
  Scenario: Orchardist sees all three new fields on every PR in `orchard --json`
    Given a fixture repo with an open PR #101 that has:
      | attribute              | value                                                           |
      | author                 | drewdrewthis                                                    |
      | last_commit_pushed_at  | 2026-04-13T10:00:00Z                                            |
      | reviewThreads          | [{isResolved:false, isOutdated:false}, {isResolved:true, isOutdated:false}, {isResolved:false, isOutdated:true}] |
      | reviews                | [{author:"reviewer-1", state:"COMMENTED", submittedAt:"2026-04-13T21:11:53Z"}, {author:"drewdrewthis", state:"COMMENTED", submittedAt:"2026-04-13T09:00:00Z"}] |
    When `orchard --json` is run against the fixture
    Then the PR's `unresolvedReviewThreads` is 1
    And the PR's `lastReviewCommentAt` is `"2026-04-13T21:11:53Z"`
    And the PR's `lastReviewCommentAuthor` is `"reviewer-1"`
    And the PR's `hasUnaddressedAuthorComment` is `true`

  @e2e
  Scenario: Orchardist filters PRs with `jq` on the new fields without follow-up API calls
    Given `orchard --json` output for a repo containing three PRs:
      | pr  | unresolvedReviewThreads | hasUnaddressedAuthorComment |
      | 101 | 2                       | true                        |
      | 102 | 0                       | false                       |
      | 103 | 3                       | false                       |
    When the orchardist runs `jq '.repos[].worktrees[] | select(.pr.hasUnaddressedAuthorComment == true) | .pr.number'`
    Then the output is `101`
    # Rationale: AC #3 — detection must be a pure jq filter, no second API round trip.

  # ===================================================================
  # Integration — GraphQL query shape (isOutdated is required)
  # ===================================================================

  @integration
  Scenario: Compiled GraphQL query selects isOutdated on reviewThreads
    Given the compiled PR GraphQL query strings from BOTH `pr_graphql_query()` and `pr_graphql_query_per_branch()`
    Then each query contains `reviewThreads(first: 100)` with a node selection that includes BOTH `isResolved` AND `isOutdated`
    # Rationale: cache_sources.rs has two GraphQL query builders + two parsers (L74/L242
    # for the all-open-PRs path, L867/L948 for per-branch). Both must ship the new field
    # or the non-per-branch refresh path silently keeps old semantics.

  @integration
  Scenario: Compiled GraphQL reviews selection returns the most recent 20 reviews, not the oldest
    Given the compiled PR GraphQL query string
    Then the `reviews` field is selected as `last: 20` (or equivalently `first: 20, orderBy: {field: CREATED_AT, direction: DESC}`)
    # Rationale: default `first: 20` returns oldest-first. On PRs with >20 reviews
    # the latest review is truncated and `lastReviewCommentAt` is stale by days.
    # This is a bug in the existing query shape, surfaced by this feature.

  @integration
  Scenario: Parser skips thread nodes that lack isOutdated (backward-compat for stale fixtures / cache replay)
    Given a GraphQL response `reviewThreads.nodes` = `[{isResolved: false}]` with no `isOutdated` key present
    When the parser counts unresolved threads
    Then the thread is counted as unresolved
    And the parser emits a `LOG.warn` noting the missing field (so a real schema regression is not silently absorbed)
    # Rationale: absence of `isOutdated` means "not known to be outdated" — treat as
    # actionable. But log, don't swallow — distinguishes cache replay from schema drift.

  @integration
  Scenario: BOTH parser sites apply the isResolved AND isOutdated filter identically
    Given `reviewThreads.nodes` = `[{isResolved:false,isOutdated:false}, {isResolved:false,isOutdated:true}]`
    When `parse_prs_graphql()` is called (non-per-branch path)
    And `parse_prs_graphql_per_branch()` is called (per-branch path)
    Then both return `unresolved_threads = 1`
    # Rationale: prevents cache churn where two refresh paths write different counts.

  # ===================================================================
  # Unit — unresolved_threads rollup semantics (covers AC #1)
  # ===================================================================

  @unit
  Scenario: unresolved_threads counts only isResolved=false AND isOutdated=false
    Given `reviewThreads.nodes` = `[{isResolved:false,isOutdated:false}, {isResolved:false,isOutdated:false}, {isResolved:true,isOutdated:false}, {isResolved:false,isOutdated:true}, {isResolved:true,isOutdated:true}]`
    When `parse_prs_graphql_per_branch()` computes `unresolved_threads`
    Then `unresolved_threads` is 2

  @unit
  Scenario: unresolved_threads is zero when every thread is either resolved or outdated
    Given `reviewThreads.nodes` = `[{isResolved:true,isOutdated:false}, {isResolved:false,isOutdated:true}, {isResolved:true,isOutdated:true}]`
    When `parse_prs_graphql_per_branch()` computes `unresolved_threads`
    Then `unresolved_threads` is 0

  @unit
  Scenario: unresolved_threads is zero when reviewThreads.nodes is empty
    Given `reviewThreads.nodes` = `[]`
    When `parse_prs_graphql_per_branch()` computes `unresolved_threads`
    Then `unresolved_threads` is 0

  # ===================================================================
  # Unit — last_review_comment_at / last_review_comment_author (covers AC #2)
  # ===================================================================

  @unit
  Scenario: last_review_comment_at is the max submittedAt across all reviews
    Given a PR's `reviews` = `[
      {author:"a", state:"COMMENTED",         submittedAt:"2026-04-10T09:00:00Z"},
      {author:"b", state:"APPROVED",          submittedAt:"2026-04-13T21:11:53Z"},
      {author:"c", state:"CHANGES_REQUESTED", submittedAt:"2026-04-12T15:00:00Z"}
    ]`
    When `last_review_comment_at` and `last_review_comment_author` are derived
    Then `last_review_comment_at` is `"2026-04-13T21:11:53Z"`
    And `last_review_comment_author` is `"b"`

  @unit
  Scenario: last_review_comment_* are null when reviews is empty
    Given a PR's `reviews` = `[]`
    When `last_review_comment_at` and `last_review_comment_author` are derived
    Then `last_review_comment_at` is null
    And `last_review_comment_author` is null

  @unit
  Scenario: last_review_comment_* counts ALL review states, not just COMMENTED
    Given a PR's `reviews` = `[
      {author:"a", state:"APPROVED",          submittedAt:"2026-04-15T00:00:00Z"},
      {author:"b", state:"CHANGES_REQUESTED", submittedAt:"2026-04-14T00:00:00Z"}
    ]`
    When `last_review_comment_at` and `last_review_comment_author` are derived
    Then `last_review_comment_at` is `"2026-04-15T00:00:00Z"`
    And `last_review_comment_author` is `"a"`
    # Rationale: per issue body, source is "most recent top-level review" — state-agnostic.
    # APPROVED and CHANGES_REQUESTED carry human feedback just as COMMENTED does.

  @unit
  Scenario: reviews with null submittedAt are ignored in last_review_comment_at
    Given a PR's `reviews` = `[
      {author:"a", state:"COMMENTED", submittedAt:"2026-04-14T00:00:00Z"},
      {author:"b", state:"COMMENTED", submittedAt:null}
    ]`
    When `last_review_comment_at` and `last_review_comment_author` are derived
    Then `last_review_comment_at` is `"2026-04-14T00:00:00Z"`
    And `last_review_comment_author` is `"a"`

  # ===================================================================
  # Unit — has_unaddressed_author_comment logic (covers AC #3)
  # ===================================================================

  @unit
  Scenario: has_unaddressed_author_comment is true when last review is non-author AND post-push
    Given a PR where `author` is `"drewdrewthis"`
    And `last_commit_pushed_at` is `"2026-04-13T10:00:00Z"`
    And `last_review_comment_at` is `"2026-04-13T21:11:53Z"`
    And `last_review_comment_author` is `"reviewer-1"`
    When `has_unaddressed_author_comment` is derived
    Then `has_unaddressed_author_comment` is `true`

  @unit
  Scenario: has_unaddressed_author_comment is false when the last review is by the PR author
    Given a PR where `author` is `"drewdrewthis"`
    And `last_commit_pushed_at` is `"2026-04-13T10:00:00Z"`
    And `last_review_comment_at` is `"2026-04-13T21:11:53Z"`
    And `last_review_comment_author` is `"drewdrewthis"`
    When `has_unaddressed_author_comment` is derived
    Then `has_unaddressed_author_comment` is `false`

  @unit
  Scenario: has_unaddressed_author_comment is false when a subsequent push from the author addressed the review
    Given a PR where `author` is `"drewdrewthis"`
    And `last_review_comment_at` is `"2026-04-13T10:00:00Z"`
    And `last_commit_pushed_at` is `"2026-04-13T21:11:53Z"`
    And `last_review_comment_author` is `"reviewer-1"`
    When `has_unaddressed_author_comment` is derived
    Then `has_unaddressed_author_comment` is `false`
    # Rationale: push strictly after review means the author responded with code.

  @unit
  Scenario: has_unaddressed_author_comment is false when last_review_comment_at equals last_commit_pushed_at
    Given a PR where `author` is `"drewdrewthis"`
    And `last_review_comment_at` is `"2026-04-13T10:00:00Z"`
    And `last_commit_pushed_at` is `"2026-04-13T10:00:00Z"`
    And `last_review_comment_author` is `"reviewer-1"`
    When `has_unaddressed_author_comment` is derived
    Then `has_unaddressed_author_comment` is `false`
    # Rationale: use strictly-greater-than ("comment AFTER push"). Ties resolve to
    # "push addressed it" — safer default than surfacing a false positive.

  @unit
  Scenario: has_unaddressed_author_comment is false when there are no reviews at all
    Given a PR where `author` is `"drewdrewthis"`
    And `last_commit_pushed_at` is `"2026-04-13T10:00:00Z"`
    And `last_review_comment_at` is null
    And `last_review_comment_author` is null
    When `has_unaddressed_author_comment` is derived
    Then `has_unaddressed_author_comment` is `false`

  @unit
  Scenario: has_unaddressed_author_comment is true when last_commit_pushed_at is null and a non-author review exists
    Given a PR where `author` is `"drewdrewthis"`
    And `last_commit_pushed_at` is null
    And `last_review_comment_at` is `"2026-04-13T21:11:53Z"`
    And `last_review_comment_author` is `"reviewer-1"`
    When `has_unaddressed_author_comment` is derived
    Then `has_unaddressed_author_comment` is `true`
    # Rationale: no known push timestamp means we cannot prove the author responded.
    # Err toward surfacing the comment as unaddressed — a false positive here is
    # cheap ("go look at the PR"), a false negative silences a blocker.

  @unit
  Scenario: has_unaddressed_author_comment is false when pr.author is null
    Given a PR where `author` is null
    And `last_commit_pushed_at` is `"2026-04-13T10:00:00Z"`
    And `last_review_comment_at` is `"2026-04-13T21:11:53Z"`
    And `last_review_comment_author` is `"reviewer-1"`
    When `has_unaddressed_author_comment` is derived
    Then `has_unaddressed_author_comment` is `false`
    # Rationale: cannot compute "non-author" without a known PR author. Degrade to
    # false rather than invent a value.

  @unit
  Scenario: has_unaddressed_author_comment is false for merged or closed PRs
    Given a PR where `state` is `"MERGED"` (or `"CLOSED"`)
    And a last review by a non-author `"reviewer-1"` post-push
    When `has_unaddressed_author_comment` is derived
    Then `has_unaddressed_author_comment` is `false`
    # Rationale: triage signal only matters for open PRs. A merged PR with a late
    # review comment is not a blocker; orchardist `jq` filter must not surface it.

  # ===================================================================
  # Unit — JsonPr wire contract
  # ===================================================================

  @unit
  Scenario: JsonPr declares the three new camelCase fields
    Then `JsonPr` (with `#[serde(rename_all = "camelCase")]`) defines:
      | rust field                       | json key                      | type            |
      | unresolved_review_threads        | unresolvedReviewThreads       | u32             |
      | last_review_comment_at           | lastReviewCommentAt           | Option<String>  |
      | last_review_comment_author       | lastReviewCommentAuthor       | Option<String>  |
      | has_unaddressed_author_comment   | hasUnaddressedAuthorComment   | bool            |
    And the legacy `unresolvedThreads` key is still emitted (same value) until in-repo script and spec consumers migrate

  @unit
  Scenario: JsonPr emits all four fields even when reviews is empty
    Given a `Pr` with zero reviews, zero unresolved threads, and a known `last_commit_pushed_at`
    When the PR is serialized via `JsonPr`
    Then the JSON contains `"unresolvedReviewThreads": 0`
    And the JSON contains `"lastReviewCommentAt": null`
    And the JSON contains `"lastReviewCommentAuthor": null`
    And the JSON contains `"hasUnaddressedAuthorComment": false`
    # Rationale: downstream consumers (`jq` pipelines in the orchardist loop) must
    # be able to assume all four keys exist — never `undefined`.

  # ===================================================================
  # Integration — cache migration / backward compat
  # ===================================================================

  @integration
  Scenario: Old cache files without the new derived fields deserialize successfully
    Given a cache file written by a previous orchard version where `Pr` lacks any derived last_review_comment fields
    When the current orchard reads the cache file
    Then deserialization succeeds
    And after the next refresh, `unresolvedReviewThreads`, `lastReviewCommentAt`, `lastReviewCommentAuthor`, and `hasUnaddressedAuthorComment` are all populated from the GraphQL response
    # Rationale: the three new fields are derived from `reviews` + `last_commit_pushed_at`
    # at serialization time (not stored), so nothing new lands in the cache schema
    # beyond the already-present `reviews` array. Deserialization must be tolerant.

  # --- AC Coverage Map ---
  # Issue AC 1: "orchard --json includes unresolvedReviewThreads for each PR"
  #   → @e2e Orchardist sees all three new fields on every PR in `orchard --json`
  #   → @integration Compiled GraphQL query selects isOutdated on reviewThreads (BOTH query builders)
  #   → @integration Compiled GraphQL reviews selection returns most recent 20 reviews
  #   → @integration Parser skips thread nodes that lack isOutdated (with warn log)
  #   → @integration BOTH parser sites apply the filter identically
  #   → @unit unresolved_threads counts only isResolved=false AND isOutdated=false
  #   → @unit unresolved_threads is zero when every thread is either resolved or outdated
  #   → @unit unresolved_threads is zero when reviewThreads.nodes is empty
  #   → @unit JsonPr declares the three new camelCase fields
  #   → @unit JsonPr emits all four fields even when reviews is empty
  #   → @integration Old cache files without the new derived fields deserialize successfully
  #
  # Issue AC 2: "orchard --json includes last review comment timestamp + author"
  #   → @e2e Orchardist sees all three new fields on every PR in `orchard --json`
  #   → @unit last_review_comment_at is the max submittedAt across all reviews
  #   → @unit last_review_comment_* are null when reviews is empty
  #   → @unit last_review_comment_* counts ALL review states, not just COMMENTED
  #   → @unit reviews with null submittedAt are ignored in last_review_comment_at
  #   → @unit JsonPr declares the three new camelCase fields
  #
  # Issue AC 3: "Orchardist can detect 'PR has pending author feedback' without additional API calls"
  #   → @e2e Orchardist filters PRs with `jq` on the new fields without follow-up API calls
  #   → @e2e Orchardist sees all three new fields on every PR in `orchard --json`
  #   → @unit has_unaddressed_author_comment is true when last review is non-author AND post-push
  #   → @unit has_unaddressed_author_comment is false when the last review is by the PR author
  #   → @unit has_unaddressed_author_comment is false when a subsequent push from the author addressed the review
  #   → @unit has_unaddressed_author_comment is false when last_review_comment_at equals last_commit_pushed_at
  #   → @unit has_unaddressed_author_comment is false when there are no reviews at all
  #   → @unit has_unaddressed_author_comment is true when last_commit_pushed_at is null and a non-author review exists
  #   → @unit has_unaddressed_author_comment is false when pr.author is null
  #   → @unit has_unaddressed_author_comment is false for merged or closed PRs
  #   → @unit JsonPr declares the three new camelCase fields
  #   → @unit JsonPr emits all four fields even when reviews is empty
  #
  # AC count: 3. Every AC has ≥1 mapped scenario. Scope: strict — no field additions
  # beyond those named in the issue body. `unresolvedThreads` legacy key retained
  # for one-release deprecation (backward compat hygiene, not a new AC).
