mod common;

use std::collections::HashMap;

use orchard::collector::{apply_issue_states, apply_prs, ensure_main_session, merge_tmux_sessions};
use orchard::types::{ChecksStatus, IssueState, PrInfo, ReviewDecision, TmuxSession, Worktree};

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

fn bare_worktree(path: &str) -> Worktree {
    Worktree {
        path: path.to_string(),
        is_bare: true,
        ..Default::default()
    }
}

fn branched_worktree(path: &str, branch: &str) -> Worktree {
    Worktree {
        path: path.to_string(),
        branch: Some(branch.to_string()),
        ..Default::default()
    }
}

fn make_pr(number: u32) -> PrInfo {
    PrInfo {
        number,
        state: "open".to_string(),
        title: "Test PR".to_string(),
        url: "https://example.com".to_string(),
        review_decision: ReviewDecision::None,
        unresolved_threads: 0,
        checks_status: ChecksStatus::None,
        has_conflicts: false,
    }
}

fn session(name: &str, path: &str) -> TmuxSession {
    TmuxSession {
        name: name.to_string(),
        path: path.to_string(),
        attached: false,
        pane_title: None,
    }
}

fn noop_error(_msg: &str) {}

// ---------------------------------------------------------------------------
// merge_tmux_sessions
// ---------------------------------------------------------------------------

/// From cache-architecture.feature: session joins to worktree by matching path.
#[test]
fn merge_tmux_sessions_maps_session_to_worktree_by_path() {
    let trees = vec![branched_worktree("/home/user/project", "main")];
    let sessions = vec![session("myrepo_main", "/home/user/project")];

    let result = merge_tmux_sessions(&trees, &sessions, false);

    assert_eq!(result[0].tmux_session.as_deref(), Some("myrepo_main"));
}

#[test]
fn merge_tmux_sessions_sets_attached_flag_from_session() {
    let trees = vec![branched_worktree("/home/user/project", "main")];
    let sessions = vec![TmuxSession {
        name: "myrepo_main".to_string(),
        path: "/home/user/project".to_string(),
        attached: true,
        pane_title: None,
    }];

    let result = merge_tmux_sessions(&trees, &sessions, false);

    assert!(result[0].tmux_attached);
}

#[test]
fn merge_tmux_sessions_no_session_when_path_does_not_match() {
    let trees = vec![branched_worktree("/home/user/project", "main")];
    let sessions = vec![session("unrelated_develop", "/home/user/other")];

    let result = merge_tmux_sessions(&trees, &sessions, false);

    assert!(result[0].tmux_session.is_none());
}

#[test]
fn merge_tmux_sessions_sets_pr_loading_when_gh_available() {
    let trees = vec![branched_worktree("/home/user/project", "main")];

    let result = merge_tmux_sessions(&trees, &[], true);

    assert!(result[0].pr_loading);
}

#[test]
fn merge_tmux_sessions_pr_loading_false_when_gh_unavailable() {
    let trees = vec![branched_worktree("/home/user/project", "main")];

    let result = merge_tmux_sessions(&trees, &[], false);

    assert!(!result[0].pr_loading);
}

// ---------------------------------------------------------------------------
// apply_prs
// ---------------------------------------------------------------------------

/// From cache-architecture.feature: PR joins to worktree via matching branch name.
#[test]
fn apply_prs_sets_pr_for_matching_branch() {
    let trees = vec![branched_worktree("/home/user/project", "feat/my-feature")];
    let mut pr_map = HashMap::new();
    pr_map.insert("feat/my-feature".to_string(), make_pr(42));

    let result = apply_prs(&trees, &pr_map);

    assert_eq!(result[0].pr.as_ref().map(|p| p.number), Some(42));
}

#[test]
fn apply_prs_no_pr_when_branch_not_in_map() {
    let trees = vec![branched_worktree("/home/user/project", "main")];

    let result = apply_prs(&trees, &HashMap::new());

    assert!(result[0].pr.is_none());
}

#[test]
fn apply_prs_skips_bare_worktrees() {
    let trees = vec![bare_worktree("/home/user/bare.git")];
    let mut pr_map = HashMap::new();
    pr_map.insert("main".to_string(), make_pr(1));

    let result = apply_prs(&trees, &pr_map);

    assert!(result[0].pr.is_none());
}

#[test]
fn apply_prs_clears_pr_loading_flag() {
    let mut tree = branched_worktree("/home/user/project", "main");
    tree.pr_loading = true;

    let result = apply_prs(&[tree], &HashMap::new());

    assert!(!result[0].pr_loading);
}

// ---------------------------------------------------------------------------
// apply_issue_states
// ---------------------------------------------------------------------------

/// From cache-architecture.feature: worktree joins to issue by issue number in branch name.
#[test]
fn apply_issue_states_sets_issue_number_and_state() {
    let trees = vec![branched_worktree("/home/user/project", "feat/issue-200-my-feature")];
    let mut issue_states = HashMap::new();
    issue_states.insert(200u32, IssueState::Open);

    let result = apply_issue_states(&trees, &issue_states);

    assert_eq!(result[0].issue_number, Some(200));
    assert_eq!(result[0].issue_state, Some(IssueState::Open));
}

#[test]
fn apply_issue_states_skips_worktree_that_has_pr() {
    let mut tree = branched_worktree("/home/user/project", "feat/issue-200-my-feature");
    tree.pr = Some(make_pr(1));
    let mut issue_states = HashMap::new();
    issue_states.insert(200u32, IssueState::Open);

    let result = apply_issue_states(&[tree], &issue_states);

    assert!(result[0].issue_number.is_none());
}

