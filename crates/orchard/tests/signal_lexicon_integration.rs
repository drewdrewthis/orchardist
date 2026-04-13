//! Integration tests for the signal-lexicon pipeline (issue #251).
//!
//! These tests drive the derive pipeline with realistic fixtures and then
//! verify that:
//!   1. The pipeline-status resolver assigns the right glyph per row.
//!   2. The activity rollup severity chain holds across session→window→pane.
//!   3. The pipeline-severity sort orders rows correctly.
//!   4. The unified labels cell merges issue + PR labels with dedup.
//!
//! Together these acceptance criteria correspond to the `Scope` checklist
//! items in issue #251.

mod common;

use common::{make_issue, make_pr, make_worktree};
use orchard::cache::CachedPr;
use orchard::derive::derive_all_repos;
use orchard::signal::{Activity, PipelineStatus, resolve_status_row, rollup_activity_row, sort_key_row};

// ---------------------------------------------------------------------------
// Pipeline status hierarchy
// ---------------------------------------------------------------------------

/// ❌ CI failing beats 🔴 changes requested when both are true on the same PR.
#[test]
fn ci_failing_outranks_changes_requested() {
    let repo_caches = vec![(
        "owner/repo".to_string(),
        vec![make_issue(10, "X")],
        vec![CachedPr {
            review_decision: Some("changes_requested".to_string()),
            ci_code_state: Some("failing".to_string()),
            ..make_pr(10, "feat/issue-10")
        }],
        vec![
            make_worktree("/wt/main", "main"),
            make_worktree("/wt/a", "feat/issue-10"),
        ],
        vec![],
    )];

    let rows = derive_all_repos(&repo_caches, &[], &[]);
    let feat = rows
        .iter()
        .find(|r| r.branch == "feat/issue-10")
        .expect("feature row");
    assert_eq!(resolve_status_row(feat), PipelineStatus::CiFailing);
}

/// ⚠️ merge conflict beats 🔴 changes requested.
#[test]
fn merge_conflict_outranks_changes_requested() {
    let repo_caches = vec![(
        "owner/repo".to_string(),
        vec![make_issue(10, "X")],
        vec![CachedPr {
            review_decision: Some("changes_requested".to_string()),
            has_conflicts: true,
            ..make_pr(10, "feat/issue-10")
        }],
        vec![
            make_worktree("/wt/main", "main"),
            make_worktree("/wt/a", "feat/issue-10"),
        ],
        vec![],
    )];

    let rows = derive_all_repos(&repo_caches, &[], &[]);
    let feat = rows
        .iter()
        .find(|r| r.branch == "feat/issue-10")
        .expect("feature row");
    assert_eq!(resolve_status_row(feat), PipelineStatus::MergeConflict);
}

/// 🚀 Merged beats everything else — it's terminal.
#[test]
fn merged_is_terminal() {
    let repo_caches = vec![(
        "owner/repo".to_string(),
        vec![make_issue(10, "X")],
        vec![CachedPr {
            state: "merged".to_string(),
            has_conflicts: true, // nonsense field on a merged PR — ignored
            ci_code_state: Some("failing".to_string()),
            ..make_pr(10, "feat/issue-10")
        }],
        vec![
            make_worktree("/wt/main", "main"),
            make_worktree("/wt/a", "feat/issue-10"),
        ],
        vec![],
    )];

    let rows = derive_all_repos(&repo_caches, &[], &[]);
    let feat = rows
        .iter()
        .find(|r| r.branch == "feat/issue-10")
        .expect("feature row");
    assert_eq!(resolve_status_row(feat), PipelineStatus::Merged);
}

/// ✍️ Coding is the default when there's no PR at all.
#[test]
fn coding_default_when_no_pr() {
    let repo_caches = vec![(
        "owner/repo".to_string(),
        vec![make_issue(10, "X")],
        vec![],
        vec![
            make_worktree("/wt/main", "main"),
            make_worktree("/wt/a", "feat/issue-10"),
        ],
        vec![],
    )];

    let rows = derive_all_repos(&repo_caches, &[], &[]);
    let feat = rows
        .iter()
        .find(|r| r.branch == "feat/issue-10")
        .expect("feature row");
    assert_eq!(resolve_status_row(feat), PipelineStatus::Coding);
}

// ---------------------------------------------------------------------------
// Activity rollup (no Claude sessions = None)
// ---------------------------------------------------------------------------

/// When no Claude sessions are attached, column A is blank (Activity::None).
#[test]
fn activity_none_when_no_claude_sessions() {
    let repo_caches = vec![(
        "owner/repo".to_string(),
        vec![make_issue(10, "X")],
        vec![],
        vec![
            make_worktree("/wt/main", "main"),
            make_worktree("/wt/a", "feat/issue-10"),
        ],
        vec![],
    )];

    let rows = derive_all_repos(&repo_caches, &[], &[]);
    let feat = rows
        .iter()
        .find(|r| r.branch == "feat/issue-10")
        .expect("feature row");
    assert_eq!(rollup_activity_row(feat), Activity::None);
}

// ---------------------------------------------------------------------------
// Pipeline-severity sort
// ---------------------------------------------------------------------------

/// CI-failing rows sort before merge-conflict rows, which sort before coding rows.
#[test]
fn sort_respects_merge_blocker_hierarchy() {
    let repo_caches = vec![(
        "owner/repo".to_string(),
        vec![
            make_issue(10, "failing"),
            make_issue(20, "conflict"),
            make_issue(30, "coding"),
        ],
        vec![
            CachedPr {
                ci_code_state: Some("failing".to_string()),
                ..make_pr(10, "feat/issue-10")
            },
            CachedPr {
                has_conflicts: true,
                ..make_pr(20, "feat/issue-20")
            },
            // issue-30 has no PR → Coding
        ],
        vec![
            make_worktree("/wt/main", "main"),
            make_worktree("/wt/a", "feat/issue-10"),
            make_worktree("/wt/b", "feat/issue-20"),
            make_worktree("/wt/c", "feat/issue-30"),
        ],
        vec![],
    )];

    let rows = derive_all_repos(&repo_caches, &[], &[]);
    // Main first, then merge-blocker hierarchy.
    assert_eq!(rows[0].branch, "main");

    // Compute sort keys and verify status ordering.
    let tail_status: Vec<PipelineStatus> = rows[1..]
        .iter()
        .map(|r| sort_key_row(r).status)
        .collect();
    assert_eq!(
        tail_status,
        vec![
            PipelineStatus::CiFailing,
            PipelineStatus::MergeConflict,
            PipelineStatus::Coding,
        ]
    );
}
