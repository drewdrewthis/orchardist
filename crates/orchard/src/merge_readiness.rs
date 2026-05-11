//! Canonical merge-readiness predicate, shared by `classify`, `signal`, and `watch::diff`.
//!
//! All callers convert their local PR type into [`MergeReadinessView`] via the provided
//! `From` adapters. `is_open` / PR state checks are the callsite's responsibility.

/// Minimal borrowed projection of the seven predicate-relevant PR fields.
///
/// No owned `String` or `Vec` — zero heap allocation in the per-row hot path.
pub struct MergeReadinessView<'a> {
    /// PR state (e.g. `"OPEN"`). Not inspected by the predicate.
    pub state: Option<&'a str>,
    /// Whether the PR is a draft.
    pub is_draft: bool,
    /// Whether the PR has merge conflicts.
    pub has_conflicts: bool,
    /// Count of unresolved review threads.
    pub unresolved_threads: u32,
    /// Review decision (e.g. `Some("approved")`).
    pub review_decision: Option<&'a str>,
    /// Code CI rollup (`Some("passing")` / `Some("failing")` / `None` = passing).
    pub ci_code_state: Option<&'a str>,
    /// Gate/policy check rollup (`Some("cleared")` / `Some("blocked")` / `None` = cleared).
    pub ci_gate_state: Option<&'a str>,
}

impl<'a> From<&'a crate::orchard_state::PrState> for MergeReadinessView<'a> {
    fn from(pr: &'a crate::orchard_state::PrState) -> Self {
        Self {
            state: pr.state.as_deref(),
            is_draft: pr.is_draft.unwrap_or(false),
            has_conflicts: pr.has_conflicts,
            unresolved_threads: pr.unresolved_threads,
            review_decision: pr.review_decision.as_deref(),
            ci_code_state: pr.ci_code_state.as_deref(),
            ci_gate_state: pr.ci_gate_state.as_deref(),
        }
    }
}

impl<'a> From<&'a crate::derive::PrInfo> for MergeReadinessView<'a> {
    fn from(pr: &'a crate::derive::PrInfo) -> Self {
        Self {
            state: pr.state.as_deref(),
            is_draft: pr.is_draft.unwrap_or(false),
            has_conflicts: pr.has_conflicts,
            unresolved_threads: pr.unresolved_threads,
            review_decision: pr.review_decision.as_deref(),
            ci_code_state: pr.ci_code_state.as_deref(),
            ci_gate_state: pr.ci_gate_state.as_deref(),
        }
    }
}

/// Returns `true` when a PR is approved, CI-clean, conflict-free, and thread-free.
///
/// Does **not** check whether the PR is open — that is the caller's responsibility.
///
/// - `review_decision` must be `Some("approved")` (literal; production cache is lowercase).
/// - `is_draft`, `has_conflicts`, `unresolved_threads > 0` → false.
/// - `ci_code_state == Some("failing")` → false; `None` = passing (no code CI).
/// - `ci_gate_state ∈ {"blocked","pending"}` → false; `None` = cleared (no gate).
pub fn is_ready_to_merge(view: &MergeReadinessView<'_>) -> bool {
    if view.review_decision != Some("approved") {
        return false;
    }
    if view.is_draft || view.has_conflicts || view.unresolved_threads > 0 {
        return false;
    }
    if !matches!(view.ci_code_state, Some("passing") | None) {
        return false;
    }
    matches!(view.ci_gate_state, Some("cleared") | None)
}
