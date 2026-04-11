//! Unit tests for [`crate::webhook::normalize`].
//!
//! Covers all scenarios from `specs/features/webhook-event-stream.feature`
//! lines 121–227 (criterion tasks #16–#24).

use super::{normalize, NormalizeResult, NormalizedEvent};
use chrono::{DateTime, Utc};
use serde_json::{json, Value};

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

fn pr_payload(action: &str, merged: bool, pr_number: u64, repo: &str, actor: &str) -> Value {
    json!({
        "action": action,
        "pull_request": { "number": pr_number, "merged": merged },
        "repository": { "full_name": repo },
        "sender": { "login": actor }
    })
}

fn assert_event(result: NormalizeResult) -> NormalizedEvent {
    match result {
        NormalizeResult::Event(e) => e,
        NormalizeResult::Unsupported => panic!("expected Event, got Unsupported"),
        NormalizeResult::Unknown => panic!("expected Event, got Unknown"),
    }
}

fn assert_unsupported(result: NormalizeResult) {
    match result {
        NormalizeResult::Unsupported => {}
        NormalizeResult::Event(_) => panic!("expected Unsupported, got Event"),
        NormalizeResult::Unknown => panic!("expected Unsupported, got Unknown"),
    }
}

// ---------------------------------------------------------------------------
// #16: full-fields scenario
// ---------------------------------------------------------------------------

#[test]
fn pull_request_opened_has_all_fields() {
    let payload = pr_payload("opened", false, 42, "acme/webapp", "octocat");
    let event = assert_event(normalize("pull_request", &payload));
    assert_eq!(event.source, "webhook");
    assert_eq!(event.kind, "pull_request.opened");
    assert_eq!(event.repo.as_deref(), Some("acme/webapp"));
    assert_eq!(event.pr, Some(42));
    assert_eq!(event.actor.as_deref(), Some("octocat"));
    let ts_str = serde_json::to_value(event.ts).unwrap().as_str().unwrap().to_string();
    ts_str.parse::<DateTime<Utc>>().expect("ts is ISO 8601 UTC");
    assert_eq!(event.data, payload);
}

// ---------------------------------------------------------------------------
// #17: pull_request action map
// ---------------------------------------------------------------------------

#[test]
fn pull_request_closed_not_merged() {
    let event = assert_event(normalize("pull_request", &pr_payload("closed", false, 1, "a/b", "x")));
    assert_eq!(event.kind, "pull_request.closed");
}

#[test]
fn pull_request_closed_and_merged() {
    let event = assert_event(normalize("pull_request", &pr_payload("closed", true, 1, "a/b", "x")));
    assert_eq!(event.kind, "pull_request.merged");
}

#[test]
fn pull_request_reopened() {
    let event = assert_event(normalize("pull_request", &pr_payload("reopened", false, 1, "a/b", "x")));
    assert_eq!(event.kind, "pull_request.reopened");
}

#[test]
fn pull_request_ready_for_review() {
    let event = assert_event(normalize("pull_request", &pr_payload("ready_for_review", false, 1, "a/b", "x")));
    assert_eq!(event.kind, "pull_request.ready_for_review");
}

#[test]
fn pull_request_converted_to_draft() {
    let event = assert_event(normalize("pull_request", &pr_payload("converted_to_draft", false, 1, "a/b", "x")));
    assert_eq!(event.kind, "pull_request.converted_to_draft");
}

// ---------------------------------------------------------------------------
// #18: pull_request_review
// ---------------------------------------------------------------------------

#[test]
fn pull_request_review_submitted() {
    let payload = json!({
        "action": "submitted",
        "pull_request": { "number": 99 },
        "repository": { "full_name": "acme/webapp" },
        "sender": { "login": "reviewer" }
    });
    let event = assert_event(normalize("pull_request_review", &payload));
    assert_eq!(event.kind, "pull_request.review.submitted");
    assert_eq!(event.pr, Some(99));
}

// ---------------------------------------------------------------------------
// #19: pull_request_review_comment
// ---------------------------------------------------------------------------

#[test]
fn pull_request_review_comment_created() {
    let payload = json!({
        "action": "created",
        "pull_request": { "number": 3099 },
        "repository": { "full_name": "acme/webapp" },
        "sender": { "login": "coderabbitai[bot]" }
    });
    let event = assert_event(normalize("pull_request_review_comment", &payload));
    assert_eq!(event.kind, "pull_request.review_comment.created");
    assert_eq!(event.pr, Some(3099));
    assert_eq!(event.actor.as_deref(), Some("coderabbitai[bot]"));
}

// ---------------------------------------------------------------------------
// #20: issue_comment
// ---------------------------------------------------------------------------

