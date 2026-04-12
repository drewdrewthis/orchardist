/// Integration tests for the derive pipeline (`orchard::derive`).
///
/// These tests exercise the full `derive_worktree_rows` / `derive_all_repos`
/// pipeline using realistic multi-step data, corresponding to acceptance
/// criteria from `cache-architecture.feature` and `state-system.feature`.
mod common;

use common::{
    TestCacheDir, make_approved_pr, make_changes_requested_pr, make_claude_session, make_issue,
    make_pr, make_session, make_worktree,
};
use orchard::cache::{CachedPr, CachedWorktree, read_cache};
use orchard::ci_state::{CheckInfo, CiChecks};
use orchard::derive::{DisplayGroup, derive_all_repos, derive_worktree_rows};
use orchard::json_output::JsonPr;
use orchard::orchard_state::PrState;

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

    let rows = derive_all_repos(&repo_caches, &[]);

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
    let rows = derive_worktree_rows(&[], &[], &[], &[], "owner/repo", &[]);
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

    let rows = derive_worktree_rows(&[], &prs, &worktrees, &[], "owner/repo", &[]);

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

    let rows = derive_worktree_rows(&[], &[], &worktrees, &sessions, "owner/repo", &[]);

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

    let rows = derive_worktree_rows(&[], &prs, &worktrees, &[], "owner/repo", &[]);

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

    let rows = derive_worktree_rows(&[], &[], &worktrees, &[], "owner/repo", &[]);

    assert_eq!(rows[1].display_group, DisplayGroup::Other);
}

// ---------------------------------------------------------------------------
// Display group ordering matches spec
// ---------------------------------------------------------------------------

/// The `Ord` derivation on `DisplayGroup` must produce the documented order:
/// Shepherd < NeedsAttention < ClaudeWorking < ReadyToMerge < Other.
#[test]
fn display_groups_ordered_for_rendering() {
    assert!(DisplayGroup::RepoMain < DisplayGroup::NeedsAttention);
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

    let rows = derive_worktree_rows(&[], &prs, &worktrees, &[], "owner/repo", &[]);

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

    let rows = derive_worktree_rows(&[], &[], &worktrees, &sessions, "owner/repo", &[]);

    assert_eq!(rows[0].sessions.len(), 1);
    assert_eq!(rows[0].sessions[0].tmux.name, "repo_47");
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

    let rows = derive_worktree_rows(&[], &[], &worktrees, &sessions, "owner/repo", &[]);

    assert_eq!(rows[0].sessions.len(), 2);
    let names: Vec<&str> = rows[0]
        .sessions
        .iter()
        .map(|s| s.tmux.name.as_str())
        .collect();
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

    let rows = derive_worktree_rows(&issues, &[], &worktrees, &[], "owner/repo", &[]);

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
        &[],
    );

    assert_eq!(rows.len(), 2);
    assert_eq!(rows[0].display_group, DisplayGroup::RepoMain);
    assert_eq!(rows[1].display_group, DisplayGroup::ReadyToMerge);
}

// ---------------------------------------------------------------------------
// End-to-end: split CI state surfaces through the JSON output pipeline
// ---------------------------------------------------------------------------

/// Helper: build a `CachedPr` that mirrors what `parse_prs_graphql` would
/// produce for a PR whose code CI is passing and whose `check-approval-or-label`
/// gate is failing. Used by the two e2e scenarios below.
fn make_code_green_gate_blocked_pr(number: u32, branch: &str) -> CachedPr {
    CachedPr {
        number,
        branch: branch.to_string(),
        linked_issue: None,
        state: "open".to_string(),
        review_decision: Some("approved".to_string()),
        // Legacy field mirrors ci_code_state — this is what slice 2 produces in
        // `derive_ci_state_graphql`. A code-green gate-blocked PR stays
        // `"passing"` at the legacy field so old consumers keep working.
        checks_state: Some("passing".to_string()),
        ci_code_state: Some("passing".to_string()),
        ci_gate_state: Some("blocked".to_string()),
        ci_checks: CiChecks {
            code: vec![
                CheckInfo {
                    name: "test-unit".to_string(),
                    state: "passing".to_string(),
                    details_url: None,
                },
                CheckInfo {
                    name: "test-integration".to_string(),
                    state: "passing".to_string(),
                    details_url: None,
                },
                CheckInfo {
                    name: "lint".to_string(),
                    state: "passing".to_string(),
                    details_url: None,
                },
            ],
            gate: vec![CheckInfo {
                name: "check-approval-or-label".to_string(),
                state: "failing".to_string(),
                details_url: None,
            }],
        },
        has_conflicts: false,
        unresolved_threads: 0,
        linked_issue_state: None,
        labels: vec![],
        title: None,
        is_draft: None,
        author: None,
        requested_reviewers: vec![],
        reviews: vec![],
        additions: None,
        deletions: None,
        created_at: None,
        updated_at: None,
        last_commit_pushed_at: None,
    }
}

