/// Integration tests for the derive pipeline (`orchard::derive`).
///
/// These tests exercise the full `derive_worktree_rows` / `derive_all_repos`
/// pipeline using realistic multi-step data, corresponding to acceptance
/// criteria from `cache-architecture.feature` and `state-system.feature`.
mod common;

use common::{
    make_approved_pr, make_changes_requested_pr, make_claude_session, make_issue, make_pr,
    make_session, make_worktree, TestCacheDir,
};
use orchard::cache::{read_cache, CachedPr, CachedWorktree};
use orchard::derive::{derive_all_repos, derive_worktree_rows, DisplayGroup};

// ---------------------------------------------------------------------------
// Multi-repo: tasks from all repos appear in the result
// ---------------------------------------------------------------------------

/// When two repos each have worktrees, `derive_all_repos` returns rows for
/// both repos combined.
#[test]
fn multi_repo_tui_shows_tasks_from_all_repos() {
    let repo_caches = vec![
        (
            "owner/repo-a".to_string(),
            vec![],
            vec![],
            vec![
                make_worktree("/workspace/repo-a", "main"),
                make_worktree("/workspace/repo-a-feat", "feat/branch-a"),
            ],
            vec![],
        ),
        (
            "owner/repo-b".to_string(),
            vec![],
            vec![],
            vec![
                make_worktree("/workspace/repo-b", "main"),
                make_worktree("/workspace/repo-b-feat", "feat/branch-b"),
            ],
            vec![],
        ),
    ];

    let rows = derive_all_repos(&repo_caches);

    assert_eq!(rows.len(), 4, "should have 2 rows per repo");

    let slugs: Vec<&str> = rows.iter().map(|r| r.repo_slug.as_str()).collect();
    assert!(slugs.contains(&"owner/repo-a"));
    assert!(slugs.contains(&"owner/repo-b"));
}

// ---------------------------------------------------------------------------
// Empty caches produce empty rows
// ---------------------------------------------------------------------------

/// Calling `derive_worktree_rows` with empty worktrees returns an empty vec.
#[test]
fn repo_with_no_cache_shows_empty() {
    let rows = derive_worktree_rows(&[], &[], &[], &[], "owner/repo");
    assert!(rows.is_empty());
}

// ---------------------------------------------------------------------------
// Display group: NeedsAttention when PR has changes_requested
// ---------------------------------------------------------------------------

/// A non-shepherd worktree whose PR has `review_decision = "changes_requested"`
/// must derive `NeedsAttention`.
#[test]
fn display_group_needs_attention_changes_requested() {
    let worktrees = vec![
        make_worktree("/workspace/repo", "main"),
        make_worktree("/workspace/repo-feat", "feat/branch"),
    ];
    let prs = vec![make_changes_requested_pr(55, "feat/branch")];

    let rows = derive_worktree_rows(&[], &prs, &worktrees, &[], "owner/repo");

    assert_eq!(rows.len(), 2);
    assert_eq!(rows[1].display_group, DisplayGroup::NeedsAttention);
}

// ---------------------------------------------------------------------------
// Display group: ClaudeWorking when session has "claude" in pane_commands
// ---------------------------------------------------------------------------

/// A worktree with a tmux session running `claude` as a pane command must
/// derive `ClaudeWorking`.
#[test]
fn display_group_claude_working_when_agent_active() {
    let worktrees = vec![
        make_worktree("/workspace/repo", "main"),
        make_worktree("/workspace/repo-feat", "feat/branch"),
    ];
    let sessions = vec![make_claude_session("repo_feat", "/workspace/repo-feat")];

    let rows = derive_worktree_rows(&[], &[], &worktrees, &sessions, "owner/repo");

    assert_eq!(rows[1].display_group, DisplayGroup::ClaudeWorking);
}

// ---------------------------------------------------------------------------
// Display group: ReadyToMerge when PR is approved + passing + no conflicts
// ---------------------------------------------------------------------------

/// A worktree whose PR is approved, passing CI, and has no conflicts must
/// derive `ReadyToMerge`.
#[test]
fn display_group_ready_to_merge() {
    let worktrees = vec![
        make_worktree("/workspace/repo", "main"),
        make_worktree("/workspace/repo-feat", "feat/branch"),
    ];
    let prs = vec![make_approved_pr(55, "feat/branch")];

    let rows = derive_worktree_rows(&[], &prs, &worktrees, &[], "owner/repo");

    assert_eq!(rows[1].display_group, DisplayGroup::ReadyToMerge);
}

// ---------------------------------------------------------------------------
// Display group: Other when there is no PR
// ---------------------------------------------------------------------------

/// A non-shepherd worktree with no matching PR and no tmux session must
/// derive `Other`.
#[test]
fn display_group_other_for_no_pr() {
    let worktrees = vec![
        make_worktree("/workspace/repo", "main"),
        make_worktree("/workspace/repo-feat", "feat/branch"),
    ];

    let rows = derive_worktree_rows(&[], &[], &worktrees, &[], "owner/repo");

    assert_eq!(rows[1].display_group, DisplayGroup::Other);
}

// ---------------------------------------------------------------------------
// Display group ordering matches spec
// ---------------------------------------------------------------------------

