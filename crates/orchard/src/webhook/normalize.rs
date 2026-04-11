//! GitHub webhook payload normalisation.
//!
//! Converts raw GitHub webhook payloads into a compact, uniform schema
//! that downstream consumers (events.jsonl tailer, watch daemon) can handle
//! without needing to understand every GitHub event type.
//!
//! See `specs/features/webhook-event-stream.feature` lines 121–227 for the
//! full acceptance contract.

use chrono::{DateTime, Utc};
use serde::Serialize;
use serde_json::Value;

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

/// A normalised GitHub webhook event ready to be written as a JSONL line.
#[derive(Debug, Clone, Serialize)]
pub struct NormalizedEvent {
    /// UTC timestamp when the event was received.
    pub ts: DateTime<Utc>,
    /// Always `"webhook"` — distinguishes these lines from task/session events.
    pub source: &'static str,
    /// Event kind identifier, e.g. `"pull_request.opened"`.
    pub kind: String,
    /// GitHub repository full name (`owner/repo`), when available.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub repo: Option<String>,
    /// Pull request number, when the event is PR-related.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub pr: Option<u64>,
    /// Issue number, when the event is issue-related.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub issue: Option<u64>,
    /// GitHub login of the actor who triggered the event.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub actor: Option<String>,
    /// Full raw GitHub payload, passed through verbatim.
    pub data: Value,
}

/// Result of normalising a GitHub webhook payload.
pub enum NormalizeResult {
    /// The event was recognised and normalised successfully.
    Event(NormalizedEvent),
    /// The event type is known but the specific action is not in scope
    /// (e.g. `pull_request.assigned`). Nothing should be written to the log.
    Unsupported,
    /// The event type is not recognised at all (e.g. `"star"`, `"fork"`).
    Unknown,
}

// ---------------------------------------------------------------------------
// Public API
// ---------------------------------------------------------------------------

/// Normalise a GitHub webhook into a [`NormalizedEvent`].
///
/// `event_type` is the value of the `X-GitHub-Event` header.
/// `payload` is the parsed JSON body — passed through verbatim into `data`.
///
/// Returns [`NormalizeResult::Unsupported`] for known-but-out-of-scope
/// actions and [`NormalizeResult::Unknown`] for unrecognised event types.
pub fn normalize(event_type: &str, payload: &Value) -> NormalizeResult {
    let repo = string_field(payload, &["repository", "full_name"]);
    let actor = string_field(payload, &["sender", "login"]);
    let action = string_field(payload, &["action"]).unwrap_or_default();

    match event_type {
        "pull_request" => normalize_pull_request(&action, payload, repo, actor),
        "pull_request_review" => normalize_pull_request_review(&action, payload, repo, actor),
        "pull_request_review_comment" => {
            normalize_pull_request_review_comment(&action, payload, repo, actor)
        }
        "issue_comment" => normalize_issue_comment(&action, payload, repo, actor),
        "issues" => normalize_issues(&action, payload, repo, actor),
        "push" => normalize_push(payload, repo, actor),
        "check_run" | "check_suite" | "workflow_run" => {
            normalize_ci_event(event_type, &action, payload, repo, actor)
        }
        _ => NormalizeResult::Unknown,
    }
}

// ---------------------------------------------------------------------------
// Per-event normalisers
// ---------------------------------------------------------------------------

fn normalize_pull_request(
    action: &str,
    payload: &Value,
    repo: Option<String>,
    actor: Option<String>,
) -> NormalizeResult {
    let kind = match action {
        "opened" => "pull_request.opened",
        "reopened" => "pull_request.reopened",
        "ready_for_review" => "pull_request.ready_for_review",
        "converted_to_draft" => "pull_request.converted_to_draft",
        "closed" => {
            let merged = payload
                .get("pull_request")
                .and_then(|pr| pr.get("merged"))
                .and_then(Value::as_bool)
                .unwrap_or(false);
            if merged {
                "pull_request.merged"
            } else {
                "pull_request.closed"
            }
        }
        _ => return NormalizeResult::Unsupported,
    };

    let pr = u64_field(payload, &["pull_request", "number"]);
    NormalizeResult::Event(build_event(kind.to_string(), repo, pr, None, actor, payload))
}

