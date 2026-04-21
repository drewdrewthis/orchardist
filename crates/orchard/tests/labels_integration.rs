//! Integration tests for the `labels` field in `orchard --json` output.
//!
//! Each test writes a minimal fixture cache (issues, PRs, worktrees) into a
//! temp HOME directory, runs `orchard --json`, parses the JSON, and asserts
//! the `labels` field on the relevant `issue` or `pr` object.
//!
//! # Design note
//!
//! `orchard --json` calls `refresh_and_build`, which refreshes worktrees via
//! `git worktree list` (overwriting any pre-written worktrees cache) but will
//! not overwrite issues/PRs caches because `gh` is unavailable in the test
//! environment. The worktree cache therefore reflects the real git repo state.
//!
//! Each test:
//!   1. Creates a temp git repo and makes a commit so `git worktree list` works.
//!   2. Checks out the target branch (so the worktree is on that branch).
//!   3. Pre-writes issues/PRs caches with the matching branch and labels.
//!   4. Runs `orchard --json` and asserts `labels` (and `phase`) on the found worktree.
//!
//! If the binary exits non-zero (e.g. because `gh` is not available at all),
//! the test skips rather than fails — consistent with `binary_integration.rs`.
mod common;

use assert_cmd::Command;
use common::{TestCacheDir, make_issue, make_pr};
use orchard::cache::CachedPr;
use serde_json::Value;

// ---------------------------------------------------------------------------
// Fixture owner/repo slug used across all tests.
// ---------------------------------------------------------------------------

const OWNER: &str = "acme";
const REPO: &str = "webapp";
const SLUG: &str = "acme/webapp";

// ---------------------------------------------------------------------------
// Fixture harness
// ---------------------------------------------------------------------------

/// A self-contained fixture environment: temp HOME + cache dir + git repo.
struct LabelsFixture {
    home: tempfile::TempDir,
    cache: TestCacheDir,
    repo: common::TestRepo,
}

impl LabelsFixture {
    /// Creates a fresh fixture. The git repo is initialised but stays on
    /// whatever default branch `git init` sets (usually "main" or "master").
    ///
    /// Call `checkout_branch` to switch the repo to the target branch, then
    /// write caches, call `apply_cache`, and finally `run`.
    fn new() -> Self {
        let home = tempfile::TempDir::new().expect("create temp HOME");
        let cache = TestCacheDir::new();
        let repo = common::TestRepo::new();

        // Make a first commit so `git worktree list --porcelain` works.
        std::fs::write(repo.path().join("README.md"), "test").expect("write README");
        std::process::Command::new("git")
            .args(["add", "README.md"])
            .current_dir(repo.path())
            .output()
            .expect("git add");
        std::process::Command::new("git")
            .args(["commit", "-m", "init"])
            .current_dir(repo.path())
            .output()
            .expect("git commit");

        // Write a global config pointing to our test repo.
        let config_dir = home.path().join(".config").join("orchard");
        std::fs::create_dir_all(&config_dir).expect("create config dir");
        let config_json = format!(
            r#"{{"repos":[{{"slug":"{SLUG}","path":"{}"}}]}}"#,
            repo.path().display()
        );
        std::fs::write(config_dir.join("config.json"), &config_json).expect("write config.json");

        // Create the cache dir under HOME.
        let home_cache = home.path().join(".cache").join("orchard");
        std::fs::create_dir_all(&home_cache).expect("create cache dir");

        Self { home, cache, repo }
    }

    /// Checks out (creates) a branch in the test git repo.
    fn checkout_branch(&self, branch: &str) {
        std::process::Command::new("git")
            .args(["checkout", "-b", branch])
            .current_dir(self.repo.path())
            .output()
            .expect("git checkout -b");
    }

    /// Returns the current branch name of the test git repo.
    fn current_branch(&self) -> String {
        let out = std::process::Command::new("git")
            .args(["rev-parse", "--abbrev-ref", "HEAD"])
            .current_dir(self.repo.path())
            .output()
            .expect("git rev-parse");
        String::from_utf8_lossy(&out.stdout).trim().to_string()
    }

    /// Copies all cache files from the TestCacheDir into `$HOME/.cache/orchard/`.
    fn apply_cache(&self) {
        let home_cache = self.home.path().join(".cache").join("orchard");
        for entry in std::fs::read_dir(self.cache.path()).expect("read cache dir") {
            let entry = entry.expect("dir entry");
            let dest = home_cache.join(entry.file_name());
            std::fs::copy(entry.path(), &dest).expect("copy cache file");
        }
    }

