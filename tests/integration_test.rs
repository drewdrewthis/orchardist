mod common;

use std::process::Command;

use assert_cmd::Command as BinCommand;
use common::{GitFixture, TmuxFixture};

// ---------------------------------------------------------------------------
// Phase 1: Test Harness
// ---------------------------------------------------------------------------

/// Scenario: Git fixture creates a disposable repo with worktrees
#[test]
fn git_fixture_creates_a_disposable_repo_with_worktrees() {
    let fixture = GitFixture::new();
    let path = fixture.path();

    // Directory exists and is a git repo on branch "main".
    let branch_out = Command::new("git")
        .args(["-C", path, "rev-parse", "--abbrev-ref", "HEAD"])
        .output()
        .expect("git rev-parse failed");
    let branch = String::from_utf8_lossy(&branch_out.stdout).trim().to_string();
    assert_eq!(branch, "main");

    // Repo has at least one commit.
    let log_out = Command::new("git")
        .args(["-C", path, "log", "--oneline"])
        .output()
        .expect("git log failed");
    assert!(!log_out.stdout.is_empty(), "expected at least one commit");

    // git worktree list returns the main worktree.
    let wt_out = Command::new("git")
        .args(["-C", path, "worktree", "list"])
        .output()
        .expect("git worktree list failed");
    let wt_text = String::from_utf8_lossy(&wt_out.stdout);
    assert!(wt_text.contains(path), "worktree list should contain main path");
}

/// Scenario: Git fixture can add worktrees
#[test]
fn git_fixture_can_add_worktrees() {
    let fixture = GitFixture::new();
    let wt_path = fixture.add_worktree("feature/test-branch");

    let wt_out = Command::new("git")
        .args(["-C", fixture.path(), "worktree", "list"])
        .output()
        .expect("git worktree list failed");
    let wt_text = String::from_utf8_lossy(&wt_out.stdout);

    assert!(wt_text.contains(fixture.path()), "main worktree should appear");
    assert!(wt_text.contains(&wt_path), "feature worktree should appear");
}

/// Scenario: Tmux fixture creates and cleans up sessions
#[test]
fn tmux_fixture_creates_and_cleans_up_sessions() {
    if !common::tmux_available() {
        eprintln!("tmux not available — skipping");
        return;
    }

    let fixture = TmuxFixture::new();
    let session_name = fixture.create_session("abc");

    assert!(
        fixture.session_exists(&session_name),
        "session should exist after create_session"
    );

    // Dropping the fixture kills the session.
    drop(fixture);

    // Check that the session is gone.
    let still_exists = Command::new("tmux")
        .args(["has-session", "-t", &session_name])
        .status()
        .map(|s| s.success())
        .unwrap_or(false);
    assert!(!still_exists, "session should be killed after fixture drop");
}

/// Scenario: Binary runner captures stdout and stderr (--help)
#[test]
fn binary_runner_captures_stdout_and_stderr() {
    BinCommand::cargo_bin("orchard")
        .unwrap()
        .arg("--help")
        .assert()
        .success()
        .stderr(predicates::str::contains("Usage:"));
}

// ---------------------------------------------------------------------------
// Phase 2: Tmux Session Management
// ---------------------------------------------------------------------------

/// Scenario: Session creation at worktree directory via real tmux
#[test]
fn session_creation_at_worktree_directory_via_real_tmux() {
    if !common::tmux_available() {
        eprintln!("tmux not available — skipping");
        return;
    }

    let fixture = GitFixture::new();
    let wt_path = fixture.add_worktree("feature/login");

    // Derive the repo name the same way the library does — including sanitization.
    let raw_repo_name = std::path::Path::new(fixture.path())
        .file_name()
        .and_then(|n| n.to_str())
        .unwrap_or("orchard");
    let repo_name = orchard::tmux::sanitize_repo_name(raw_repo_name);
    let session_name = orchard::tmux::derive_session_name(&repo_name, Some("feature/login"), &wt_path);

    let opts = orchard::types::SwitchToSessionOptions {
        session_name: session_name.clone(),
        worktree_path: wt_path.clone(),
        branch: Some("feature/login".to_string()),
        pr: None,
    };
    orchard::tmux::create_session(&opts).expect("create_session failed");

    // Verify the session exists and is at the worktree path.
    let sessions = orchard::tmux::list_tmux_sessions();
    let found = sessions.iter().find(|s| s.name == session_name);
    assert!(
        found.is_some(),
        "expected session '{}' to exist in tmux",
        session_name
    );
    assert_eq!(found.unwrap().path, wt_path);

    let _ = orchard::tmux::kill_tmux_session(&session_name);
}