/// The `Ord` derivation on `DisplayGroup` must produce the documented order:
/// Shepherd < NeedsAttention < ClaudeWorking < ReadyToMerge < Other.
#[test]
fn display_groups_ordered_for_rendering() {
    assert!(DisplayGroup::Shepherd < DisplayGroup::NeedsAttention);
    assert!(DisplayGroup::NeedsAttention < DisplayGroup::ClaudeWorking);
    assert!(DisplayGroup::ClaudeWorking < DisplayGroup::ReadyToMerge);
    assert!(DisplayGroup::ReadyToMerge < DisplayGroup::Other);
}

// ---------------------------------------------------------------------------
// Worktree joins to PR via shared branch name
// ---------------------------------------------------------------------------

/// A worktree on branch `"feat/x"` must join to the PR whose `branch` field
/// is also `"feat/x"`.
#[test]
fn worktree_joins_to_pr_via_branch() {
    let worktrees = vec![make_worktree("/workspace/repo-feat", "feat/x")];
    let prs = vec![make_pr(99, "feat/x")];

    let rows = derive_worktree_rows(&[], &prs, &worktrees, &[], "owner/repo");

    assert_eq!(rows.len(), 1);
    let pr = rows[0].pr.as_ref().expect("PR should be joined");
    assert_eq!(pr.number, 99);
    assert_eq!(pr.branch, "feat/x");
}

// ---------------------------------------------------------------------------
// Tmux session joins via worktree path
// ---------------------------------------------------------------------------

/// A tmux session whose `path` matches the worktree `path` must be included
/// in that worktree row's `sessions` list.
#[test]
fn tmux_session_joins_via_worktree_path() {
    let worktrees = vec![make_worktree("/workspace/repo-47", "feat/task")];
    let sessions = vec![make_session("repo_47", "/workspace/repo-47", vec!["bash"])];

    let rows = derive_worktree_rows(&[], &[], &worktrees, &sessions, "owner/repo");

    assert_eq!(rows[0].sessions.len(), 1);
    assert_eq!(rows[0].sessions[0].name, "repo_47");
}

// ---------------------------------------------------------------------------
// Multiple sessions at same path all join
// ---------------------------------------------------------------------------

/// When two tmux sessions share the same path as a worktree, both must appear
/// in the worktree row's `sessions` list.
#[test]
fn multiple_tmux_sessions_at_same_path_all_join() {
    let worktrees = vec![make_worktree("/workspace/repo-47", "feat/task")];
    let sessions = vec![
        make_session("repo_47_main", "/workspace/repo-47", vec!["bash"]),
        make_claude_session("repo_47_claude", "/workspace/repo-47"),
    ];

    let rows = derive_worktree_rows(&[], &[], &worktrees, &sessions, "owner/repo");

    assert_eq!(rows[0].sessions.len(), 2);
    let names: Vec<&str> = rows[0].sessions.iter().map(|s| s.name.as_str()).collect();
    assert!(names.contains(&"repo_47_main"));
    assert!(names.contains(&"repo_47_claude"));
}

// ---------------------------------------------------------------------------
// Issue join via branch naming convention
// ---------------------------------------------------------------------------

/// A branch named `"issue-42"` must cause the row to carry `issue_number = 42`
/// and the corresponding `issue_title` from the issues cache.
#[test]
fn issue_joined_to_worktree_via_branch_naming_convention() {
    let issues = vec![make_issue(42, "Fix the widget")];
    let worktrees = vec![make_worktree("/workspace/repo-42", "issue-42")];

    let rows = derive_worktree_rows(&issues, &[], &worktrees, &[], "owner/repo");

    assert_eq!(rows[0].issue_number, Some(42));
    assert_eq!(rows[0].issue_title.as_deref(), Some("Fix the widget"));
}

// ---------------------------------------------------------------------------
// Cache file roundtrip feeds cleanly into derive pipeline
// ---------------------------------------------------------------------------

/// Write cache fixtures to real temp files, read them back, then run the derive
/// pipeline — verifying the full file → cache → derive path works end-to-end.
#[test]
fn cache_file_roundtrip_feeds_into_derive_pipeline() {
    let cache = TestCacheDir::new();

    cache.write_worktrees(
        "owner",
        "repo",
        &[
            make_worktree("/workspace/repo", "main"),
            make_worktree("/workspace/repo-feat", "feat/branch"),
        ],
    );
    cache.write_prs("owner", "repo", &[make_approved_pr(55, "feat/branch")]);
    cache.write_issues("owner", "repo", &[]);

    let worktrees_path = cache.repo_cache_path("owner", "repo", "worktrees");
    let prs_path = cache.repo_cache_path("owner", "repo", "prs");

    let loaded_worktrees = read_cache::<CachedWorktree>(&worktrees_path);
    let loaded_prs = read_cache::<CachedPr>(&prs_path);

    let rows = derive_worktree_rows(
        &[],
        &loaded_prs.entries,
        &loaded_worktrees.entries,
        &[],
        "owner/repo",
    );

    assert_eq!(rows.len(), 2);
    assert_eq!(rows[0].display_group, DisplayGroup::Shepherd);
    assert_eq!(rows[1].display_group, DisplayGroup::ReadyToMerge);
}
