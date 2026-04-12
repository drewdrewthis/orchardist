//! Pure classification and rollup logic for split CI state.
//!
//! This module provides the data types and pure functions that implement the
//! "code vs gate" CI check classification introduced in issue #218. No I/O
//! occurs here — all functions are fully deterministic and unit-testable.
//!
//! # Overview
//!
//! Checks are classified into two buckets:
//! - **code** — ordinary CI (tests, lint, build). A failing code check means
//!   the PR is broken and needs a fix.
//! - **gate** — process / policy checks (e.g. `check-approval-or-label`,
//!   `license/cla`). A failing gate check means the PR is waiting on a human
//!   action, not necessarily broken code.
//!
//! The two rollup functions (`rollup_code_state`, `rollup_gate_state`) collapse
//! a bucket's `CheckInfo` vec into a single canonical state string, letting
//! consumers distinguish "code green, gate blocked" from "code broken".

use globset::{GlobBuilder, GlobSetBuilder};
use serde::{Deserialize, Serialize};

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

/// A single CI check with its normalized state.
///
/// `state` values: `"passing"`, `"failing"`, `"pending"`.
#[derive(Debug, Clone, Default, PartialEq, Eq, Serialize, Deserialize)]
pub struct CheckInfo {
    /// Name of the check (from `CheckRun.name` or `StatusContext.context`).
    pub name: String,
    /// Normalized state: `"passing"`, `"failing"`, or `"pending"`.
    pub state: String,
}

/// Two-bucket classification of all CI checks on a PR.
///
/// `code` holds ordinary CI checks; `gate` holds process/policy gate checks
/// whose patterns are configured in `GlobalConfig.ci_gate_patterns`.
/// There is no `ignored` bucket in v1 — that is reserved for a follow-up issue.
#[derive(Debug, Clone, Default, PartialEq, Eq, Serialize, Deserialize)]
pub struct CiChecks {
    /// Checks classified as code CI (tests, lint, build, etc.).
    pub code: Vec<CheckInfo>,
    /// Checks classified as gate/policy checks (approval, license, etc.).
    pub gate: Vec<CheckInfo>,
}

/// Which bucket a check belongs to.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum CheckBucket {
    /// Ordinary code CI check.
    Code,
    /// Process/policy gate check.
    Gate,
}

// ---------------------------------------------------------------------------
// GateMatcher
// ---------------------------------------------------------------------------

/// Compiled glob-set for matching gate-pattern check names.
///
/// Patterns are case-insensitive. Exact-match patterns (no glob metacharacters)
/// are treated as literal globs. Patterns containing `*` use globset semantics
/// where a single `*` does **not** cross `/` directory separators; `**` does.
///
/// # Construction
///
/// ```
/// use orchard::ci_state::GateMatcher;
/// let m = GateMatcher::new(&["check-approval-or-label".to_string(), "license/*".to_string()]);
/// ```
pub struct GateMatcher {
    set: globset::GlobSet,
}

impl GateMatcher {
    /// Builds a `GateMatcher` from a list of pattern strings.
    ///
    /// Patterns are compiled case-insensitively. Returns an empty matcher (no
    /// checks classified as gate) if `patterns` is empty or if any pattern
    /// fails to compile (the invalid pattern is skipped with a debug message).
    pub fn new(patterns: &[String]) -> Self {
        let mut builder = GlobSetBuilder::new();
        for pattern in patterns {
            let glob_result = GlobBuilder::new(pattern)
                .case_insensitive(true)
                .literal_separator(true)
                .build();
            match glob_result {
                Ok(glob) => {
                    builder.add(glob);
                }
                Err(e) => {
                    // Skip invalid patterns — don't panic at runtime. Routed
                    // through the shared logger so the warning surfaces in the
                    // orchard log file rather than vanishing into stderr.
                    crate::logger::LOG.warn(&format!(
                        "ci_state: skipping invalid gate pattern {pattern:?}: {e}"
                    ));
                }
            }
        }
        let set = builder.build().unwrap_or_else(|_| {
            GlobSetBuilder::new()
                .build()
                .expect("empty globset always builds")
        });
        Self { set }
    }

    /// Returns `true` if `name` matches any gate pattern.
    pub fn is_gate(&self, name: &str) -> bool {
        self.set.is_match(name)
    }
}