/// Scenario: Existing session is reused on repeat create_session call
#[test]
fn existing_session_is_reused_on_repeat_create_session_call() {
    if !common::tmux_available() {
        eprintln!("tmux not available — skipping");
        return;
    }

    let fixture = GitFixture::new();
    let wt_path = fixture.add_worktree("feature/login");

    let raw_repo_name = std::path::Path::new(fixture.path())
        .file_name()
        .and_then(|n| n.to_str())
        .unwrap_or("orchard");
    let repo_name = orchard::tmux::sanitize_repo_name(raw_repo_name);
    let session_name = orchard::tmux::derive_session_name(&repo_name, Some("feature/login"), &wt_path);

    let opts = orchard::types::SwitchToSessionOptions {
        session_name: session_name.clone(),
        worktree_path: wt_path.clone(),
        branch: Some("feature/login".to_string()),
        pr: None,
    };

    // First call creates the session.
    orchard::tmux::create_session(&opts).expect("first create_session failed");
    // Second call must be idempotent — no error, no duplicate.
    orchard::tmux::create_session(&opts).expect("second create_session failed");

    let sessions = orchard::tmux::list_tmux_sessions();
    let count = sessions.iter().filter(|s| s.name == session_name).count();
    assert_eq!(count, 1, "expected exactly one session, found {count}");

    let _ = orchard::tmux::kill_tmux_session(&session_name);
}

/// Scenario: list_tmux_sessions returns real tmux sessions
#[test]
fn list_tmux_sessions_returns_real_tmux_sessions() {
    if !common::tmux_available() {
        eprintln!("tmux not available — skipping");
        return;
    }

    let tmux = TmuxFixture::new();
    let name_a = tmux.create_session("a");
    let name_b = tmux.create_session("b");

    let sessions = orchard::tmux::list_tmux_sessions();

    let has_a = sessions.iter().any(|s| s.name == name_a);
    let has_b = sessions.iter().any(|s| s.name == name_b);

    assert!(has_a, "expected session '{name_a}' in list");
    assert!(has_b, "expected session '{name_b}' in list");

    // Each session has a name, path, and attached flag (the struct guarantees the fields exist).
    for s in sessions.iter().filter(|s| s.name == name_a || s.name == name_b) {
        assert!(!s.name.is_empty(), "session name must not be empty");
        // path may be empty on some platforms if not set explicitly — just confirm the field exists
        let _ = &s.path;
        let _ = s.attached;
    }
}

/// Scenario: find_session_for_worktree matches by path against real sessions
#[test]
fn find_session_for_worktree_matches_by_path_against_real_sessions() {
    if !common::tmux_available() {
        eprintln!("tmux not available — skipping");
        return;
    }

    let fixture = GitFixture::new();
    let repo_path = fixture.path().to_string();

    let tmux = TmuxFixture::new();
    tmux.create_session_at("repo", &repo_path);

    let sessions = orchard::tmux::list_tmux_sessions();
    let found = orchard::tmux::find_session_for_worktree(&sessions, &repo_path, None);

    assert!(
        found.is_some(),
        "expected to find a session at path '{repo_path}'"
    );
    assert_eq!(found.unwrap().path, repo_path);
}

// ---------------------------------------------------------------------------
// Phase 2: Main Session
// ---------------------------------------------------------------------------

/// Scenario: ensure_main_session creates session at worktree origin
#[test]
fn ensure_main_session_creates_session_at_worktree_origin() {
    if !common::tmux_available() {
        eprintln!("tmux not available — skipping");
        return;
    }

    let fixture = GitFixture::new();
    let repo_path = fixture.path().to_string();
    let expected_session = orchard::tmux::derive_main_session_name(&repo_path, Some("main"));

    // Build a minimal worktree list for this repo.
    let trees = vec![orchard::types::Worktree {
        path: repo_path.clone(),
        branch: Some("main".to_string()),
        ..Default::default()
    }];

    // Provide an empty session list so ensure_main_session must create the session.
    let sessions = orchard::collector::ensure_main_session(&trees, vec![], &|e| {
        panic!("ensure_main_session error: {e}")
    });

    assert!(
        sessions.iter().any(|s| s.name == expected_session),
        "expected session '{expected_session}' in returned list"
    );

    // Confirm tmux actually has the session.
    let live = orchard::tmux::list_tmux_sessions();
    assert!(
        live.iter().any(|s| s.name == expected_session),
        "expected session '{expected_session}' to exist in tmux"
    );

    let _ = orchard::tmux::kill_tmux_session(&expected_session);
}