#[test]
fn apply_issue_states_skips_bare_worktrees() {
    let tree = bare_worktree("/home/user/bare.git");
    let mut issue_states = HashMap::new();
    issue_states.insert(200u32, IssueState::Closed);

    let result = apply_issue_states(&[tree], &issue_states);

    assert!(result[0].issue_number.is_none());
}

#[test]
fn apply_issue_states_returns_unchanged_when_map_empty() {
    let trees = vec![branched_worktree("/home/user/project", "feat/issue-200-thing")];

    let result = apply_issue_states(&trees, &HashMap::new());

    assert!(result[0].issue_state.is_none());
}

// ---------------------------------------------------------------------------
// Full pipeline: worktree -> tmux merge -> PR apply -> issue states
// ---------------------------------------------------------------------------

#[test]
fn full_pipeline_worktree_to_issue_states() {
    let trees = vec![
        branched_worktree("/home/user/myrepo", "main"),
        branched_worktree("/home/user/myrepo/.worktrees/feat", "feat/issue-123-add-login"),
    ];
    let sessions = vec![session("myrepo_main", "/home/user/myrepo")];

    // Stage 1: merge tmux sessions
    let with_tmux = merge_tmux_sessions(&trees, &sessions, true);
    assert_eq!(with_tmux[0].tmux_session.as_deref(), Some("myrepo_main"));
    assert!(with_tmux[1].tmux_session.is_none());

    // Stage 2: apply PRs — main has a PR, feat branch does not
    let mut pr_map = HashMap::new();
    pr_map.insert("main".to_string(), make_pr(10));
    let with_prs = apply_prs(&with_tmux, &pr_map);
    assert_eq!(with_prs[0].pr.as_ref().map(|p| p.number), Some(10));
    assert!(with_prs[1].pr.is_none());

    // Stage 3: apply issue states — feat branch matches issue 123
    let mut issue_states = HashMap::new();
    issue_states.insert(123u32, IssueState::Open);
    let final_trees = apply_issue_states(&with_prs, &issue_states);
    assert!(final_trees[0].issue_number.is_none()); // main has PR, skipped
    assert_eq!(final_trees[1].issue_number, Some(123));
    assert_eq!(final_trees[1].issue_state, Some(IssueState::Open));
}

// ---------------------------------------------------------------------------
// ensure_main_session (pure-logic cases only — no tmux creation)
// ---------------------------------------------------------------------------

/// From main-session-at-worktree-origin.feature: skips when session already exists.
#[test]
fn ensure_main_session_skips_when_session_already_exists() {
    let trees = vec![branched_worktree("/home/user/myrepo", "main")];
    let sessions = vec![session("myrepo_main", "/home/user/myrepo")];

    let result = ensure_main_session(&trees, sessions, &noop_error);

    assert_eq!(result.len(), 1);
    assert_eq!(result[0].name, "myrepo_main");
}

/// From main-session-at-worktree-origin.feature: skips when session name exists at different path.
#[test]
fn ensure_main_session_skips_when_session_name_exists_at_different_path() {
    let trees = vec![branched_worktree("/home/user/myrepo", "main")];
    let sessions = vec![session("myrepo_main", "/tmp/other-path")];

    let result = ensure_main_session(&trees, sessions, &noop_error);

    // Session name already exists — must not add a duplicate.
    assert_eq!(result.len(), 1);
    assert_eq!(result[0].name, "myrepo_main");
}

#[test]
fn ensure_main_session_returns_unchanged_when_no_worktrees() {
    let sessions = vec![session("other", "/other")];

    let result = ensure_main_session(&[], sessions, &noop_error);

    assert_eq!(result.len(), 1);
    assert_eq!(result[0].name, "other");
}

#[test]
fn ensure_main_session_skips_when_first_worktree_is_bare() {
    let trees = vec![bare_worktree("/home/user/bare.git")];

    let result = ensure_main_session(&trees, vec![], &noop_error);

    assert!(result.is_empty());
}

// ---------------------------------------------------------------------------
// Derived join logic
// ---------------------------------------------------------------------------

/// From cache-architecture.feature derived view: tmux session joins to issue via worktree path.
#[test]
fn tmux_session_joins_to_issue_via_worktree_path() {
    let trees = vec![branched_worktree("/home/user/myrepo/.worktrees/feat", "feat/issue-500-thing")];
    let sessions = vec![session("myrepo_feat-issue-500-thing", "/home/user/myrepo/.worktrees/feat")];

    let with_tmux = merge_tmux_sessions(&trees, &sessions, false);
    let mut issue_states = HashMap::new();
    issue_states.insert(500u32, IssueState::Completed);
    let result = apply_issue_states(&with_tmux, &issue_states);

    assert!(result[0].tmux_session.is_some());
    assert_eq!(result[0].issue_number, Some(500));
    assert_eq!(result[0].issue_state, Some(IssueState::Completed));
}

/// From cache-architecture.feature derived view: PR joins to issue via worktree branch name.
#[test]
fn pr_joins_to_issue_via_worktree_branch_name() {
    // A worktree with a PR should not get issue state applied.
    let trees = vec![branched_worktree("/home/user/myrepo/.worktrees/fix", "fix/issue-300-crash")];
    let mut pr_map = HashMap::new();
    pr_map.insert("fix/issue-300-crash".to_string(), make_pr(99));
    let with_prs = apply_prs(&trees, &pr_map);

    let mut issue_states = HashMap::new();
    issue_states.insert(300u32, IssueState::Closed);
    let result = apply_issue_states(&with_prs, &issue_states);

    // Has PR — issue state must not be applied.
    assert_eq!(result[0].pr.as_ref().map(|p| p.number), Some(99));
    assert!(result[0].issue_number.is_none());
}
