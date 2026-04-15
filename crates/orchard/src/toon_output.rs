//! TOON (Token-Oriented Object Notation) output for `orchard --toon`.
//!
//! Thin transform over [`crate::json_output::JsonOutput`]: serialize to
//! `serde_json::Value`, then encode as TOON v2.0 via `json2toon_rs`.
//! Versioning, schema, and field names are identical to `--json` — this
//! module intentionally adds no new schema surface.
//!
//! Intended consumer: AI agents reading orchard state. TOON uses uniform
//! arrays with a header row, which tokenizes more efficiently than the
//! equivalent JSON for repeated-shape data like `repos[].worktrees[]`.
//!
//! One-way only — there is no `from_toon` path. Agents consume, they don't
//! write back.

use json2toon_rs::{EncoderOptions, encode};

use crate::json_output::JsonOutput;

/// Renders a [`JsonOutput`] as TOON v2.0.
///
/// # Errors
///
/// Returns the underlying `serde_json` error if the output cannot be
/// converted to a `serde_json::Value`. In practice `JsonOutput` always
/// serializes cleanly; this path is retained so callers can surface any
/// unexpected failure without panicking.
pub fn render(output: &JsonOutput) -> Result<String, serde_json::Error> {
    let value = serde_json::to_value(output)?;
    Ok(encode(&value, &EncoderOptions::default()))
}

#[cfg(test)]
mod tests {
    use std::collections::HashMap;

    use super::*;
    use crate::derive::DisplayGroup;
    use crate::orchard_state::{OrchardState, RepoState, WorktreeState};

    /// Minimal fixture: empty state renders as a non-empty TOON document with
    /// the schema `version` field visible.
    #[test]
    fn render_empty_state_contains_version() {
        let state = OrchardState::new();
        let output = JsonOutput::from(&state);
        let toon = render(&output).unwrap();
        assert!(toon.contains("version"), "toon output missing version key");
    }

    /// Round-trip: TOON encoded from `JsonOutput` decodes back to JSON that
    /// matches the original `JsonOutput` serialization. This is the core
    /// correctness guarantee — no schema drift between `--json` and `--toon`.
    #[test]
    fn render_round_trips_through_toon_decoder() {
        let state = OrchardState {
            repos: vec![RepoState {
                slug: "owner/repo".to_string(),
                worktrees: vec![WorktreeState {
                    path: "/repos/main".to_string(),
                    branch: "main".to_string(),
                    is_bare: false,
                    host: None,
                    issue: None,
                    pr: None,
                    sessions: vec![],
                    display_group: DisplayGroup::RepoMain,
                    is_main_worktree: true,
                    ahead_behind: None,
                    last_commit_at: None,
                }],
                default_branch: Some("main".to_string()),
                main_ci_state: None,
            }],
            standalone_sessions: vec![],
            hosts: HashMap::new(),
        };
        let output = JsonOutput::from(&state);
        let original_json = serde_json::to_value(&output).unwrap();

        let toon = render(&output).unwrap();
        let decoded =
            json2toon_rs::decode(&toon, &json2toon_rs::DecoderOptions::default()).unwrap();

        assert_eq!(
            original_json, decoded,
            "toon round-trip lost or mutated data"
        );
    }

    /// Snapshot-style test: a fixture `OrchardState` covering nested
    /// collection types (repo → worktree → session, plus issue) produces
    /// deterministic TOON output with recognisable structural markers.
    ///
    /// We assert on substrings rather than a full byte-for-byte snapshot
    /// because `JsonOutput.hosts` is a `HashMap` whose serialization order
    /// is non-deterministic across runs.
    #[test]
    fn render_snapshot_full_fixture() {
        use crate::orchard_state::{IssueInfo, SessionState};

        let state = OrchardState {
            repos: vec![RepoState {
                slug: "acme/widgets".to_string(),
                worktrees: vec![WorktreeState {
                    path: "/src/widgets".to_string(),
                    branch: "issue42/fix-bug".to_string(),
                    is_bare: false,
                    host: None,
                    issue: Some(IssueInfo {
                        number: 42,
                        title: "Widget crash on load".to_string(),
                        state: "open".to_string(),
                        labels: vec!["bug".to_string()],
                        assignees: vec![],
                        created_at: None,
                        updated_at: None,
                        blocked_by: vec![],
                        sub_issues: vec![],
                        parent: None,
                    }),
                    pr: None,
                    sessions: vec![SessionState {
                        name: "widgets-issue42".to_string(),
                        host: None,
                        claude: None,
                        windows: vec![],
                        started_at: None,
                        last_activity_at: None,
                    }],
                    display_group: DisplayGroup::ReadyToMerge,
                    is_main_worktree: false,
                    ahead_behind: None,
                    last_commit_at: None,
                }],
                default_branch: Some("main".to_string()),
                main_ci_state: Some("SUCCESS".to_string()),
            }],
            standalone_sessions: vec![],
            hosts: HashMap::new(),
        };

        let output = JsonOutput::from(&state);
        let toon = render(&output).unwrap();

        // Structural checks — HashMap iteration order precludes a byte-snapshot.
        assert!(toon.contains("version"), "missing schema version:\n{toon}");
        assert!(toon.contains("acme/widgets"), "missing repo slug:\n{toon}");
        assert!(toon.contains("issue42/fix-bug"), "missing branch:\n{toon}");
        assert!(
            toon.contains("ready_to_merge"),
            "missing display group:\n{toon}"
        );
        assert!(
            toon.contains("widgets-issue42"),
            "missing session name:\n{toon}"
        );
    }
}