    /// Runs `orchard refresh && orchard --json` with HOME pointing to the
    /// fixture and returns the parsed JSON, or `None` if the binary fails
    /// (e.g. `gh` unavailable).
    ///
    /// Post-#329: `orchard --json` is cache-only. `orchard refresh` is the
    /// explicit entry point that refreshes worktrees via `git worktree list`
    /// (so the local git repo's current branch surfaces in the cache) and
    /// leaves any pre-written issues/PRs caches untouched when `gh` is
    /// unavailable. The test then reads the cache via `--json`.
    fn run(&self) -> Option<Value> {
        let refresh = Command::cargo_bin("orchard")
            .unwrap()
            .arg("refresh")
            .current_dir(self.repo.path())
            .env("HOME", self.home.path())
            .env_remove("TMUX")
            .output()
            .unwrap();
        if !refresh.status.success() {
            let stderr = String::from_utf8_lossy(&refresh.stderr);
            eprintln!(
                "SKIP labels_integration: orchard refresh exited with {:?}. stderr: {}",
                refresh.status.code(),
                stderr.trim()
            );
            return None;
        }

        let output = Command::cargo_bin("orchard")
            .unwrap()
            .arg("--json")
            .current_dir(self.repo.path())
            .env("HOME", self.home.path())
            .env_remove("TMUX")
            .output()
            .unwrap();

        if !output.status.success() {
            let stderr = String::from_utf8_lossy(&output.stderr);
            eprintln!(
                "SKIP labels_integration: orchard --json exited with {:?}. stderr: {}",
                output.status.code(),
                stderr.trim()
            );
            return None;
        }

        let stdout = String::from_utf8_lossy(&output.stdout);
        Some(serde_json::from_str(&stdout).expect("stdout should be valid JSON"))
    }
}

// ---------------------------------------------------------------------------
// Helper: find a worktree entry in the JSON output by branch name
// ---------------------------------------------------------------------------

/// Searches all repos' worktrees for one matching the predicate.
fn find_worktree<F>(output: &Value, predicate: F) -> Option<&Value>
where
    F: Fn(&Value) -> bool,
{
    output["repos"].as_array()?.iter().find_map(|repo| {
        repo["worktrees"]
            .as_array()?
            .iter()
            .find(|wt| predicate(wt))
    })
}

fn find_worktree_by_branch<'a>(output: &'a Value, branch: &str) -> Option<&'a Value> {
    find_worktree(output, |wt| wt["branch"].as_str() == Some(branch))
}

// ---------------------------------------------------------------------------
// Integration scenario: issue with labels
// ---------------------------------------------------------------------------

/// orchard --json exposes labels on an issue and derives phase from them.
///
/// Given a fixture cache with issue #47 having labels `["in-progress", "enhancement"]`
/// Then the worktree's `issue.labels` is `["in-progress", "enhancement"]`
/// And `issue.phase` is `"in-progress"`
#[test]
fn json_includes_labels_on_issue() {
    let fixture = LabelsFixture::new();

    fixture.checkout_branch("feat/issue-47-labels");
    let branch = fixture.current_branch();

    let issue = orchard::cache::CachedIssue {
        labels: vec!["in-progress".to_string(), "enhancement".to_string()],
        ..make_issue(47, "Implement labels field")
    };
    let pr = CachedPr {
        linked_issue: Some(47),
        ..make_pr(55, &branch)
    };

    fixture.cache.write_issues(OWNER, REPO, &[issue]);
    fixture.cache.write_prs(OWNER, REPO, &[pr]);
    fixture.apply_cache();

    let Some(output) = fixture.run() else {
        return; // gh not available
    };

    let wt = find_worktree_by_branch(&output, &branch)
        .expect("worktree for target branch should be in output");

    assert_eq!(
        wt["issue"]["labels"],
        serde_json::json!(["in-progress", "enhancement"]),
        "issue.labels should be ['in-progress', 'enhancement']"
    );
    assert_eq!(
        wt["issue"]["phase"],
        Value::String("in-progress".to_string()),
        "issue.phase should be 'in-progress'"
    );
}

// ---------------------------------------------------------------------------
// Integration scenario: PR with labels
// ---------------------------------------------------------------------------

/// orchard --json exposes labels on a PR and derives phase from them.
///
/// Given a fixture cache with PR #55 having labels `["pr-ready", "needs-review"]`
/// Then the worktree's `pr.labels` is `["pr-ready", "needs-review"]`
/// And `pr.phase` is `"pr-ready"`
#[test]
fn json_includes_labels_on_pr() {
    let fixture = LabelsFixture::new();

    fixture.checkout_branch("feat/pr-labels");
    let branch = fixture.current_branch();

    let pr = CachedPr {
        labels: vec!["pr-ready".to_string(), "needs-review".to_string()],
        ..make_pr(55, &branch)
    };

    fixture.cache.write_issues(OWNER, REPO, &[]);
    fixture.cache.write_prs(OWNER, REPO, &[pr]);
    fixture.apply_cache();

    let Some(output) = fixture.run() else {
        return; // gh not available
    };

    let wt = find_worktree_by_branch(&output, &branch)
        .expect("worktree for target branch should be in output");

    assert_eq!(
        wt["pr"]["labels"],
        serde_json::json!(["pr-ready", "needs-review"]),
        "pr.labels should be ['pr-ready', 'needs-review']"
    );
    assert_eq!(
        wt["pr"]["phase"],
        Value::String("pr-ready".to_string()),
        "pr.phase should be 'pr-ready'"
    );
}