/// Scenario: ensure_main_session is idempotent
#[test]
fn ensure_main_session_is_idempotent() {
    if !common::tmux_available() {
        eprintln!("tmux not available — skipping");
        return;
    }

    let fixture = GitFixture::new();
    let repo_path = fixture.path().to_string();
    let expected_session = orchard::tmux::derive_main_session_name(&repo_path, Some("main"));

    let trees = vec![orchard::types::Worktree {
        path: repo_path.clone(),
        branch: Some("main".to_string()),
        ..Default::default()
    }];

    // First call — creates the session.
    let sessions_after_first = orchard::collector::ensure_main_session(&trees, vec![], &|e| {
        panic!("first ensure_main_session error: {e}")
    });

    // Second call — must be idempotent.
    let sessions_after_second =
        orchard::collector::ensure_main_session(&trees, sessions_after_first, &|e| {
            panic!("second ensure_main_session error: {e}")
        });

    let count = sessions_after_second
        .iter()
        .filter(|s| s.name == expected_session)
        .count();
    assert_eq!(count, 1, "expected exactly one session '{expected_session}', found {count}");

    let _ = orchard::tmux::kill_tmux_session(&expected_session);
}

// ---------------------------------------------------------------------------
// Phase 2: Config Round-trip
// ---------------------------------------------------------------------------

/// Scenario: Config round-trip with parse_config
#[test]
fn config_round_trip_with_parse_config() {
    let fixture = GitFixture::new();

    let json = r#"{"remote":{"host":"myhost","repoPath":"/srv/repo","shell":"ssh"}}"#;
    fixture.write_orchard_config(json);

    let data = std::fs::read(format!("{}/.git/orchard.json", fixture.path()))
        .expect("orchard.json not found");

    let config = orchard::config::parse_config(&data, "orchard.json");

    let remote = config.remote.expect("expected a remote");
    assert_eq!(remote.host, "myhost");
    assert_eq!(remote.repo_path, "/srv/repo");
    assert_eq!(remote.shell, "ssh");
}

// ---------------------------------------------------------------------------
// Phase 3: E2E Binary Tests
// ---------------------------------------------------------------------------

/// Scenario: orchard --json outputs valid JSON worktree array
#[test]
fn orchard_json_outputs_valid_json_worktree_array() {
    let fixture = GitFixture::new();

    let output = BinCommand::cargo_bin("orchard")
        .unwrap()
        .arg("--json")
        .current_dir(fixture.path())
        .env_remove("TMUX")
        .output()
        .expect("failed to run orchard --json");

    assert!(output.status.success(), "orchard --json exited with non-zero status");

    let stdout = String::from_utf8_lossy(&output.stdout);
    let parsed: serde_json::Value =
        serde_json::from_str(&stdout).expect("stdout is not valid JSON");

    assert!(parsed.is_array(), "expected JSON array, got: {parsed}");

    let arr = parsed.as_array().unwrap();
    assert!(!arr.is_empty(), "expected at least one worktree in JSON output");

    for obj in arr {
        assert!(obj.get("path").is_some(), "each object must have 'path'");
        assert!(obj.get("isBare").is_some(), "each object must have 'isBare'");
    }
}

/// Scenario: orchard --json includes branch information
#[test]
fn orchard_json_includes_branch_information() {
    let fixture = GitFixture::new();

    let output = BinCommand::cargo_bin("orchard")
        .unwrap()
        .arg("--json")
        .current_dir(fixture.path())
        .env_remove("TMUX")
        .output()
        .expect("failed to run orchard --json");

    assert!(output.status.success());

    let stdout = String::from_utf8_lossy(&output.stdout);
    let parsed: serde_json::Value = serde_json::from_str(&stdout).expect("stdout is not valid JSON");
    let arr = parsed.as_array().unwrap();

    let has_main_branch = arr.iter().any(|obj| {
        obj.get("branch")
            .and_then(|v| v.as_str())
            .map(|b| b == "main")
            .unwrap_or(false)
    });

    assert!(has_main_branch, "expected at least one worktree with branch 'main'");
}

/// Scenario: orchard --help exits successfully
#[test]
fn orchard_help_exits_successfully() {
    BinCommand::cargo_bin("orchard")
        .unwrap()
        .arg("--help")
        .assert()
        .success()
        .stderr(predicates::str::contains("Usage:"));
}