#[test]
fn issue_comment_on_pr_populates_pr_and_issue() {
    let payload = json!({
        "action": "created",
        "issue": {
            "number": 42,
            "pull_request": { "url": "https://api.github.com/repos/acme/webapp/pulls/42" }
        },
        "repository": { "full_name": "acme/webapp" },
        "sender": { "login": "commenter" }
    });
    let event = assert_event(normalize("issue_comment", &payload));
    assert_eq!(event.kind, "issue_comment.created");
    assert_eq!(event.pr, Some(42));
    assert_eq!(event.issue, Some(42));
}

#[test]
fn issue_comment_on_plain_issue_omits_pr() {
    let payload = json!({
        "action": "created",
        "issue": { "number": 77 },
        "repository": { "full_name": "acme/webapp" },
        "sender": { "login": "commenter" }
    });
    let event = assert_event(normalize("issue_comment", &payload));
    assert_eq!(event.kind, "issue_comment.created");
    assert_eq!(event.issue, Some(77));
    assert!(event.pr.is_none());
}

// ---------------------------------------------------------------------------
// #21: issues action map
// ---------------------------------------------------------------------------

fn issues_payload(action: &str) -> Value {
    json!({
        "action": action,
        "issue": { "number": 77 },
        "repository": { "full_name": "acme/webapp" },
        "sender": { "login": "actor" }
    })
}

#[test]
fn issues_opened() {
    let event = assert_event(normalize("issues", &issues_payload("opened")));
    assert_eq!(event.kind, "issues.opened");
    assert_eq!(event.issue, Some(77));
}

#[test]
fn issues_closed() {
    assert_eq!(assert_event(normalize("issues", &issues_payload("closed"))).kind, "issues.closed");
}

#[test]
fn issues_labeled() {
    assert_eq!(assert_event(normalize("issues", &issues_payload("labeled"))).kind, "issues.labeled");
}

#[test]
fn issues_unlabeled() {
    assert_eq!(assert_event(normalize("issues", &issues_payload("unlabeled"))).kind, "issues.unlabeled");
}

// ---------------------------------------------------------------------------
// #22: push
// ---------------------------------------------------------------------------

#[test]
fn push_carries_ref_and_commits() {
    let payload = json!({
        "ref": "refs/heads/main",
        "commits": [{"id": "a"}, {"id": "b"}, {"id": "c"}],
        "repository": { "full_name": "acme/webapp" },
        "sender": { "login": "pusher" }
    });
    let event = assert_event(normalize("push", &payload));
    assert_eq!(event.kind, "push");
    assert_eq!(event.repo.as_deref(), Some("acme/webapp"));
    assert_eq!(event.data["ref"].as_str(), Some("refs/heads/main"));
    assert_eq!(event.data["commits"].as_array().map(|a| a.len()), Some(3));
}

// ---------------------------------------------------------------------------
// #23: CI completions
// ---------------------------------------------------------------------------

fn ci_completed_payload(conclusion: &str) -> Value {
    json!({
        "action": "completed",
        "conclusion": conclusion,
        "repository": { "full_name": "acme/webapp" },
        "sender": { "login": "actor" }
    })
}

#[test]
fn check_run_completed() {
    let event = assert_event(normalize("check_run", &ci_completed_payload("failure")));
    assert_eq!(event.kind, "check_run.completed");
    assert_eq!(event.data["conclusion"].as_str(), Some("failure"));
}

#[test]
fn check_suite_completed() {
    let event = assert_event(normalize("check_suite", &ci_completed_payload("success")));
    assert_eq!(event.kind, "check_suite.completed");
    assert_eq!(event.data["conclusion"].as_str(), Some("success"));
}

#[test]
fn workflow_run_completed() {
    let event = assert_event(normalize("workflow_run", &ci_completed_payload("success")));
    assert_eq!(event.kind, "workflow_run.completed");
    assert_eq!(event.data["conclusion"].as_str(), Some("success"));
}

// ---------------------------------------------------------------------------
// #24: unsupported actions
// ---------------------------------------------------------------------------

#[test]
fn pull_request_assigned_is_unsupported() {
    assert_unsupported(normalize("pull_request", &pr_payload("assigned", false, 1, "a/b", "x")));
}

#[test]
fn check_run_created_is_unsupported() {
    assert_unsupported(normalize("check_run", &json!({ "action": "created" })));
}

#[test]
fn check_suite_requested_is_unsupported() {
    assert_unsupported(normalize("check_suite", &json!({ "action": "requested" })));
}

#[test]
fn workflow_run_requested_is_unsupported() {
    assert_unsupported(normalize("workflow_run", &json!({ "action": "requested" })));
}

#[test]
fn unknown_event_type_is_unknown() {
    match normalize("star", &json!({ "action": "created" })) {
        NormalizeResult::Unknown => {}
        _ => panic!("expected Unknown for 'star' event"),
    }
}