// ---------------------------------------------------------------------------
// Classification
// ---------------------------------------------------------------------------

/// Classifies a check name as `Code` or `Gate` based on the configured patterns.
///
/// Matching is case-insensitive. A single `*` in a pattern does not cross `/`;
/// use `**` for recursive matching.
///
/// # Examples
///
/// ```
/// use orchard::ci_state::{GateMatcher, classify_check, CheckBucket};
/// let matcher = GateMatcher::new(&["check-approval-or-label".to_string()]);
/// assert_eq!(classify_check("check-approval-or-label", &matcher), CheckBucket::Gate);
/// assert_eq!(classify_check("test-unit", &matcher), CheckBucket::Code);
/// ```
pub fn classify_check(name: &str, gate_patterns: &GateMatcher) -> CheckBucket {
    if gate_patterns.is_gate(name) {
        CheckBucket::Gate
    } else {
        CheckBucket::Code
    }
}

// ---------------------------------------------------------------------------
// Rollup functions
// ---------------------------------------------------------------------------

/// Rolls up a slice of code checks into a single state string.
///
/// Priority: `failing` dominates, then `pending`, then `passing`.
/// Returns `None` when `checks` is empty (no code CI on this PR).
///
/// | Condition                        | Returns         |
/// |----------------------------------|-----------------|
/// | Any check has state `"failing"`  | `Some("failing")` |
/// | No failing, at least one pending | `Some("pending")` |
/// | All passing                      | `Some("passing")` |
/// | Empty slice                      | `None`            |
pub fn rollup_code_state(checks: &[CheckInfo]) -> Option<String> {
    if checks.is_empty() {
        return None;
    }
    let has_failing = checks.iter().any(|c| c.state == "failing");
    let has_pending = checks.iter().any(|c| c.state == "pending");
    if has_failing {
        Some("failing".to_string())
    } else if has_pending {
        Some("pending".to_string())
    } else {
        Some("passing".to_string())
    }
}

/// Rolls up a slice of gate checks into a single state string.
///
/// Priority: `blocked` (any failing) dominates, then `pending`, then `cleared`.
/// Returns `None` when `checks` is empty (no gate checks on this PR).
///
/// The four-valued semantics are important: `pending` distinguishes
/// "Mintlify preview still building" from "approval check explicitly failed".
///
/// | Condition                        | Returns           |
/// |----------------------------------|-------------------|
/// | Any check has state `"failing"`  | `Some("blocked")` |
/// | No failing, at least one pending | `Some("pending")` |
/// | All passing                      | `Some("cleared")` |
/// | Empty slice                      | `None`            |
pub fn rollup_gate_state(checks: &[CheckInfo]) -> Option<String> {
    if checks.is_empty() {
        return None;
    }
    let has_failing = checks.iter().any(|c| c.state == "failing");
    let has_pending = checks.iter().any(|c| c.state == "pending");
    if has_failing {
        Some("blocked".to_string())
    } else if has_pending {
        Some("pending".to_string())
    } else {
        Some("cleared".to_string())
    }
}

// ---------------------------------------------------------------------------
// GraphQL mapping functions
// ---------------------------------------------------------------------------

/// Maps a GraphQL `CheckRun` conclusion (and optional status) to a normalized state.
///
/// # Mapping table
///
/// | Conclusion            | State     |
/// |-----------------------|-----------|
/// | `SUCCESS`, `NEUTRAL`  | `passing` |
/// | `FAILURE`, `TIMED_OUT`, `ACTION_REQUIRED` | `failing` |
/// | `SKIPPED`, `CANCELLED`, `STALE` | `None` (omitted from rollup) |
/// | `None` + `IN_PROGRESS` status | `pending` |
/// | `None` + any other status | `None` |
///
/// NEUTRAL is treated as passing because it is GitHub's "opinionated pass" used
/// by bots that ran but made no judgment call.
/// SKIPPED/CANCELLED/STALE are omitted — they are neither pass nor fail signals.
pub fn map_check_run_conclusion(conclusion: Option<&str>, status: Option<&str>) -> Option<String> {
    match conclusion {
        Some(c) => match c {
            "SUCCESS" | "NEUTRAL" => Some("passing".to_string()),
            "FAILURE" | "TIMED_OUT" | "ACTION_REQUIRED" => Some("failing".to_string()),
            "SKIPPED" | "CANCELLED" | "STALE" => None,
            _ => None,
        },
        None => {
            // Null conclusion: check status to distinguish in-progress from other.
            if status == Some("IN_PROGRESS") {
                Some("pending".to_string())
            } else {
                None
            }
        }
    }
}