fn normalize_pull_request_review(
    action: &str,
    payload: &Value,
    repo: Option<String>,
    actor: Option<String>,
) -> NormalizeResult {
    if action != "submitted" {
        return NormalizeResult::Unsupported;
    }
    let pr = u64_field(payload, &["pull_request", "number"]);
    NormalizeResult::Event(build_event(
        "pull_request.review.submitted".to_string(),
        repo,
        pr,
        None,
        actor,
        payload,
    ))
}

fn normalize_pull_request_review_comment(
    action: &str,
    payload: &Value,
    repo: Option<String>,
    actor: Option<String>,
) -> NormalizeResult {
    if action != "created" {
        return NormalizeResult::Unsupported;
    }
    let pr = u64_field(payload, &["pull_request", "number"]);
    NormalizeResult::Event(build_event(
        "pull_request.review_comment.created".to_string(),
        repo,
        pr,
        None,
        actor,
        payload,
    ))
}

fn normalize_issue_comment(
    action: &str,
    payload: &Value,
    repo: Option<String>,
    actor: Option<String>,
) -> NormalizeResult {
    if action != "created" {
        return NormalizeResult::Unsupported;
    }
    let issue_number = u64_field(payload, &["issue", "number"]);
    let is_pr = payload
        .get("issue")
        .and_then(|i| i.get("pull_request"))
        .is_some();
    let pr = if is_pr { issue_number } else { None };

    NormalizeResult::Event(build_event(
        "issue_comment.created".to_string(),
        repo,
        pr,
        issue_number,
        actor,
        payload,
    ))
}

fn normalize_issues(
    action: &str,
    payload: &Value,
    repo: Option<String>,
    actor: Option<String>,
) -> NormalizeResult {
    let kind = match action {
        "opened" | "closed" | "labeled" | "unlabeled" => format!("issues.{}", action),
        _ => return NormalizeResult::Unsupported,
    };
    let issue = u64_field(payload, &["issue", "number"]);
    NormalizeResult::Event(build_event(kind, repo, None, issue, actor, payload))
}

fn normalize_push(payload: &Value, repo: Option<String>, actor: Option<String>) -> NormalizeResult {
    NormalizeResult::Event(build_event(
        "push".to_string(),
        repo,
        None,
        None,
        actor,
        payload,
    ))
}

fn normalize_ci_event(
    event_type: &str,
    action: &str,
    payload: &Value,
    repo: Option<String>,
    actor: Option<String>,
) -> NormalizeResult {
    if action != "completed" {
        return NormalizeResult::Unsupported;
    }
    let kind = format!("{}.completed", event_type);
    NormalizeResult::Event(build_event(kind, repo, None, None, actor, payload))
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

/// Build a [`NormalizedEvent`] with the current UTC timestamp.
pub(crate) fn build_event(
    kind: String,
    repo: Option<String>,
    pr: Option<u64>,
    issue: Option<u64>,
    actor: Option<String>,
    payload: &Value,
) -> NormalizedEvent {
    NormalizedEvent {
        ts: Utc::now(),
        source: "webhook",
        kind,
        repo,
        pr,
        issue,
        actor,
        data: payload.clone(),
    }
}

/// Extract a nested string field from `value` by following the `path` keys.
pub(crate) fn string_field(value: &Value, path: &[&str]) -> Option<String> {
    let mut current = value;
    for key in path {
        current = current.get(key)?;
    }
    current.as_str().map(|s| s.to_string())
}

/// Extract a nested `u64` field from `value` by following the `path` keys.
pub(crate) fn u64_field(value: &Value, path: &[&str]) -> Option<u64> {
    let mut current = value;
    for key in path {
        current = current.get(key)?;
    }
    current.as_u64()
}

#[cfg(test)]
#[path = "normalize_tests.rs"]
mod tests;