/// @e2e — task #6: a PR that is code-green but has a failing gate check must:
/// 1. not be classified as `NeedsAttention` (the regression this feature fixes),
/// 2. expose `ciCodeState` / `ciGateState` / `ciChecks` in the JSON output
///    so the orchardist's `jq` filter (`ciCodeState == "passing" and
///    ciGateState == "blocked"`) finds it.
#[test]
fn e2e_code_green_gate_blocked_pr_surfaces_in_json_output() {
    let worktrees = vec![
        make_worktree("/workspace/repo", "main"),
        make_worktree("/workspace/repo-feat", "feat/branch"),
    ];
    let prs = vec![make_code_green_gate_blocked_pr(42, "feat/branch")];

    let rows = derive_worktree_rows(&[], &prs, &worktrees, &[], "owner/repo", &[]);

    // (1) Display group is NOT NeedsAttention — the code path that would have
    // caused the regression. It is also NOT ReadyToMerge because the gate is
    // blocked. So the feat row falls through to `Other`.
    assert_eq!(
        rows[1].display_group,
        DisplayGroup::Other,
        "code-green gate-blocked PR must not cascade into NeedsAttention"
    );
    assert_ne!(rows[1].display_group, DisplayGroup::NeedsAttention);

    // (2) Serialize the PR through the full JsonPr pipeline and verify the new
    // fields appear with camelCase keys alongside the legacy field, and the
    // orchardist's triage filter matches.
    let pr_info = rows[1].pr.as_ref().expect("row should have a PR");
    let pr_state = PrState::from(pr_info);
    let json_pr = JsonPr::from(&pr_state);
    let json_value = serde_json::to_value(&json_pr).expect("serialize JsonPr");

    assert_eq!(json_value["ciCodeState"], "passing");
    assert_eq!(json_value["ciGateState"], "blocked");
    // checksState (legacy) mirrors ci_code_state — code-green stays "passing".
    assert_eq!(json_value["checksState"], "passing");

    // ciChecks carries the per-check breakdown with code and gate buckets.
    let code_names: Vec<&str> = json_value["ciChecks"]["code"]
        .as_array()
        .expect("code is array")
        .iter()
        .map(|n| n["name"].as_str().unwrap())
        .collect();
    assert!(code_names.contains(&"test-unit"));
    assert!(code_names.contains(&"test-integration"));
    assert!(code_names.contains(&"lint"));

    let gate_names: Vec<&str> = json_value["ciChecks"]["gate"]
        .as_array()
        .expect("gate is array")
        .iter()
        .map(|n| n["name"].as_str().unwrap())
        .collect();
    assert_eq!(gate_names, vec!["check-approval-or-label"]);

    // The JSON must not carry an `ignored` bucket in v1 (explicitly scoped out).
    assert!(json_value["ciChecks"].get("ignored").is_none());

    // The orchardist's `jq` filter for "ready for Slack review request":
    //   .pr | select(.ciCodeState == "passing" and .ciGateState == "blocked")
    // matches this PR.
    let matches_triage_filter =
        json_value["ciCodeState"] == "passing" && json_value["ciGateState"] == "blocked";
    assert!(matches_triage_filter, "triage filter must surface this PR");
}

/// @e2e — task #7: a PR whose only gate check is still running (pending) must
/// resolve to `ciGateState == "pending"`, not `"blocked"`, so the
/// orchardist's triage filter (blocked only) does NOT surface it. This
/// distinguishes a mid-flight Mintlify preview from a hard
/// `check-approval-or-label` failure.
#[test]
fn e2e_pending_gate_is_not_surfaced_by_blocked_filter() {
    let worktrees = vec![
        make_worktree("/workspace/repo", "main"),
        make_worktree("/workspace/repo-feat", "feat/branch"),
    ];
    let prs = vec![CachedPr {
        number: 43,
        branch: "feat/branch".to_string(),
        linked_issue: None,
        state: "open".to_string(),
        review_decision: Some("approved".to_string()),
        checks_state: Some("passing".to_string()),
        ci_code_state: Some("passing".to_string()),
        ci_gate_state: Some("pending".to_string()),
        ci_checks: CiChecks {
            code: vec![CheckInfo {
                name: "test-unit".to_string(),
                state: "passing".to_string(),
                details_url: None,
            }],
            gate: vec![CheckInfo {
                name: "Mintlify Deployment".to_string(),
                state: "pending".to_string(),
                details_url: None,
            }],
        },
        has_conflicts: false,
        unresolved_threads: 0,
        linked_issue_state: None,
        labels: vec![],
        title: None,
        is_draft: None,
        author: None,
        requested_reviewers: vec![],
        reviews: vec![],
        additions: None,
        deletions: None,
        created_at: None,
        updated_at: None,
        last_commit_pushed_at: None,
    }];

    let rows = derive_worktree_rows(&[], &prs, &worktrees, &[], "owner/repo", &[]);

    let pr_info = rows[1].pr.as_ref().expect("row should have a PR");
    let pr_state = PrState::from(pr_info);
    let json_pr = JsonPr::from(&pr_state);
    let json_value = serde_json::to_value(&json_pr).expect("serialize JsonPr");

    assert_eq!(json_value["ciCodeState"], "passing");
    assert_eq!(
        json_value["ciGateState"], "pending",
        "mid-flight gate checks must be distinguishable from hard failures"
    );

    // The orchardist's triage filter requires `ciGateState == "blocked"` — this
    // PR must NOT match, because the gate is still running.
    let matches_triage_filter =
        json_value["ciCodeState"] == "passing" && json_value["ciGateState"] == "blocked";
    assert!(
        !matches_triage_filter,
        "a pending-gate PR must not be surfaced by the blocked filter"
    );
}
