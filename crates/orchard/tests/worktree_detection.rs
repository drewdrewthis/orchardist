//! Integration regression test for #239 — worktrees under `.worktrees/` must
//! appear in `orchard --json` output for flat-clone repositories.
//!
//! Reported symptom (2026-04-13): `orchard --json` only returned the `main`
//! worktree for `drewdrewthis/git-orchard-rs` (a flat clone with worktrees
//! under `.worktrees/`), while the langwatch submodule repo in the same
//! config returned all of its worktrees correctly.
//!
//! The bug no longer reproduces on current `main` (the hexagonal-backend
//! refactor in #284 reworked worktree plumbing and introduced
//! `WorktreeLayout`). This test pins the expected behavior so the
//! regression cannot silently return.
//!
//! ## Scenario
//!
//! 1. Create a flat-clone git repo (has `.git/` directory, not bare, not a
//!    submodule).
//! 2. Add a linked worktree under `.worktrees/<name>/` on a feature branch.
//! 3. Point a fixture global config at the flat clone.
//! 4. Run `orchard --json` and assert both the main worktree AND the
//!    `.worktrees/` entry appear under the repo's `worktrees` array.

mod common;

use assert_cmd::Command;
use serde_json::Value;

const SLUG: &str = "acme/flat-clone-app";
const FEATURE_BRANCH: &str = "feat/issue-239-regression";

/// Canonicalizes a path for comparison. On macOS `/var/folders/...` is a
/// symlink to `/private/var/folders/...`, so tempfile-created paths come back
/// from `orchard` in resolved form — tests must compare against the resolved
/// path, not the pre-resolution temp path.
fn canon(p: &str) -> String {
    std::fs::canonicalize(p)
        .map(|pb| pb.display().to_string())
        .unwrap_or_else(|_| p.to_string())
}

/// Creates a flat-clone repo with one `.worktrees/` entry, runs
/// `orchard --json`, and returns the parsed JSON value (or `None` if the
/// binary exits non-zero — e.g. `gh` unavailable in CI).
fn run_json_against_flat_clone_with_worktree() -> Option<(Value, String, String)> {
    let home = tempfile::TempDir::new().expect("create temp HOME");
    let repo = common::TestRepo::new();

    // Seed the repo with an initial commit so `git worktree add` succeeds.
    std::fs::write(repo.path().join("README.md"), "seed").expect("write README");
    std::process::Command::new("git")
        .args(["add", "README.md"])
        .current_dir(repo.path())
        .output()
        .expect("git add");
    std::process::Command::new("git")
        .args(["commit", "-m", "seed"])
        .current_dir(repo.path())
        .output()
        .expect("git commit");

    // Create a linked worktree under `.worktrees/` on a new branch — this is
    // the exact layout the bug reported missing.
    let worktree_path = repo.path().join(".worktrees").join("issue239-regression");
    let status = std::process::Command::new("git")
        .args([
            "worktree",
            "add",
            "-b",
            FEATURE_BRANCH,
            worktree_path.to_str().expect("utf-8 path"),
        ])
        .current_dir(repo.path())
        .status()
        .expect("git worktree add");
    assert!(status.success(), "git worktree add must succeed");

    // Write a global config pointing to our flat clone.
    let config_dir = home.path().join(".config").join("orchard");
    std::fs::create_dir_all(&config_dir).expect("create config dir");
    let config_json = format!(
        r#"{{"repos":[{{"slug":"{SLUG}","path":"{}"}}]}}"#,
        repo.path().display()
    );
    std::fs::write(config_dir.join("config.json"), &config_json).expect("write config.json");

    // Fresh cache dir under the fake HOME so we don't read the developer's cache.
    let home_cache = home.path().join(".cache").join("orchard");
    std::fs::create_dir_all(&home_cache).expect("create cache dir");

    let output = Command::cargo_bin("orchard")
        .unwrap()
        .arg("--json")
        .current_dir(repo.path())
        .env("HOME", home.path())
        .env_remove("TMUX")
        .output()
        .unwrap();

    if !output.status.success() {
        eprintln!(
            "SKIP worktree_detection: orchard --json exited with {:?}. stderr: {}",
            output.status.code(),
            String::from_utf8_lossy(&output.stderr).trim()
        );
        return None;
    }

    let stdout = String::from_utf8_lossy(&output.stdout).into_owned();
    let parsed: Value = serde_json::from_str(&stdout).expect("stdout should be valid JSON");
    Some((
        parsed,
        canon(&repo.path().display().to_string()),
        canon(&worktree_path.display().to_string()),
    ))
}

/// Regression guard for #239.
///
/// Given a flat-clone repo with one linked worktree under `.worktrees/`,
/// `orchard --json` must surface both the main worktree and the
/// `.worktrees/` entry for that repo.
#[test]
fn json_detects_worktrees_under_dot_worktrees_for_flat_clone() {
    let Some((output, main_path, wt_path)) = run_json_against_flat_clone_with_worktree() else {
        return;
    };

    let repo = output["repos"]
        .as_array()
        .expect("repos array present")
        .iter()
        .find(|r| r["slug"] == SLUG)
        .unwrap_or_else(|| panic!("repo {SLUG} must be present in JSON output"));

    let worktrees = repo["worktrees"]
        .as_array()
        .expect("worktrees array present");

    let paths: Vec<&str> = worktrees
        .iter()
        .filter_map(|w| w["path"].as_str())
        .collect();

    assert!(
        paths.iter().any(|p| *p == main_path),
        "main worktree path {main_path} missing from output; got {paths:?}"
    );
    assert!(
        paths.iter().any(|p| *p == wt_path),
        ".worktrees/ entry {wt_path} missing from output — #239 regression; got {paths:?}"
    );

    let feature = worktrees
        .iter()
        .find(|w| w["branch"] == FEATURE_BRANCH)
        .unwrap_or_else(|| {
            panic!("worktree for branch {FEATURE_BRANCH} missing — #239 regression")
        });
    assert_eq!(
        feature["path"].as_str(),
        Some(wt_path.as_str()),
        "feature worktree path must match the .worktrees/ location"
    );
}
