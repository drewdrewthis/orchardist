Feature: Consolidate is_ready_to_merge across signal.rs, classify.rs, watch/diff.rs (#322)
  As an orchard maintainer
  I want one canonical is_ready_to_merge predicate operating on a small NewType view
  So that the next CI-field or merge-gate change lands in one place
     instead of three near-identical implementations that can silently diverge

  # Refactor — semantics are explicit, not implicit. Three behaviour deltas are
  # intentional and bounded:
  #   * watch/diff.rs starts filtering draft PRs (closes a pre-existing minor bug).
  #   * watch/diff.rs and signal.rs start respecting ci_gate_state (blocked/pending = not ready).
  #   * signal.rs's existing None-as-passing behaviour is preserved as canonical.
  # See the issue body's Plan §"Key decisions" for the rationale.

  Background:
    Given `crates/orchard/src/signal.rs` defines `fn is_ready_to_merge(pr: &PrState) -> bool` at line 472
    And `crates/orchard/src/classify.rs` defines `pub(crate) fn is_ready_to_merge(pr: &PrInfo) -> bool` at line 97
    And `crates/orchard/src/watch/diff.rs` defines `fn is_ready_to_merge(pr: &PrState) -> bool` at line 64
    And `crates/orchard/src/orchard_state.rs` defines `impl From<&derive::PrInfo> for PrState`
    And `PrState` and `derive::PrInfo` both carry the seven predicate-relevant fields
       (state, is_draft, has_conflicts, unresolved_threads, review_decision, ci_code_state, ci_gate_state)

  # =======================================================================
  # AC1 — Single canonical implementation in crates/orchard/src/merge_readiness.rs
  # =======================================================================

  @unit
  Scenario: merge_readiness module exists and is wired into lib.rs
    Then `crates/orchard/src/merge_readiness.rs` exists
    And `crates/orchard/src/lib.rs` declares `pub mod merge_readiness;` (or `mod merge_readiness;` with selective re-exports)
    And `merge_readiness::is_ready_to_merge` is the only `is_ready_to_merge` function in the workspace
    And the function is reachable from `signal.rs`, `classify.rs`, and `watch/diff.rs`

  # =======================================================================
  # AC2 — Predicate operates on MergeReadinessView NewType with two From adapters
  # =======================================================================

  @unit
  Scenario: MergeReadinessView NewType exposes exactly seven predicate-relevant fields
    Given the `MergeReadinessView` struct in `crates/orchard/src/merge_readiness.rs`
    Then it has these fields (names exact; types may be `Option<&str>` / `&str` / `bool` / `u32`):
      | field              | semantic                                            |
      | state              | PR state string (e.g. "OPEN", "MERGED", "CLOSED")   |
      | is_draft           | draft flag                                          |
      | has_conflicts      | merge-conflict flag                                 |
      | unresolved_threads | count of unresolved review threads                  |
      | review_decision    | review-decision string (e.g. Some("approved"))      |
      | ci_code_state      | code-CI state (Some("passing") / Some("failing"))   |
      | ci_gate_state      | merge-gate state (Some("cleared") / "blocked" / …)  |
    And the struct contains exactly seven fields (no extras)

  @unit
  Scenario: MergeReadinessView fields are borrowed/copy (no owned Vec/String per row)
    Given the `MergeReadinessView` struct definition
    Then no field is an owned `String`
    And no field is an owned `Vec<_>`
    And the type is suitable for per-row construction without heap allocation in the hot path

  @unit
  Scenario: From<&PrState> for MergeReadinessView adapter exists and is total
    Given `impl From<&PrState> for MergeReadinessView` in `crates/orchard/src/merge_readiness.rs`
    When a fully-populated `PrState` is converted
    Then every one of the seven `MergeReadinessView` fields is populated from the corresponding `PrState` field
    And the adapter does not consult fields outside the seven (no reviewers / labels / timestamps reads)

  @unit
  Scenario: From<&derive::PrInfo> for MergeReadinessView adapter exists and is total
    Given `impl From<&derive::PrInfo> for MergeReadinessView` in `crates/orchard/src/merge_readiness.rs`
    When a fully-populated `derive::PrInfo` is converted
    Then every one of the seven `MergeReadinessView` fields is populated from the corresponding `PrInfo` field
    And the adapter does not consult `checks_state` (legacy field) for `ci_code_state` or `ci_gate_state`
    And the adapter does not clone any `Vec<_>` from `PrInfo`

  # =======================================================================
  # AC3 — All three callsites migrate to the canonical function
  # =======================================================================

  @integration
  Scenario: signal.rs no longer defines a private is_ready_to_merge
    When `crates/orchard/src/signal.rs` is inspected
    Then it does NOT define `fn is_ready_to_merge(pr: &PrState) -> bool`
    And the previous callsite (line 427 region) calls `merge_readiness::is_ready_to_merge(&(&pr).into())`
       (or an equivalent binding through the `From<&PrState>` adapter)

  @integration
  Scenario: classify.rs no longer defines a private is_ready_to_merge
    When `crates/orchard/src/classify.rs` is inspected
    Then it does NOT define `pub(crate) fn is_ready_to_merge(pr: &PrInfo) -> bool`
    And the previous callsite (line 56 region) calls `merge_readiness::is_ready_to_merge(&(&pr).into())`
       (or an equivalent binding through the `From<&derive::PrInfo>` adapter)

  @integration
  Scenario: watch/diff.rs no longer defines a private is_ready_to_merge
    When `crates/orchard/src/watch/diff.rs` is inspected
    Then it does NOT define `fn is_ready_to_merge(pr: &PrState) -> bool`
    And every previous callsite (lines 233, 234, 246 regions) calls `merge_readiness::is_ready_to_merge(&(&pr).into())`

  @integration
  Scenario: No private is_ready_to_merge function survives anywhere in the workspace
    When the workspace is grepped for `fn is_ready_to_merge`
    Then exactly one match is found
    And that match is in `crates/orchard/src/merge_readiness.rs`

  # =======================================================================
  # AC4 — Canonical semantics: filter branches and allow branches
  # =======================================================================

  @unit
  Scenario: Predicate returns false when review_decision is not Some("approved")
    Given a `MergeReadinessView` with `review_decision = Some("changes_requested")` and all other fields green
    When `merge_readiness::is_ready_to_merge(&view)` is called
    Then it returns false
    And a second view with `review_decision = None` also returns false
    And a third view with `review_decision = Some("commented")` also returns false

  @unit
  Scenario: Predicate returns false when is_draft is true
    Given a `MergeReadinessView` with `is_draft = true`, `review_decision = Some("approved")`, and all other fields green
    When `merge_readiness::is_ready_to_merge(&view)` is called
    Then it returns false

  @unit
  Scenario: Predicate returns false when has_conflicts is true
    Given a `MergeReadinessView` with `has_conflicts = true`, `review_decision = Some("approved")`, and all other fields green
    When `merge_readiness::is_ready_to_merge(&view)` is called
    Then it returns false

  @unit
  Scenario: Predicate returns false when unresolved_threads > 0
    Given a `MergeReadinessView` with `unresolved_threads = 1`, `review_decision = Some("approved")`, and all other fields green
    When `merge_readiness::is_ready_to_merge(&view)` is called
    Then it returns false

  @unit
  Scenario: Predicate returns false when ci_code_state is "failing"
    Given a `MergeReadinessView` with `ci_code_state = Some("failing")`, `review_decision = Some("approved")`, and all other fields green
    When `merge_readiness::is_ready_to_merge(&view)` is called
    Then it returns false

  @unit
  Scenario: Predicate returns false when ci_gate_state is "blocked"
    Given a `MergeReadinessView` with `ci_gate_state = Some("blocked")`, `review_decision = Some("approved")`, and all other fields green
    When `merge_readiness::is_ready_to_merge(&view)` is called
    Then it returns false

  @unit
  Scenario: Predicate returns false when ci_gate_state is "pending"
    Given a `MergeReadinessView` with `ci_gate_state = Some("pending")`, `review_decision = Some("approved")`, and all other fields green
    When `merge_readiness::is_ready_to_merge(&view)` is called
    Then it returns false

  @unit
  Scenario: Predicate returns true when approved + ci_code_state passing + ci_gate_state cleared
    Given a `MergeReadinessView` with `review_decision = Some("approved")`,
       `ci_code_state = Some("passing")`, `ci_gate_state = Some("cleared")`,
       `is_draft = false`, `has_conflicts = false`, `unresolved_threads = 0`
    When `merge_readiness::is_ready_to_merge(&view)` is called
    Then it returns true

  @unit
  Scenario: Predicate treats ci_code_state == None as passing (None-as-passing preserved)
    Given a `MergeReadinessView` with `review_decision = Some("approved")`,
       `ci_code_state = None`, `ci_gate_state = None`,
       `is_draft = false`, `has_conflicts = false`, `unresolved_threads = 0`
    When `merge_readiness::is_ready_to_merge(&view)` is called
    Then it returns true
    # Rationale: approved PR in a no-CI-configured repo continues to fire Ready,
    # matching signal.rs's pre-refactor behaviour.

  @unit
  Scenario: Predicate treats ci_gate_state == None as cleared
    Given a `MergeReadinessView` with `review_decision = Some("approved")`,
       `ci_code_state = Some("passing")`, `ci_gate_state = None`,
       `is_draft = false`, `has_conflicts = false`, `unresolved_threads = 0`
    When `merge_readiness::is_ready_to_merge(&view)` is called
    Then it returns true

  @unit
  Scenario: Predicate does NOT check is_open / pr.state
    Given a `MergeReadinessView` with `state = "MERGED"` and all other fields green
    When `merge_readiness::is_ready_to_merge(&view)` is called
    Then it returns true (state is callsite concern, not predicate concern)
    # Rationale: signal.rs's resolve_status() ordering already short-circuits on
    # merged/closed before the predicate runs; classify.rs's caller does the same.

  @unit
  Scenario: Predicate uses literal Some("approved") matching (production cache data is lowercase)
    Given a `MergeReadinessView` with `review_decision = Some("APPROVED")` (uppercase)
    When `merge_readiness::is_ready_to_merge(&view)` is called
    Then it returns false
    # Rationale: real cache data is lowercase per cache_sources.rs:348; the
    # case-insensitive comparison previously in signal.rs was defensive against
    # test fixtures only.

  # =======================================================================
  # AC5 — watch/diff.rs migrates off checks_state (legacy) to ci_code_state + ci_gate_state
  # =======================================================================

  @integration
  Scenario: watch/diff.rs no longer reads checks_state for readiness decisions
    When `crates/orchard/src/watch/diff.rs` is inspected after the refactor
    Then no readiness-related code path reads `pr.checks_state`
    And readiness flows through `merge_readiness::is_ready_to_merge` via the `From<&PrState>` adapter
    And the adapter sources `ci_code_state` and `ci_gate_state` directly from `PrState`

  @integration
  Scenario: Approved + code-green + gate-cleared PR still fires PrReadyToMerge in watch
    Given a watch fixture where `new_pr` is approved, `ci_code_state = Some("passing")`,
       `ci_gate_state = Some("cleared")`, not draft, no conflicts, zero unresolved threads
    And `old_pr` was not ready (e.g. `review_decision = None`)
    When `watch::diff::diff` runs
    Then a `PrReadyToMerge` event is emitted for that worktree

  @integration
  Scenario: Approved + code-green + gate-blocked PR no longer fires PrReadyToMerge in watch
    Given a watch fixture where `new_pr` is approved, `ci_code_state = Some("passing")`,
       `ci_gate_state = Some("blocked")`, not draft, no conflicts, zero unresolved threads
    When `watch::diff::diff` runs
    Then no `PrReadyToMerge` event is emitted for that worktree
    # Documented behaviour delta: watch/diff.rs starts respecting gate state.

  # =======================================================================
  # AC6 — Documented behaviour deltas (intentional, surfaced in PR description)
  # =======================================================================

  @integration
  Scenario: Behaviour delta — draft+approved+passing PR no longer fires PrReadyToMerge in watch
    Given a watch fixture where `new_pr` is approved, `ci_code_state = Some("passing")`,
       `ci_gate_state = Some("cleared")`, `is_draft = true`, no conflicts, zero unresolved threads
    And `old_pr` was not ready
    When `watch::diff::diff` runs
    Then no `PrReadyToMerge` event is emitted for that worktree
    # Pre-refactor watch/diff.rs did NOT filter draft. The unified predicate does.

  @integration
  Scenario: Behaviour delta — signal.rs no longer routes a gate-blocked PR to Ready
    Given a signal fixture: PR is approved, `ci_code_state = Some("passing")`,
       `ci_gate_state = Some("blocked")`, not draft, no conflicts, zero unresolved threads
    When `signal::resolve_status` (or whatever calls the canonical predicate) runs
    Then the resulting `PipelineStatus` is NOT `Ready`
    # Pre-refactor signal.rs did NOT check gate state. The unified predicate does.

  @integration
  Scenario: Behaviour preserved — approved PR with no CI configured still fires Ready in signal
    Given a signal fixture: PR is approved, `ci_code_state = None`,
       `ci_gate_state = None`, not draft, no conflicts, zero unresolved threads
    When `signal::resolve_status` runs
    Then the resulting `PipelineStatus` is `Ready`
    # None-as-passing canonical semantics preserve signal.rs's pre-refactor behaviour.

  @unit
  Scenario: PR description documents the three behaviour deltas
    Given the PR opened for issue #322
    Then the PR body contains a section explicitly listing:
      | delta                                                                   |
      | watch/diff.rs starts filtering draft PRs                                |
      | watch/diff.rs and signal.rs start respecting ci_gate_state              |
      | signal.rs's existing None-as-passing behaviour is preserved             |

  # =======================================================================
  # AC7 — No regressions on the canonical "ready" path; existing tests pass
  # =======================================================================

  @integration
  Scenario: Existing signal.rs ready/not-ready tests still pass under the new predicate
    Given the `#[cfg(test)]` block in `crates/orchard/src/signal.rs` after migration
    When `cargo test -p orchard signal::` runs
    Then every test passes
    And `is_ready_to_merge_returns_false_when_unresolved_threads_gt_zero` (line 1338) still asserts false
    And no test asserts the now-removed local `fn is_ready_to_merge` exists

  @integration
  Scenario: Existing classify.rs ready tests still pass under the new predicate
    Given the `#[cfg(test)]` block in `crates/orchard/src/classify.rs` after migration
    When `cargo test -p orchard classify::` runs
    Then every test passes
    And the test at line 481 (`is_ready_to_merge fires when code is green and gate is cleared`)
       continues to assert ready
    And no test asserts the now-removed local `is_ready_to_merge` exists

  @integration
  Scenario: Existing watch/diff.rs ready-transition tests still pass under the new predicate
    Given the test module in `crates/orchard/src/watch/diff.rs` after migration
    When `cargo test -p orchard watch::diff::` runs
    Then tests asserting the documented `PrReadyToMerge` happy path pass
    And any test that previously relied on draft-not-being-filtered is updated to reflect the
       documented behaviour delta (draft no longer fires Ready) and now passes

  @e2e
  Scenario: cargo test --workspace passes after the refactor lands
    When `cargo test --workspace` runs
    Then it exits with code 0
    And no pre-existing test outside the three migrated modules regresses

  @integration
  Scenario: Workspace builds clean with clippy and fmt
    When `cargo build --workspace` runs
    Then it succeeds with no errors
    And `cargo clippy --workspace --all-targets -- -D warnings` succeeds
    And `cargo fmt --check` succeeds

  # =======================================================================
  # AC8 — Module is ≤ 80 lines including doc comments
  # =======================================================================

  @unit
  Scenario: merge_readiness.rs is at most 80 lines including doc comments
    When `wc -l crates/orchard/src/merge_readiness.rs` runs
    Then the line count is ≤ 80
    # Includes doc comments, struct definition, two From impls, the predicate fn.
    # Tests for the predicate may live in a colocated #[cfg(test)] mod and do
    # NOT count toward this budget (test code is excluded from the budget; if the
    # implementer chooses to colocate tests, the 80-line cap applies to the
    # non-test region only).

  # =======================================================================
  # Scope guards — what this refactor does NOT do
  # =======================================================================

  @unit
  Scenario: types::PrInfo elimination is NOT in scope
    Given `crates/orchard/src/types.rs` defines a `PrInfo` distinct from `derive::PrInfo`
    Then the refactor does NOT delete or modify `types::PrInfo`
    And the single caller in `crates/orchard/src/tui/list.rs:1015` is unchanged
    And a follow-up issue stub for `types::PrInfo` elimination is filed (link present in the PR body)

  @unit
  Scenario: derive::PrInfo and PrState are NOT merged
    Then `crates/orchard/src/derive.rs` still defines `PrInfo`
    And `crates/orchard/src/orchard_state.rs` still defines `PrState`
    And `impl From<&derive::PrInfo> for PrState` at `orchard_state.rs:344` is unchanged
       (still available for other consumers — json output, snapshot writers)

  @unit
  Scenario: is_needs_attention is NOT consolidated
    Given `crates/orchard/src/classify.rs` defines `is_needs_attention` (line 92 region)
    Then this predicate is unchanged by the refactor
    And its continued reading of `checks_state` fallback is out of scope

  @unit
  Scenario: is_open helper in signal.rs remains at the callsite (or is removed if unused)
    When `crates/orchard/src/signal.rs` is inspected after migration
    Then the `is_ready_to_merge` callsite at line 427 still gates open-ness explicitly
       (either via the existing `is_open` helper or equivalent state check on `&PrState`)
    And the canonical `merge_readiness::is_ready_to_merge` does NOT perform an is_open check
    And if `fn is_open` becomes unused after migration, it is removed (no `#[allow(dead_code)]`)

  # =======================================================================
  # AC Coverage Map
  # =======================================================================
  # AC 1: "Single canonical is_ready_to_merge implementation in crates/orchard/src/merge_readiness.rs."
  #   -> @unit "merge_readiness module exists and is wired into lib.rs"
  #   -> @integration "No private is_ready_to_merge function survives anywhere in the workspace"
  #
  # AC 2: "Predicate operates on a MergeReadinessView NewType (7 fields). Two From adapters:
  #        From<&PrState> and From<&derive::PrInfo>."
  #   -> @unit "MergeReadinessView NewType exposes exactly seven predicate-relevant fields"
  #   -> @unit "MergeReadinessView fields are borrowed/copy (no owned Vec/String per row)"
  #   -> @unit "From<&PrState> for MergeReadinessView adapter exists and is total"
  #   -> @unit "From<&derive::PrInfo> for MergeReadinessView adapter exists and is total"
  #
  # AC 3: "All three previous callsites (signal.rs, classify.rs, watch/diff.rs) delete their private
  #        implementation and call merge_readiness::is_ready_to_merge(&(&pr).into())."
  #   -> @integration "signal.rs no longer defines a private is_ready_to_merge"
  #   -> @integration "classify.rs no longer defines a private is_ready_to_merge"
  #   -> @integration "watch/diff.rs no longer defines a private is_ready_to_merge"
  #   -> @integration "No private is_ready_to_merge function survives anywhere in the workspace"
  #
  # AC 4: "Canonical semantics (explicit, with tests for each):
  #         - Filter: not-approved, draft, has_conflicts, unresolved_threads > 0,
  #                   ci_code_state == 'failing', ci_gate_state ∈ {'blocked', 'pending'}.
  #         - Allow: approved + (ci_code_state ∈ {Some('passing'), None}) +
  #                  (ci_gate_state ∈ {Some('cleared'), None}).
  #         - is_open check stays at callsite (not in predicate)."
  #   -> @unit "Predicate returns false when review_decision is not Some(\"approved\")"
  #   -> @unit "Predicate returns false when is_draft is true"
  #   -> @unit "Predicate returns false when has_conflicts is true"
  #   -> @unit "Predicate returns false when unresolved_threads > 0"
  #   -> @unit "Predicate returns false when ci_code_state is \"failing\""
  #   -> @unit "Predicate returns false when ci_gate_state is \"blocked\""
  #   -> @unit "Predicate returns false when ci_gate_state is \"pending\""
  #   -> @unit "Predicate returns true when approved + ci_code_state passing + ci_gate_state cleared"
  #   -> @unit "Predicate treats ci_code_state == None as passing (None-as-passing preserved)"
  #   -> @unit "Predicate treats ci_gate_state == None as cleared"
  #   -> @unit "Predicate does NOT check is_open / pr.state"
  #   -> @unit "Predicate uses literal Some(\"approved\") matching (production cache data is lowercase)"
  #   -> @unit "is_open helper in signal.rs remains at the callsite (or is removed if unused)"
  #
  # AC 5: "watch/diff.rs migrates off checks_state to ci_code_state + ci_gate_state (via the adapter)."
  #   -> @integration "watch/diff.rs no longer reads checks_state for readiness decisions"
  #   -> @integration "Approved + code-green + gate-cleared PR still fires PrReadyToMerge in watch"
  #   -> @integration "Approved + code-green + gate-blocked PR no longer fires PrReadyToMerge in watch"
  #
  # AC 6: "Behaviour deltas surfaced in PR description:
  #         - watch/diff.rs starts filtering draft PRs.
  #         - watch/diff.rs and signal.rs start respecting ci_gate_state.
  #         - signal.rs's existing None-as-passing behaviour is preserved."
  #   -> @integration "Behaviour delta — draft+approved+passing PR no longer fires PrReadyToMerge in watch"
  #   -> @integration "Behaviour delta — signal.rs no longer routes a gate-blocked PR to Ready"
  #   -> @integration "Behaviour preserved — approved PR with no CI configured still fires Ready in signal"
  #   -> @unit "PR description documents the three behaviour deltas"
  #
  # AC 7: "No behaviour regressions for the documented 'ready' path: existing is_ready_to_merge_*
  #        tests across all three files still pass (or are migrated to assert the same outcome
  #        under the new shape)."
  #   -> @integration "Existing signal.rs ready/not-ready tests still pass under the new predicate"
  #   -> @integration "Existing classify.rs ready tests still pass under the new predicate"
  #   -> @integration "Existing watch/diff.rs ready-transition tests still pass under the new predicate"
  #   -> @e2e "cargo test --workspace passes after the refactor lands"
  #   -> @integration "Workspace builds clean with clippy and fmt"
  #
  # AC 8: "Module is ≤ 80 lines including doc comments."
  #   -> @unit "merge_readiness.rs is at most 80 lines including doc comments"
  #
  # Total ACs in issue body: 8. All 8 mapped above.
