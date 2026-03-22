mod common;

use orchard::tmux::{derive_main_session_name, derive_session_name, find_session_for_worktree, sanitize_repo_name};
use orchard::types::TmuxSession;

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

fn session(name: &str, path: &str) -> TmuxSession {
    TmuxSession {
        name: name.to_string(),
        path: path.to_string(),
        attached: false,
        pane_title: None,
    }
}

// ---------------------------------------------------------------------------
// derive_session_name — from tmux-session-management.feature
// ---------------------------------------------------------------------------

/// Slashes in branch name are replaced with dashes.
#[test]
fn session_name_for_branch_with_slashes_replaces_slashes_with_dashes() {
    let name = derive_session_name("myrepo", Some("feature/my-work"), "/any/path");
    assert_eq!(name, "myrepo_feature-my-work");
}

/// Falls back to directory name when no branch is provided.
#[test]
fn session_name_falls_back_to_directory_name_when_branch_absent() {
    let name = derive_session_name("myrepo", None, "/home/user/my-worktree");
    assert_eq!(name, "myrepo_my-worktree");
}

// ---------------------------------------------------------------------------
// find_session_for_worktree — from tmux-session-management.feature
// ---------------------------------------------------------------------------

/// Finds session by exact path match (highest priority).
#[test]
fn find_session_by_exact_path_match() {
    let sessions = vec![
        session("other_main", "/other/path"),
        session("myrepo_main", "/home/user/myrepo"),
    ];

    let result = find_session_for_worktree(&sessions, "/home/user/myrepo", None);

    assert!(result.is_some());
    assert_eq!(result.unwrap().name, "myrepo_main");
}

/// Finds session by matching branch slug after '_' in session name.
#[test]
fn find_session_by_branch_slug() {
    let sessions = vec![session("myrepo_feature-x", "/home/user/myrepo-feature-x")];

    let result = find_session_for_worktree(&sessions, "/no/match", Some("feature/x"));

    assert!(result.is_some());
    assert_eq!(result.unwrap().name, "myrepo_feature-x");
}

/// Returns None when no session matches path or branch.
#[test]
fn no_session_found_returns_none() {
    let sessions = vec![session("other_main", "/other/path")];

    let result = find_session_for_worktree(&sessions, "/no/match", Some("no-match-branch"));

    assert!(result.is_none());
}

// ---------------------------------------------------------------------------
// derive_main_session_name — from main-session-at-worktree-origin.feature
// ---------------------------------------------------------------------------

/// Derives repo name from the worktree origin path (last segment).
#[test]
fn derives_repo_name_from_worktree_origin_path() {
    let name = derive_main_session_name("/home/user/myrepo", Some("main"));
    assert_eq!(name, "myrepo_main");
}

/// Dots in repo name are replaced with underscores.
#[test]
fn sanitizes_dots_in_repo_name() {
    let name = derive_main_session_name("/home/user/my.repo-v2", Some("main"));
    assert_eq!(name, "my_repo-v2_main");
}

/// Uses HEAD as branch identifier when detached.
#[test]
fn uses_head_as_branch_identifier_when_detached() {
    let name = derive_main_session_name("/home/user/myrepo", None);
    assert_eq!(name, "myrepo_HEAD");
}

// ---------------------------------------------------------------------------
// sanitize_repo_name
// ---------------------------------------------------------------------------

#[test]
fn sanitize_repo_name_replaces_dots_with_underscores() {
    assert_eq!(sanitize_repo_name("my.repo.v2"), "my_repo_v2");
}

#[test]
fn sanitize_repo_name_preserves_names_without_dots() {
    assert_eq!(sanitize_repo_name("myrepo"), "myrepo");
}

// ---------------------------------------------------------------------------
// ensure_main_session pure-logic cases — from main-session-at-worktree-origin.feature
// (Real tmux tests are marked #[ignore] below)
// ---------------------------------------------------------------------------

use orchard::collector::ensure_main_session;
use orchard::types::Worktree;

fn noop_error(_: &str) {}

fn branched_worktree(path: &str, branch: &str) -> Worktree {
    Worktree {
        path: path.to_string(),
        branch: Some(branch.to_string()),
        ..Default::default()
    }
}

/// Skips creation when session name already exists in list.
#[test]
fn skips_creation_when_session_name_already_exists() {
    let trees = vec![branched_worktree("/home/user/myrepo", "main")];
    let sessions = vec![session("myrepo_main", "/home/user/myrepo")];

    let result = ensure_main_session(&trees, sessions, &noop_error);

    assert_eq!(result.len(), 1);
    assert_eq!(result[0].name, "myrepo_main");
}

/// Skips creation when session with same name exists at a different path.
#[test]
fn skips_creation_when_session_exists_at_different_path() {
    let trees = vec![branched_worktree("/home/user/myrepo", "main")];
    let sessions = vec![session("myrepo_main", "/tmp/other")];

    let result = ensure_main_session(&trees, sessions, &noop_error);

    assert_eq!(result.len(), 1);
}

// ---------------------------------------------------------------------------
// Real tmux integration tests — require tmux to be installed and running.
// These are skipped by default; run with `cargo test -- --ignored`.
// ---------------------------------------------------------------------------

#[test]
#[ignore]
fn worktree_session_is_created_in_worktree_directory() {
    if !common::tmux_available() {
        return;
    }

    let (_dir, path) = common::create_temp_git_repo();
    let session_name = format!("orchard-test-{}", std::process::id());

    let result = orchard::tmux::new_detached_session(&session_name, &path);
    assert!(result.is_ok(), "failed to create session: {:?}", result);

    // Verify it exists
    let sessions = orchard::tmux::list_tmux_sessions();
    assert!(sessions.iter().any(|s| s.name == session_name));

    // Cleanup
    let _ = orchard::tmux::kill_tmux_session(&session_name);
}

#[test]
#[ignore]
fn existing_session_for_worktree_is_reused() {
    if !common::tmux_available() {
        return;
    }

    let (_dir, path) = common::create_temp_git_repo();
    let session_name = format!("orchard-test-reuse-{}", std::process::id());

    use orchard::types::SwitchToSessionOptions;

    let opts = SwitchToSessionOptions {
        session_name: session_name.clone(),
        worktree_path: path.clone(),
        branch: Some("main".to_string()),
        pr: None,
    };

    // Create once
    orchard::tmux::create_session(&opts).expect("first create_session failed");

    // Create again — should be idempotent
    let result = orchard::tmux::create_session(&opts);
    assert!(result.is_ok(), "second create_session failed: {:?}", result);

    // Cleanup
    let _ = orchard::tmux::kill_tmux_session(&session_name);
}
