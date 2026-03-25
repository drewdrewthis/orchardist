use std::collections::HashMap;
use std::sync::OnceLock;

use regex::Regex;

use crate::services::github::CommandGithub;
use crate::services::GithubService;
use crate::types::{IssueState, PrInfo};

// ---------------------------------------------------------------------------
// Delegating wrappers
// ---------------------------------------------------------------------------

/// Returns `(owner, name)` for the current GitHub repository.
/// The result is cached after the first successful call.
pub fn get_repo() -> anyhow::Result<(String, String)> {
    CommandGithub.get_repo()
}

/// Reports whether the `gh` CLI is authenticated and available.
pub fn is_gh_available() -> bool {
    CommandGithub.is_gh_available()
}

/// Fetches PR info for the given branches.
pub fn get_all_prs(branches: &[String]) -> HashMap<String, PrInfo> {
    CommandGithub.get_all_prs(branches)
}

/// Fetches detailed GraphQL data for up to 25 open PRs and updates `pr_map` in-place.
pub fn enrich_pr_details(pr_map: &mut HashMap<String, PrInfo>) {
    CommandGithub.enrich_pr_details(pr_map)
}

/// Fetches the open/closed/completed state for up to 25 issues via a batched
/// GraphQL query.
pub fn get_issue_states(numbers: &[u32]) -> HashMap<u32, IssueState> {
    CommandGithub.get_issue_states(numbers)
}

// ---------------------------------------------------------------------------
// Issue number extraction (pure logic, no Command calls)
// ---------------------------------------------------------------------------

fn issue_keyword_re() -> &'static Regex {
    static RE: OnceLock<Regex> = OnceLock::new();
    RE.get_or_init(|| Regex::new(r"(?i)issue[/\-]?(\d+)").unwrap())
}

fn leading_number_re() -> &'static Regex {
    static RE: OnceLock<Regex> = OnceLock::new();
    RE.get_or_init(|| Regex::new(r"^(\d+)-").unwrap())
}

fn embedded_number_re() -> &'static Regex {
    static RE: OnceLock<Regex> = OnceLock::new();
    RE.get_or_init(|| Regex::new(r"-(\d+)(?:-|$)").unwrap())
}

fn strip_prefix_re() -> &'static Regex {
    static RE: OnceLock<Regex> = OnceLock::new();
    RE.get_or_init(|| Regex::new(r"^[a-zA-Z][a-zA-Z0-9]*[/_]").unwrap())
}

/// Attempts to extract a GitHub issue number from a branch name.
/// Strips common prefixes (e.g. `feat/`, `fix/`) before matching.
pub fn extract_issue_number(branch: &str) -> Option<u32> {
    let stripped = strip_prefix_re().replace(branch, "").into_owned();

    // Keyword pattern on original and stripped.
    for candidate in &[branch, stripped.as_str()] {
        if let Some(caps) = issue_keyword_re().captures(candidate)
            && let Ok(n) = caps[1].parse::<u32>()
                && n >= 1 {
                    return Some(n);
                }
    }

    // Leading number (>= 100) on stripped.
    if let Some(caps) = leading_number_re().captures(&stripped)
        && let Ok(n) = caps[1].parse::<u32>()
            && n >= 100 {
                return Some(n);
            }

    // Embedded number (>= 100) on stripped.
    if let Some(caps) = embedded_number_re().captures(&stripped)
        && let Ok(n) = caps[1].parse::<u32>()
            && n >= 100 {
                return Some(n);
            }

    None
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

#[cfg(test)]
mod tests {
    use super::*;
    use crate::services::github::{derive_checks_status, derive_review_decision};
    use crate::types::{ChecksStatus, ReviewDecision};
    use serde_json::json;

    // --- derive_review_decision ---

    #[test]
    fn review_decision_uses_explicit_value_when_non_empty() {
        let result = derive_review_decision("APPROVED", &[]);
        assert_eq!(result, ReviewDecision::Approved);
    }

    #[test]
    fn review_decision_changes_requested_when_explicit() {
        let result = derive_review_decision("CHANGES_REQUESTED", &[]);
        assert_eq!(result, ReviewDecision::ChangesRequested);
    }

    #[test]
    fn review_decision_derives_changes_requested_from_reviews() {
        let reviews = vec![
            json!({"state": "APPROVED"}),
            json!({"state": "CHANGES_REQUESTED"}),
        ];
        let result = derive_review_decision("", &reviews);
        assert_eq!(result, ReviewDecision::ChangesRequested);
    }

    #[test]
    fn review_decision_derives_approved_from_reviews() {
        let reviews = vec![json!({"state": "APPROVED"})];
        let result = derive_review_decision("", &reviews);
        assert_eq!(result, ReviewDecision::Approved);
    }

    #[test]
    fn review_decision_returns_none_when_no_reviews() {
        let result = derive_review_decision("", &[]);
        assert_eq!(result, ReviewDecision::None);
    }

    // --- derive_checks_status ---

    #[test]
    fn checks_status_returns_none_for_empty_contexts() {
        assert_eq!(derive_checks_status(&[]), ChecksStatus::None);
    }

    #[test]
    fn checks_status_fails_on_check_run_failure() {
        let contexts = vec![json!({
            "__typename": "CheckRun",
            "status": "COMPLETED",
            "conclusion": "FAILURE"
        })];
        assert_eq!(derive_checks_status(&contexts), ChecksStatus::Fail);
    }

    #[test]
    fn checks_status_fails_on_status_context_error() {
        let contexts = vec![json!({
            "__typename": "StatusContext",
            "state": "ERROR"
        })];
        assert_eq!(derive_checks_status(&contexts), ChecksStatus::Fail);
    }

    #[test]
    fn checks_status_pending_when_check_run_not_completed() {
        let contexts = vec![json!({
            "__typename": "CheckRun",
            "status": "IN_PROGRESS",
            "conclusion": null
        })];
        assert_eq!(derive_checks_status(&contexts), ChecksStatus::Pending);
    }

    #[test]
    fn checks_status_pass_when_all_completed_success() {
        let contexts = vec![json!({
            "__typename": "CheckRun",
            "status": "COMPLETED",
            "conclusion": "SUCCESS"
        })];
        assert_eq!(derive_checks_status(&contexts), ChecksStatus::Pass);
    }

    // --- extract_issue_number ---

    #[test]
    fn extracts_issue_keyword_with_slash() {
        assert_eq!(extract_issue_number("issue/42"), Some(42));
    }

    #[test]
    fn extracts_issue_keyword_case_insensitive() {
        assert_eq!(extract_issue_number("feat/Issue-123-some-thing"), Some(123));
    }

    #[test]
    fn extracts_leading_number_above_100() {
        assert_eq!(extract_issue_number("feat/200-my-feature"), Some(200));
    }

    #[test]
    fn extracts_embedded_number_above_100() {
        assert_eq!(extract_issue_number("fix/something-150-desc"), Some(150));
    }

    #[test]
    fn returns_none_for_small_leading_number() {
        assert_eq!(extract_issue_number("feat/42-small"), None);
    }

    #[test]
    fn returns_none_for_plain_branch() {
        assert_eq!(extract_issue_number("main"), None);
    }
}
