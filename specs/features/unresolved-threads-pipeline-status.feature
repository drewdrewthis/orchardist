Feature: UnresolvedThreads pipeline status below ChangesRequested
  As an orchardist triaging PRs
  I want a dedicated `UnresolvedThreads` pipeline status with its own glyph, hierarchy position, and SINCE timestamp
  So that PRs with unresolved review threads but no formal CHANGES_REQUESTED review do not fall through to `AwaitingReview` and hide reviewer feedback

  # Issue #320 — promotion (not plumbing). The `unresolved_threads` signal is already
  # cached on PrState (populated by parse_prs_graphql_counts_unresolved_threads in
  # cache_sources.rs) and already honored by classify.rs. The gap is signal.rs:
  # `resolve_status` never reads it, so #3298-style PRs display as `AwaitingReview`.
  #
  # CONTRACT ADDITIONS:
  #   1. New enum variant `PipelineStatus::UnresolvedThreads` between `Paused` and `Ready`.
  #   2. Glyph U+1F4AC (speech bubble) + label "unresolved threads".
  #   3. `resolve_status` fires `UnresolvedThreads` when `pr.unresolved_threads > 0`
  #      and no higher-severity status matches.
  #   4. `signal.rs::is_ready_to_merge` must also return false when
  #      `unresolved_threads > 0` (aligns with classify.rs:100).
  #   5. `since_epoch` for `UnresolvedThreads` = max latest-comment timestamp across
  #      unresolved-AND-not-outdated threads; falls back to `pr.updated_at`.
  #   6. Signal module `//!` doc table updated.
  #   7. Unit tests (glyph, hierarchy, firing, precedence, since).
  #   8. TUI legend row updated.
  #   9. JSON output serializes as stable string `"unresolved_threads"`.
  #  10. Watch daemon emits status-pipeline-transition events (distinct from the
  #      existing ReviewComments count event at watch/diff.rs:190).
  #  11. `cargo test -p orchard` green; `cargo clippy -p orchard -- -D warnings` clean.
  #
  # OUT OF SCOPE (explicit):
  # - Bot-author filtering (all threads count — matches existing count_actionable_threads).
  # - Changing `count_actionable_threads` filter semantics (isResolved=false AND isOutdated!=true per #252).
  # - Direction-of-blame signal ("reviewer waiting on author" vs. inverse).
  # - Modifying the existing ReviewComments count-transition event semantics.

  Background:
    Given `PrState.unresolved_threads: u32` is already populated from `reviewThreads { isResolved, isOutdated }`
    And `parse_prs_graphql_counts_unresolved_threads` already filters to `isResolved=false AND isOutdated!=true`
    And the current `PipelineStatus` hierarchy is: NeedsInput → CiFailing → MergeConflict → ChangesRequested → Coding → AwaitingReview → Draft → Blocked → Paused → Ready → Merged
    And classify.rs::is_ready_to_merge already requires `unresolved_threads == 0` but signal.rs::is_ready_to_merge does not

  # ===================================================================
  # End-to-end — full binary renders the new status in TUI + JSON
  # (covers AC #1 enum slot, AC #2 glyph, AC #3 resolution, AC #9 JSON value)
  # ===================================================================

  @e2e
  Scenario: `orchard --json` surfaces `unresolved_threads` as the status for a PR with unresolved threads and no higher blocker
    Given a fixture repo with an open worktree whose PR #3298 is approved, CI passing, no merge conflicts, not paused, not blocked
    And the PR's `unresolved_threads` is 2
    When `orchard --json` is run against the fixture
    Then the worktree's `status` field is `"unresolved_threads"`
    And the worktree's `status_glyph` is `"💬"`
    And the worktree is not surfaced with status `"ready"`
    And the worktree is not surfaced with status `"awaiting_review"`

  @e2e
  Scenario: TUI renders the 💬 glyph for PRs with unresolved threads and includes the legend row
    Given a fixture repo with a worktree PR that has `unresolved_threads > 0` and no higher-severity blocker
    When orchard TUI renders the worktree list
    Then the worktree row shows the 💬 glyph
    And the TUI status legend lists a row with glyph `💬`, label `"unresolved threads"`, and a short meaning
    # Rationale: AC #8 — wherever the legend lives (`tui/mod.rs` or widgets), the
    # new status must be visible so users learn the glyph without grepping source.

  # ===================================================================
  # Integration — watch daemon, GraphQL query shape, cache migration
  # ===================================================================

  @integration
  Scenario: Watch daemon emits a status-transition event on AwaitingReview → UnresolvedThreads
    Given a prior watch snapshot where worktree W's status was `AwaitingReview`
    And the new snapshot places W at status `UnresolvedThreads`
    When `crates/orchard/src/watch/diff.rs` diffs the two snapshots
    Then a status-change event is emitted recording the transition `AwaitingReview → UnresolvedThreads`
    And the existing `ReviewComments` count-transition event at `watch/diff.rs:190` is NOT duplicated for the same transition
    # Rationale: AC #10 — two orthogonal signals (count 0→N vs. status change). Both
    # may fire at the same time for the same worktree; they must remain distinct events.

  @integration
  Scenario: Watch daemon emits a status-transition event on UnresolvedThreads → Ready
    Given a prior watch snapshot where worktree W's status was `UnresolvedThreads`
    And the new snapshot places W at status `Ready` (threads now all resolved)
    When the diff is computed
    Then a status-change event is emitted recording `UnresolvedThreads → Ready`

  @integration
  Scenario: Both GraphQL query builders select per-thread latest-comment timestamps
    Given the compiled PR GraphQL query strings from BOTH `pr_graphql_query` (cache_sources.rs:917) AND `pr_graphql_query_per_branch` (cache_sources.rs:1027)
    Then each query's `reviewThreads` node selection includes `comments(last: 1) { nodes { createdAt } }`
    And no other field of `comments` (e.g. `body`, `author`) is selected
    # Rationale: AC #5 — missing either builder causes inconsistent `since_epoch`
    # depending on refresh phase. Minimal selection keeps GraphQL cost bounded.

  @integration
  Scenario: Old cache files without per-thread timestamps deserialize cleanly
    Given a `CachedPr` JSON written by a previous orchard version that lacks the per-thread latest-comment timestamp field
    When current orchard reads the cache file
    Then deserialization succeeds (field defaults to empty / None)
    And `since_epoch` for `UnresolvedThreads` on that PR falls back to `pr.updated_at`
    # Rationale: Serde default tolerance for forward-compat cache replay.

  @integration
  Scenario: JSON output serializes `UnresolvedThreads` as stable string `"unresolved_threads"`
    Given a `JsonOutput` produced for a worktree whose `PipelineStatus` is `UnresolvedThreads`
    When the output is serialized
    Then the `status` field value is the exact string `"unresolved_threads"`
    And if `JsonOutput` carries a version field, the minor version is bumped to reflect the new enum value
    # Rationale: AC #9 — downstream scripts parsing `.status` must see a stable,
    # snake_case string distinct from all other variants.

  # ===================================================================
  # Unit — enum shape, glyph table, hierarchy ordering (AC #1, #2, #7)
  # ===================================================================

  @unit
  Scenario: `PipelineStatus` declares `UnresolvedThreads` between `Paused` and `Ready`
    Then the `PipelineStatus` enum in `crates/orchard/src/signal.rs` declares variants in order:
      | position | variant            |
      | 1        | NeedsInput         |
      | 2        | CiFailing          |
      | 3        | MergeConflict      |
      | 4        | ChangesRequested   |
      | 5        | Coding             |
      | 6        | AwaitingReview     |
      | 7        | Draft              |
      | 8        | Blocked            |
      | 9        | Paused             |
      | 10       | UnresolvedThreads  |
      | 11       | Ready              |
      | 12       | Merged             |

  @unit
  Scenario: `status_ord_matches_hierarchy` asserts `Paused < UnresolvedThreads < Ready`
    When the existing `status_ord_matches_hierarchy` unit test runs
    Then it asserts `PipelineStatus::Paused < PipelineStatus::UnresolvedThreads`
    And it asserts `PipelineStatus::UnresolvedThreads < PipelineStatus::Ready`

  @unit
  Scenario: `every_status_has_distinct_glyph` accepts `UnresolvedThreads` with glyph 💬
    When the existing `every_status_has_distinct_glyph` unit test runs
    Then `PipelineStatus::UnresolvedThreads.glyph()` returns the string `"💬"` (U+1F4AC)
    And that glyph is distinct from every other non-blank glyph in the enum (10 others)

  @unit
  Scenario: `label()` returns `"unresolved threads"` for `UnresolvedThreads`
    When `PipelineStatus::UnresolvedThreads.label()` is invoked
    Then the returned string is exactly `"unresolved threads"`

  # ===================================================================
  # Unit — resolve_status firing + precedence (AC #3, #4)
  # ===================================================================

  @unit
  Scenario: `resolve_status` fires `UnresolvedThreads` when `pr.unresolved_threads > 0` on an otherwise unblocked PR
    Given a PR that is approved, CI passing, not drafted, not in merge conflict, not paused, not blocked, not awaiting review for other reasons
    And `pr.unresolved_threads` is 1
    When `resolve_status` is invoked
    Then it returns `PipelineStatus::UnresolvedThreads`

  @unit
  Scenario: `Paused` beats `UnresolvedThreads`
    Given a PR with `pr.unresolved_threads > 0`
    And the worktree is flagged as paused
    When `resolve_status` is invoked
    Then it returns `PipelineStatus::Paused`

  @unit
  Scenario: `Blocked` beats `UnresolvedThreads`
    Given a PR with `pr.unresolved_threads > 0`
    And the worktree is blocked (issue-level blocker)
    When `resolve_status` is invoked
    Then it returns `PipelineStatus::Blocked`

  @unit
  Scenario: `ChangesRequested` beats `UnresolvedThreads` when both conditions hold
    Given a PR with formal review decision `CHANGES_REQUESTED`
    And `pr.unresolved_threads > 0`
    When `resolve_status` is invoked
    Then it returns `PipelineStatus::ChangesRequested`

  @unit
  Scenario: `UnresolvedThreads` beats `Ready` — approved + CI passing + threads open → UnresolvedThreads, not Ready
    Given a PR that is approved
    And CI is passing
    And `pr.unresolved_threads` is 1
    When `resolve_status` is invoked
    Then it returns `PipelineStatus::UnresolvedThreads`
    # Rationale: AC #4 — without this, an approved+passing+thread-blocked PR renders
    # as 🟢 Ready through the `is_ready_to_merge` short-circuit, before the
    # UnresolvedThreads branch is ever checked. The fix MUST land at
    # `signal.rs::is_ready_to_merge` (aligning with classify.rs:100).

  @unit
  Scenario: `UnresolvedThreads` does NOT fire when `pr.unresolved_threads == 0`
    Given a PR that is approved, CI passing, and `pr.unresolved_threads == 0`
    When `resolve_status` is invoked
    Then it returns `PipelineStatus::Ready`
    And it does NOT return `PipelineStatus::UnresolvedThreads`

  @unit
  Scenario: `signal.rs::is_ready_to_merge` returns false when `unresolved_threads > 0`
    Given a PR that is approved and CI passing
    And `pr.unresolved_threads` is 1
    When `signal.rs::is_ready_to_merge(pr)` is invoked
    Then it returns false
    # Rationale: AC #4 explicit — aligns signal.rs with classify.rs:100 (which
    # already checks `unresolved_threads == 0`). Prevents flicker where the
    # approved-but-thread-blocked PR toggles between 🟢 and 💬.

  # ===================================================================
  # Unit — since_epoch semantics for UnresolvedThreads (AC #5)
  # ===================================================================

  @unit
  Scenario: `since_epoch` for `UnresolvedThreads` uses the max latest-comment timestamp across unresolved-and-not-outdated threads
    Given a PR where `reviewThreads` is:
      | isResolved | isOutdated | latest_comment_at    |
      | false      | false      | 2026-04-15T10:00:00Z |
      | false      | false      | 2026-04-18T09:30:00Z |
      | true       | false      | 2026-04-19T11:00:00Z |
      | false      | true       | 2026-04-19T12:00:00Z |
    When `since_epoch` is computed for status `UnresolvedThreads`
    Then the returned timestamp equals the epoch of `"2026-04-18T09:30:00Z"`
    # Rationale: filter matches `count_actionable_threads` (isResolved=false AND
    # isOutdated!=true). Resolved and outdated thread timestamps are ignored.

  @unit
  Scenario: `since_epoch` for `UnresolvedThreads` falls back to `pr.updated_at` when thread timestamp data is unavailable
    Given a PR whose `reviewThreads` have no latest-comment timestamps populated (old cache, or GraphQL omitted the field)
    And `pr.unresolved_threads > 0` (as computed from the count field)
    And `pr.updated_at` is `"2026-04-10T00:00:00Z"`
    When `since_epoch` is computed for status `UnresolvedThreads`
    Then the returned timestamp equals the epoch of `"2026-04-10T00:00:00Z"`

  @unit
  Scenario: `since_epoch` for `UnresolvedThreads` falls back to `pr.updated_at` when no unresolved-and-not-outdated thread timestamps exist but count > 0
    Given a PR where every `reviewThread` is either resolved or outdated but `pr.unresolved_threads` is stale and > 0
    And `pr.updated_at` is `"2026-04-11T00:00:00Z"`
    When `since_epoch` is computed for status `UnresolvedThreads`
    Then the returned timestamp equals the epoch of `"2026-04-11T00:00:00Z"`
    # Rationale: defensive fallback — if the count says threads exist but the
    # timestamp filter produces nothing, do not return None or crash.

  # ===================================================================
  # Unit — module doc hygiene (AC #6)
  # ===================================================================

  @unit
  Scenario: `signal.rs` module `//!` doc lists UnresolvedThreads in its hierarchy slot
    Given the `//!` module-level doc at the top of `crates/orchard/src/signal.rs`
    Then the severity hierarchy table/comment lists `UnresolvedThreads` in position between `Paused` and `Ready`
    And the row includes glyph `💬` and a short meaning line
    And any prior severity-note text explaining why the position sits "after ChangesRequested, below Blocked and Paused" is updated to reference the new variant

  # ===================================================================
  # Verification (AC #11)
  # ===================================================================

  @integration
  Scenario: `cargo test -p orchard` passes and `cargo clippy -p orchard -- -D warnings` is clean
    When `cargo test -p orchard` is run
    Then all tests pass
    When `cargo clippy -p orchard -- -D warnings` is run
    Then the command exits zero with no warnings

  # --- AC Coverage Map ---
  # Issue AC 1: "Enum variant `UnresolvedThreads` added, ordered between Paused and Ready"
  #   → @unit `PipelineStatus` declares `UnresolvedThreads` between `Paused` and `Ready`
  #   → @unit `status_ord_matches_hierarchy` asserts `Paused < UnresolvedThreads < Ready`
  #
  # Issue AC 2: "Glyph 💬 (U+1F4AC) + label 'unresolved threads'; glyph distinct"
  #   → @unit `every_status_has_distinct_glyph` accepts `UnresolvedThreads` with glyph 💬
  #   → @unit `label()` returns `"unresolved threads"` for `UnresolvedThreads`
  #
  # Issue AC 3: "`resolve_status` fires UnresolvedThreads when pr.unresolved_threads > 0
  #              and no higher-severity status matches; after Paused, before Draft/Ready/AwaitingReview"
  #   → @e2e `orchard --json` surfaces `unresolved_threads` as the status
  #   → @unit `resolve_status` fires `UnresolvedThreads` when `pr.unresolved_threads > 0` on an otherwise unblocked PR
  #   → @unit `Paused` beats `UnresolvedThreads`
  #   → @unit `Blocked` beats `UnresolvedThreads`
  #   → @unit `ChangesRequested` beats `UnresolvedThreads` when both conditions hold
  #   → @unit `UnresolvedThreads` does NOT fire when `pr.unresolved_threads == 0`
  #
  # Issue AC 4: "`signal.rs::is_ready_to_merge` blocks on unresolved_threads > 0 (align with classify.rs:100);
  #              approved-but-thread-blocked PR must not render as Ready"
  #   → @unit `UnresolvedThreads` beats `Ready` — approved + CI passing + threads open → UnresolvedThreads
  #   → @unit `signal.rs::is_ready_to_merge` returns false when `unresolved_threads > 0`
  #
  # Issue AC 5: "SINCE timestamp sourced from unresolved thread comment timestamps;
  #              extend BOTH GraphQL queries with `comments(last:1){nodes{createdAt}}`;
  #              max across unresolved-AND-not-outdated; fall back to pr.updated_at"
  #   → @integration Both GraphQL query builders select per-thread latest-comment timestamps
  #   → @integration Old cache files without per-thread timestamps deserialize cleanly
  #   → @unit `since_epoch` for `UnresolvedThreads` uses the max latest-comment timestamp across unresolved-and-not-outdated threads
  #   → @unit `since_epoch` for `UnresolvedThreads` falls back to `pr.updated_at` when thread timestamp data is unavailable
  #   → @unit `since_epoch` for `UnresolvedThreads` falls back to `pr.updated_at` when no unresolved-and-not-outdated thread timestamps exist but count > 0
  #
  # Issue AC 6: "signal.rs `//!` header table / hierarchy comment updated with new slot + glyph"
  #   → @unit `signal.rs` module `//!` doc lists UnresolvedThreads in its hierarchy slot
  #
  # Issue AC 7: "Unit tests added: glyph distinct, hierarchy ord, firing conditions, precedence, since semantics, is_ready_to_merge alignment"
  #   → @unit `every_status_has_distinct_glyph` accepts `UnresolvedThreads` with glyph 💬
  #   → @unit `status_ord_matches_hierarchy` asserts `Paused < UnresolvedThreads < Ready`
  #   → @unit `resolve_status` fires `UnresolvedThreads` when `pr.unresolved_threads > 0` on an otherwise unblocked PR
  #   → @unit `Paused` beats `UnresolvedThreads`
  #   → @unit `Blocked` beats `UnresolvedThreads`
  #   → @unit `UnresolvedThreads` beats `Ready` — approved + CI passing + threads open → UnresolvedThreads
  #   → @unit `ChangesRequested` beats `UnresolvedThreads` when both conditions hold
  #   → @unit `since_epoch` for `UnresolvedThreads` uses the max latest-comment timestamp across unresolved-and-not-outdated threads
  #   → @unit `since_epoch` for `UnresolvedThreads` falls back to `pr.updated_at` when thread timestamp data is unavailable
  #
  # Issue AC 8: "TUI legend updated — glyph row with label + meaning"
  #   → @e2e TUI renders the 💬 glyph for PRs with unresolved threads and includes the legend row
  #
  # Issue AC 9: "JSON output schema supports the new variant; stable string (e.g. `unresolved_threads`);
  #              if JsonOutput versioned, bump minor"
  #   → @e2e `orchard --json` surfaces `unresolved_threads` as the status
  #   → @integration JSON output serializes `UnresolvedThreads` as stable string `"unresolved_threads"`
  #
  # Issue AC 10: "Watch daemon emits status-pipeline transition events in/out of UnresolvedThreads;
  #               do NOT duplicate the existing ReviewComments count event at watch/diff.rs:190"
  #   → @integration Watch daemon emits a status-transition event on AwaitingReview → UnresolvedThreads
  #   → @integration Watch daemon emits a status-transition event on UnresolvedThreads → Ready
  #
  # Issue AC 11: "Verification: `cargo test -p orchard` passes; `cargo clippy -p orchard -- -D warnings` clean"
  #   → @integration `cargo test -p orchard` passes and `cargo clippy -p orchard -- -D warnings` is clean
  #
  # AC count: 11. Every AC has ≥1 mapped scenario. No ACs dropped.