/// Maps a GraphQL `StatusContext` state to a normalized check state.
///
/// | State               | Result    |
/// |---------------------|-----------|
/// | `SUCCESS`, `EXPECTED` | `passing` |
/// | `FAILURE`, `ERROR`    | `failing` |
/// | `PENDING`             | `pending` |
/// | anything else         | `None`    |
pub fn map_status_context_state(state: &str) -> Option<String> {
    match state {
        "SUCCESS" | "EXPECTED" => Some("passing".to_string()),
        "FAILURE" | "ERROR" => Some("failing".to_string()),
        "PENDING" => Some("pending".to_string()),
        _ => None,
    }
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

#[cfg(test)]
mod tests {
    use super::*;

    // -----------------------------------------------------------------------
    // Classification tests (feature scenarios #25-30)
    // -----------------------------------------------------------------------

    #[test]
    fn classify_exact_match_is_gate() {
        let m = GateMatcher::new(&["check-approval-or-label".to_string()]);
        assert_eq!(
            classify_check("check-approval-or-label", &m),
            CheckBucket::Gate
        );
    }

    #[test]
    fn classify_exact_match_is_case_insensitive() {
        let m = GateMatcher::new(&["Mintlify Deployment".to_string()]);
        assert_eq!(classify_check("mintlify deployment", &m), CheckBucket::Gate);
    }

    #[test]
    fn classify_shallow_glob_matches_single_level() {
        let m = GateMatcher::new(&["license/*".to_string()]);
        assert_eq!(classify_check("license/cla", &m), CheckBucket::Gate);
    }

    #[test]
    fn classify_single_star_does_not_cross_slash() {
        let m = GateMatcher::new(&["license/*".to_string()]);
        // "license/cla/v2" has two path segments after "license/" — single * must not match.
        assert_eq!(classify_check("license/cla/v2", &m), CheckBucket::Code);
    }

    #[test]
    fn classify_double_star_matches_recursively() {
        let m = GateMatcher::new(&["license/**".to_string()]);
        assert_eq!(classify_check("license/cla/v2", &m), CheckBucket::Gate);
    }

    #[test]
    fn classify_no_match_is_code() {
        let m = GateMatcher::new(&[
            "check-approval-or-label".to_string(),
            "license/*".to_string(),
        ]);
        assert_eq!(classify_check("test-unit", &m), CheckBucket::Code);
    }

    /// Malformed glob patterns must not panic or poison the matcher — the
    /// invalid pattern is logged and skipped, and the matcher continues to
    /// honor the remaining valid patterns.
    #[test]
    fn gate_matcher_skips_invalid_pattern_and_preserves_others() {
        let patterns = vec![
            "[".to_string(), // invalid: unclosed character class
            "check-approval-or-label".to_string(),
        ];
        let m = GateMatcher::new(&patterns);
        // The valid pattern still matches.
        assert_eq!(
            classify_check("check-approval-or-label", &m),
            CheckBucket::Gate
        );
        // The invalid pattern produces no match surface (it was dropped).
        assert_eq!(classify_check("[", &m), CheckBucket::Code);
    }

    // -----------------------------------------------------------------------
    // ci_code_state rollup (feature scenario #31)
    // -----------------------------------------------------------------------

    fn check(state: &str) -> CheckInfo {
        CheckInfo {
            name: "x".to_string(),
            state: state.to_string(),
        }
    }

    #[test]
    fn code_rollup_all_passing_returns_passing() {
        let checks = vec![check("passing"), check("passing"), check("passing")];
        assert_eq!(rollup_code_state(&checks), Some("passing".to_string()));
    }

    #[test]
    fn code_rollup_any_failing_returns_failing() {
        let checks = vec![check("passing"), check("failing"), check("passing")];
        assert_eq!(rollup_code_state(&checks), Some("failing".to_string()));
    }

    #[test]
    fn code_rollup_failing_dominates_pending() {
        let checks = vec![check("failing"), check("pending")];
        assert_eq!(rollup_code_state(&checks), Some("failing".to_string()));
    }

    #[test]
    fn code_rollup_pending_when_no_failures() {
        let checks = vec![check("passing"), check("pending")];
        assert_eq!(rollup_code_state(&checks), Some("pending".to_string()));
    }

    #[test]
    fn code_rollup_empty_returns_none() {
        assert_eq!(rollup_code_state(&[]), None);
    }

    // -----------------------------------------------------------------------
    // ci_gate_state rollup (feature scenario #32)
    // -----------------------------------------------------------------------

    #[test]
    fn gate_rollup_all_passing_returns_cleared() {
        let checks = vec![check("passing"), check("passing")];
        assert_eq!(rollup_gate_state(&checks), Some("cleared".to_string()));
    }

    #[test]
    fn gate_rollup_any_failing_returns_blocked() {
        let checks = vec![check("passing"), check("failing")];
        assert_eq!(rollup_gate_state(&checks), Some("blocked".to_string()));
    }

    #[test]
    fn gate_rollup_failing_dominates_pending() {
        let checks = vec![check("failing"), check("pending")];
        assert_eq!(rollup_gate_state(&checks), Some("blocked".to_string()));
    }

    #[test]
    fn gate_rollup_pending_when_no_failures() {
        let checks = vec![check("passing"), check("pending")];
        assert_eq!(rollup_gate_state(&checks), Some("pending".to_string()));
    }

    #[test]
    fn gate_rollup_empty_returns_none() {
        assert_eq!(rollup_gate_state(&[]), None);
    }

    // -----------------------------------------------------------------------
    // CheckRun conclusion mapping (feature scenario #33 + #34)
    // -----------------------------------------------------------------------

    fn map_conclusion(conclusion: &str) -> Option<String> {
        map_check_run_conclusion(Some(conclusion), None)
    }

    #[test]
    fn conclusion_success_maps_to_passing() {
        assert_eq!(map_conclusion("SUCCESS"), Some("passing".to_string()));
    }

    #[test]
    fn conclusion_neutral_maps_to_passing() {
        assert_eq!(map_conclusion("NEUTRAL"), Some("passing".to_string()));
    }

    #[test]
    fn conclusion_failure_maps_to_failing() {
        assert_eq!(map_conclusion("FAILURE"), Some("failing".to_string()));
    }

    #[test]
    fn conclusion_timed_out_maps_to_failing() {
        assert_eq!(map_conclusion("TIMED_OUT"), Some("failing".to_string()));
    }

    #[test]
    fn conclusion_action_required_maps_to_failing() {
        assert_eq!(
            map_conclusion("ACTION_REQUIRED"),
            Some("failing".to_string())
        );
    }

    #[test]
    fn conclusion_skipped_maps_to_none() {
        assert_eq!(map_conclusion("SKIPPED"), None);
    }

    #[test]
    fn conclusion_cancelled_maps_to_none() {
        assert_eq!(map_conclusion("CANCELLED"), None);
    }

    #[test]
    fn conclusion_stale_maps_to_none() {
        assert_eq!(map_conclusion("STALE"), None);
    }

    #[test]
    fn null_conclusion_with_in_progress_status_maps_to_pending() {
        // Scenario #34: null conclusion + IN_PROGRESS → pending
        assert_eq!(
            map_check_run_conclusion(None, Some("IN_PROGRESS")),
            Some("pending".to_string())
        );
    }

    // -----------------------------------------------------------------------
    // StatusContext state mapping (feature scenario #35)
    // -----------------------------------------------------------------------

    #[test]
    fn status_context_success_maps_to_passing() {
        assert_eq!(
            map_status_context_state("SUCCESS"),
            Some("passing".to_string())
        );
    }

    #[test]
    fn status_context_expected_maps_to_passing() {
        assert_eq!(
            map_status_context_state("EXPECTED"),
            Some("passing".to_string())
        );
    }

    #[test]
    fn status_context_failure_maps_to_failing() {
        assert_eq!(
            map_status_context_state("FAILURE"),
            Some("failing".to_string())
        );
    }

    #[test]
    fn status_context_error_maps_to_failing() {
        assert_eq!(
            map_status_context_state("ERROR"),
            Some("failing".to_string())
        );
    }

    #[test]
    fn status_context_pending_maps_to_pending() {
        assert_eq!(
            map_status_context_state("PENDING"),
            Some("pending".to_string())
        );
    }
}
