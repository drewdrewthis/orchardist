//! Derivations over a PR's review timeline: last top-level review
//! comment, and whether the PR has pending author feedback.
//!
//! These fields are computed at serialization time (not cached) from
//! `reviews: Vec<CachedReview>`, `pr.author`, `pr.state`, and
//! `pr.last_commit_pushed_at`.

use crate::cache::CachedReview;

/// Returns `(submittedAt, author)` of the most recent review by
/// `submittedAt`. Reviews with a null `submitted_at` are ignored.
/// State-agnostic: APPROVED, CHANGES_REQUESTED, and COMMENTED all count.
pub fn last_review_comment(reviews: &[CachedReview]) -> (Option<String>, Option<String>) {
    let best = reviews
        .iter()
        .filter(|r| r.submitted_at.is_some())
        .max_by(|a, b| a.submitted_at.cmp(&b.submitted_at));

    match best {
        Some(r) => (r.submitted_at.clone(), Some(r.author.clone())),
        None => (None, None),
    }
}

/// Returns true iff the PR has an unaddressed author comment:
/// - the PR is OPEN (false for MERGED/CLOSED/None)
/// - `pr_author` is known
/// - a review exists with a known author that is NOT the PR author
/// - either `last_commit_pushed_at` is None, OR
///   `last_review_comment_at > last_commit_pushed_at` (strict, string-compare OK — ISO-8601 lexicographic).
pub fn has_unaddressed_author_comment(
    pr_state: Option<&str>,
    pr_author: Option<&str>,
    last_review_comment_at: Option<&str>,
    last_review_comment_author: Option<&str>,
    last_commit_pushed_at: Option<&str>,
) -> bool {
    // Must be an open PR
    let state = match pr_state {
        Some(s) => s,
        None => return false,
    };
    if !state.eq_ignore_ascii_case("OPEN") {
        return false;
    }

    // PR author must be known
    let author = match pr_author {
        Some(a) => a,
        None => return false,
    };

    // There must be a review from someone other than the PR author
    let reviewer = match last_review_comment_author {
        Some(r) => r,
        None => return false,
    };
    if reviewer == author {
        return false;
    }

    // The review timestamp must exist
    let review_ts = match last_review_comment_at {
        Some(ts) => ts,
        None => return false,
    };

    // If no push timestamp, any non-author review is unaddressed
    match last_commit_pushed_at {
        None => true,
        Some(push_ts) => review_ts > push_ts,
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn review(author: &str, state: &str, submitted_at: Option<&str>) -> CachedReview {
        CachedReview {
            author: author.to_string(),
            state: state.to_string(),
            submitted_at: submitted_at.map(|s| s.to_string()),
        }
    }

    #[test]
    fn last_review_comment_is_max_submitted_at_across_all_reviews() {
        let reviews = vec![
            review("alice", "APPROVED", Some("2024-01-01T10:00:00Z")),
            review("bob", "CHANGES_REQUESTED", Some("2024-01-03T12:00:00Z")),
            review("charlie", "COMMENTED", Some("2024-01-02T08:00:00Z")),
        ];
        let (ts, author) = last_review_comment(&reviews);
        assert_eq!(ts.as_deref(), Some("2024-01-03T12:00:00Z"));
        assert_eq!(author.as_deref(), Some("bob"));
    }

    #[test]
    fn last_review_comment_is_none_when_reviews_empty() {
        let (ts, author) = last_review_comment(&[]);
        assert_eq!(ts, None);
        assert_eq!(author, None);
    }

    #[test]
    fn last_review_comment_ignores_null_submitted_at() {
        let reviews = vec![
            review("alice", "APPROVED", None),
            review("bob", "COMMENTED", Some("2024-01-01T09:00:00Z")),
        ];
        let (ts, author) = last_review_comment(&reviews);
        assert_eq!(ts.as_deref(), Some("2024-01-01T09:00:00Z"));
        assert_eq!(author.as_deref(), Some("bob"));
    }

    #[test]
    fn last_review_comment_state_agnostic() {
        // All three review states should be considered
        let reviews = vec![
            review("reviewer1", "APPROVED", Some("2024-02-01T00:00:00Z")),
            review("reviewer2", "CHANGES_REQUESTED", Some("2024-02-02T00:00:00Z")),
            review("reviewer3", "COMMENTED", Some("2024-02-03T00:00:00Z")),
        ];
        let (ts, author) = last_review_comment(&reviews);
        assert_eq!(ts.as_deref(), Some("2024-02-03T00:00:00Z"));
        assert_eq!(author.as_deref(), Some("reviewer3"));
    }

    #[test]
    fn unaddressed_true_when_last_review_is_non_author_and_post_push() {
        let result = has_unaddressed_author_comment(
            Some("OPEN"),
            Some("alice"),
            Some("2024-01-05T12:00:00Z"),
            Some("bob"),
            Some("2024-01-04T10:00:00Z"),
        );
        assert!(result);
    }

    #[test]
    fn unaddressed_false_when_last_review_is_self() {
        // Reviewer is the same as PR author
        let result = has_unaddressed_author_comment(
            Some("OPEN"),
            Some("alice"),
            Some("2024-01-05T12:00:00Z"),
            Some("alice"),
            Some("2024-01-04T10:00:00Z"),
        );
        assert!(!result);
    }

    #[test]
    fn unaddressed_false_when_push_after_review() {
        // Push is more recent than review → author has addressed it
        let result = has_unaddressed_author_comment(
            Some("OPEN"),
            Some("alice"),
            Some("2024-01-04T10:00:00Z"),
            Some("bob"),
            Some("2024-01-05T12:00:00Z"),
        );
        assert!(!result);
    }

    #[test]
    fn unaddressed_false_when_review_ts_equals_push_ts() {
        // Tie → strict > so false
        let result = has_unaddressed_author_comment(
            Some("OPEN"),
            Some("alice"),
            Some("2024-01-05T10:00:00Z"),
            Some("bob"),
            Some("2024-01-05T10:00:00Z"),
        );
        assert!(!result);
    }

    #[test]
    fn unaddressed_false_when_no_reviews() {
        // No last_review_comment_at → no reviews
        let result = has_unaddressed_author_comment(
            Some("OPEN"),
            Some("alice"),
            None,
            None,
            Some("2024-01-04T10:00:00Z"),
        );
        assert!(!result);
    }

    #[test]
    fn unaddressed_true_when_push_ts_is_null_and_non_author_review_exists() {
        // No push timestamp → any non-author review counts
        let result = has_unaddressed_author_comment(
            Some("OPEN"),
            Some("alice"),
            Some("2024-01-05T12:00:00Z"),
            Some("bob"),
            None,
        );
        assert!(result);
    }

    #[test]
    fn unaddressed_false_when_pr_author_is_null() {
        let result = has_unaddressed_author_comment(
            Some("OPEN"),
            None,
            Some("2024-01-05T12:00:00Z"),
            Some("bob"),
            None,
        );
        assert!(!result);
    }

    #[test]
    fn unaddressed_false_when_pr_state_is_merged() {
        let result = has_unaddressed_author_comment(
            Some("MERGED"),
            Some("alice"),
            Some("2024-01-05T12:00:00Z"),
            Some("bob"),
            None,
        );
        assert!(!result);
    }

    #[test]
    fn unaddressed_false_when_pr_state_is_closed() {
        let result = has_unaddressed_author_comment(
            Some("CLOSED"),
            Some("alice"),
            Some("2024-01-05T12:00:00Z"),
            Some("bob"),
            None,
        );
        assert!(!result);
    }

    #[test]
    fn unaddressed_false_when_pr_state_is_none() {
        let result = has_unaddressed_author_comment(
            None,
            Some("alice"),
            Some("2024-01-05T12:00:00Z"),
            Some("bob"),
            None,
        );
        assert!(!result);
    }
}
