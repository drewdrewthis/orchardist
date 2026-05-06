//! End-to-end CLI tests for `orchard-worktree`.
//!
//! Each test creates a real git repo in a temp dir, runs the binary, and
//! asserts on stdout/stderr/exit-code/repo state. This exercises the same
//! `git` calls a user would.

use std::path::Path;
use std::process::Command;

fn binary() -> &'static str {
    env!("CARGO_BIN_EXE_orchard-worktree")
}

fn git(repo: &Path, args: &[&str]) {
    let status = Command::new("git")
        .args(args)
        .current_dir(repo)
        .status()
        .expect("run git");
    assert!(
        status.success(),
        "git {args:?} failed in {}",
        repo.display()
    );
}

/// Initialize a temp git repo with one commit on `main`. Returns its path.
fn init_repo() -> tempfile::TempDir {
    let dir = tempfile::TempDir::with_prefix("orchard-worktree-test").unwrap();
    git(dir.path(), &["init", "-b", "main", "-q"]);
    git(dir.path(), &["config", "user.email", "test@example.com"]);
    git(dir.path(), &["config", "user.name", "Test"]);
    git(dir.path(), &["commit", "--allow-empty", "-m", "init", "-q"]);
    dir
}

fn run(repo: &Path, args: &[&str]) -> std::process::Output {
    Command::new(binary())
        .args(args)
        .current_dir(repo)
        .output()
        .expect("run orchard-worktree")
}

#[test]
fn ls_default_repo_shows_main() {
    let repo = init_repo();
    let out = run(repo.path(), &["ls"]);
    assert!(
        out.status.success(),
        "stderr: {:?}",
        String::from_utf8_lossy(&out.stderr)
    );
    let stdout = String::from_utf8_lossy(&out.stdout);
    assert!(stdout.contains("main"), "expected 'main' in: {stdout}");
}

#[test]
fn ls_json_emits_versioned_schema() {
    let repo = init_repo();
    let out = run(repo.path(), &["ls", "--json"]);
    assert!(out.status.success());
    let stdout = String::from_utf8_lossy(&out.stdout);
    let parsed: serde_json::Value = serde_json::from_str(&stdout).expect("valid JSON");
    assert_eq!(parsed["version"], 1);
    assert!(parsed["worktrees"].is_array());
}

#[test]
fn new_creates_worktree_for_new_branch() {
    let repo = init_repo();
    let out = run(repo.path(), &["new", "feature/x"]);
    assert!(
        out.status.success(),
        "stderr: {:?}",
        String::from_utf8_lossy(&out.stderr)
    );
    let stdout = String::from_utf8_lossy(&out.stdout);
    assert!(stdout.contains("created branch 'feature/x'"));
    assert!(repo.path().join(".worktrees/feature-x").exists());
}

#[test]
fn new_is_idempotent_on_re_invocation() {
    let repo = init_repo();
    let _ = run(repo.path(), &["new", "feature/idem"]);
    let out = run(repo.path(), &["new", "feature/idem"]);
    assert!(out.status.success());
    let stdout = String::from_utf8_lossy(&out.stdout);
    assert!(stdout.contains("already exists"), "stdout: {stdout}");
}

#[test]
fn rm_removes_existing_worktree() {
    let repo = init_repo();
    let _ = run(repo.path(), &["new", "feature/torm"]);
    assert!(repo.path().join(".worktrees/feature-torm").exists());

    let out = run(repo.path(), &["rm", "feature/torm"]);
    assert!(
        out.status.success(),
        "stderr: {:?}",
        String::from_utf8_lossy(&out.stderr)
    );
    assert!(!repo.path().join(".worktrees/feature-torm").exists());
}

#[test]
fn rm_fails_for_unknown_branch() {
    let repo = init_repo();
    let out = run(repo.path(), &["rm", "nope/never"]);
    assert!(!out.status.success(), "expected non-zero exit");
    assert_eq!(out.status.code(), Some(3), "should be precondition-failed");
    let stderr = String::from_utf8_lossy(&out.stderr);
    assert!(stderr.contains("no worktree found for branch 'nope/never'"));
}

#[test]
fn path_prints_absolute_worktree_path() {
    let repo = init_repo();
    let _ = run(repo.path(), &["new", "feature/path"]);
    let out = run(repo.path(), &["path", "feature/path"]);
    assert!(out.status.success());
    let stdout = String::from_utf8_lossy(&out.stdout).trim().to_string();
    assert!(
        stdout.ends_with(".worktrees/feature-path") || stdout.contains("/feature-path"),
        "stdout: {stdout}"
    );
}

#[test]
fn prune_all_removes_every_non_main_worktree() {
    let repo = init_repo();
    let _ = run(repo.path(), &["new", "feature/a"]);
    let _ = run(repo.path(), &["new", "feature/b"]);

    let out = run(repo.path(), &["prune", "--all"]);
    assert!(
        out.status.success(),
        "stderr: {:?}",
        String::from_utf8_lossy(&out.stderr)
    );
    assert!(!repo.path().join(".worktrees/feature-a").exists());
    assert!(!repo.path().join(".worktrees/feature-b").exists());
}

#[test]
fn prune_without_filter_errors() {
    let repo = init_repo();
    let out = run(repo.path(), &["prune"]);
    assert!(!out.status.success());
    let stderr = String::from_utf8_lossy(&out.stderr);
    assert!(stderr.contains("specify --all"), "stderr: {stderr}");
}

#[test]
fn outside_a_repo_errors_cleanly() {
    let dir = tempfile::TempDir::with_prefix("not-a-repo").unwrap();
    let out = Command::new(binary())
        .args(["new", "anything"])
        .current_dir(dir.path())
        .output()
        .unwrap();
    assert!(!out.status.success());
    assert_eq!(out.status.code(), Some(3));
    let stderr = String::from_utf8_lossy(&out.stderr);
    assert!(
        stderr.contains("not in a git repository"),
        "stderr: {stderr}"
    );
}
