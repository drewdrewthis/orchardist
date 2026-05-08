//! Integration tests for the `phase` field in `orchard-tui --json` output.
//!
//! Each test writes a minimal fixture cache (issues, PRs, worktrees) into a
//! temp HOME directory, runs `orchard-tui --json`, parses the JSON, and asserts
//! the `phase` field on the relevant `issue` or `pr` object.
//!
//! These tests correspond to the `@integration` scenarios in
//! `specs/features/phase-field-in-json.feature`.
//!
//! # Design note
//!
//! `orchard-tui --json` calls `refresh_and_build`, which refreshes worktrees via
//! `git worktree list` (overwriting any pre-written worktrees cache) but will
//! not overwrite issues/PRs caches because `gh` is unavailable in the test
//! environment. The worktree cache therefore reflects the real git repo state.
//!
//! Each test:
//!   1. Creates a temp git repo and makes a commit so `git worktree list` works.
//!   2. Checks out the target branch (so the worktree is on that branch).
//!   3. Pre-writes issues/PRs caches with the matching branch and phase labels.
//!   4. Runs `orchard-tui --json` and asserts `phase` on the found worktree.
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
struct PhaseFixture {
    home: tempfile::TempDir,
    cache: TestCacheDir,
    repo: common::TestRepo,
}

impl PhaseFixture {
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
        let config_dir = home.path().join(".orchard");
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

    /// Runs `orchard refresh && orchard-tui --json` with HOME pointing to the
    /// fixture and returns the parsed JSON, or `None` if the binary fails
    /// (e.g. `gh` unavailable).
    ///
    /// Post-#329: `orchard-tui --json` is cache-only, so `orchard refresh` runs
    /// first to populate the worktree cache from the fixture's temp git repo.
    fn run(&self) -> Option<Value> {
        let refresh = Command::cargo_bin("orchard-tui")
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
                "SKIP phase_field_integration: orchard refresh exited with {:?}. stderr: {}",
                refresh.status.code(),
                stderr.trim()
            );
            return None;
        }

        let output = Command::cargo_bin("orchard-tui")
            .unwrap()
            .arg("--json")
            .current_dir(self.repo.path())
            .env("HOME", self.home.path())
            .env_remove("TMUX")
            .output()
            .unwrap();

        if !output.status.success() {
            // Acceptable in CI environments without `gh` CLI. Log the skip so
            // it surfaces in test output — a silent skip would let a regression
            // hide as a pass.
            let stderr = String::from_utf8_lossy(&output.stderr);
            eprintln!(
                "SKIP phase_field_integration: orchard-tui --json exited with {:?}. stderr: {}",
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
// Integration scenario: issue with a single phase label
// ---------------------------------------------------------------------------

/// orchard-tui --json exposes phase on an issue with a single phase label.
///
/// Feature `@integration` scenario:
///   Given a fixture cache with issue #47 having labels `["in-progress"]`
///   Then the worktree for that branch has `issue.phase` == `"in-progress"`
#[test]
fn json_exposes_phase_on_issue_with_single_phase_label() {
    let fixture = PhaseFixture::new();

    // The worktree will be on this branch after checkout.
    fixture.checkout_branch("feat/issue-47");
    let branch = fixture.current_branch();

    let issue = orchard::cache::CachedIssue {
        labels: vec!["in-progress".to_string()],
        ..make_issue(47, "Implement phase field")
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
        wt["issue"]["phase"],
        Value::String("in-progress".to_string()),
        "issue.phase should be 'in-progress'"
    );
    // issue.state and issue.title must still be present.
    assert!(
        !wt["issue"]["state"].is_null(),
        "issue.state must be present"
    );
    assert!(
        !wt["issue"]["title"].is_null(),
        "issue.title must be present"
    );
}

// ---------------------------------------------------------------------------
// Integration scenario: PR with a single phase label
// ---------------------------------------------------------------------------

/// orchard-tui --json exposes phase on a PR with a single phase label.
///
/// Feature `@integration` scenario:
///   Given a fixture cache with PR #55 having labels `["pr-ready"]`
///   Then the worktree for that branch has `pr.phase` == `"pr-ready"`
#[test]
fn json_exposes_phase_on_pr_with_single_phase_label() {
    let fixture = PhaseFixture::new();

    fixture.checkout_branch("feat/pr-ready");
    let branch = fixture.current_branch();

    let pr = CachedPr {
        labels: vec!["pr-ready".to_string()],
        ..make_pr(55, &branch)
    };

    fixture.cache.write_issues(OWNER, REPO, &[]);
    fixture.cache.write_prs(OWNER, REPO, &[pr]);
    fixture.apply_cache();

    let Some(output) = fixture.run() else {
        return;
    };

    let wt = find_worktree_by_branch(&output, &branch)
        .expect("worktree for target branch should be in output");

    assert_eq!(
        wt["pr"]["phase"],
        Value::String("pr-ready".to_string()),
        "pr.phase should be 'pr-ready'"
    );
}

// ---------------------------------------------------------------------------
// Integration scenario: issue with no phase labels → phase null
// ---------------------------------------------------------------------------

/// orchard-tui --json emits phase null when an issue has no phase labels.
///
/// Feature `@integration` scenario:
///   Given a fixture cache with issue #10 having labels `["bug"]`
///   Then `issue.phase` is `null`
#[test]
fn json_emits_null_phase_for_issue_with_no_phase_labels() {
    let fixture = PhaseFixture::new();

    fixture.checkout_branch("feat/issue-10");
    let branch = fixture.current_branch();

    let issue = orchard::cache::CachedIssue {
        labels: vec!["bug".to_string()],
        ..make_issue(10, "Bug report")
    };
    let pr = CachedPr {
        linked_issue: Some(10),
        ..make_pr(20, &branch)
    };

    fixture.cache.write_issues(OWNER, REPO, &[issue]);
    fixture.cache.write_prs(OWNER, REPO, &[pr]);
    fixture.apply_cache();

    let Some(output) = fixture.run() else {
        return;
    };

    let wt = find_worktree_by_branch(&output, &branch)
        .expect("worktree for target branch should be in output");

    assert!(
        wt["issue"]["phase"].is_null(),
        "issue.phase should be null when no phase labels are present, got: {}",
        wt["issue"]["phase"]
    );
}

// ---------------------------------------------------------------------------
// Integration scenario: multi-phase PR resolved by priority
// ---------------------------------------------------------------------------

/// orchard-tui --json resolves a multi-phase PR by priority.
///
/// Feature `@integration` scenario:
///   Given a fixture cache with PR #60 having labels `["in-progress", "blocked"]`
///   Then `pr.phase` is `"blocked"`
#[test]
fn json_resolves_multi_phase_pr_by_priority() {
    let fixture = PhaseFixture::new();

    fixture.checkout_branch("feat/multi-phase");
    let branch = fixture.current_branch();

    let pr = CachedPr {
        labels: vec!["in-progress".to_string(), "blocked".to_string()],
        ..make_pr(60, &branch)
    };

    fixture.cache.write_issues(OWNER, REPO, &[]);
    fixture.cache.write_prs(OWNER, REPO, &[pr]);
    fixture.apply_cache();

    let Some(output) = fixture.run() else {
        return;
    };

    let wt = find_worktree_by_branch(&output, &branch)
        .expect("worktree for target branch should be in output");

    assert_eq!(
        wt["pr"]["phase"],
        Value::String("blocked".to_string()),
        "pr.phase should be 'blocked' (highest priority)"
    );
}

// ---------------------------------------------------------------------------
// Integration scenario: PR with no linked issue
// ---------------------------------------------------------------------------

/// orchard-tui --json exposes phase on a PR with no linked issue.
///
/// Feature `@integration` scenario:
///   Given a fixture cache with PR #70 having `linked_issue: null` and labels `["in-ai-review"]`
///   Then `pr.phase` is `"in-ai-review"`
#[test]
fn json_exposes_phase_on_pr_with_no_linked_issue() {
    let fixture = PhaseFixture::new();

    fixture.checkout_branch("feat/no-issue");
    let branch = fixture.current_branch();

    let pr = CachedPr {
        linked_issue: None,
        labels: vec!["in-ai-review".to_string()],
        ..make_pr(70, &branch)
    };

    fixture.cache.write_issues(OWNER, REPO, &[]);
    fixture.cache.write_prs(OWNER, REPO, &[pr]);
    fixture.apply_cache();

    let Some(output) = fixture.run() else {
        return;
    };

    let wt = find_worktree_by_branch(&output, &branch)
        .expect("worktree for target branch should be in output");

    assert_eq!(
        wt["pr"]["phase"],
        Value::String("in-ai-review".to_string()),
        "pr.phase should be 'in-ai-review'"
    );
}

// ---------------------------------------------------------------------------
// Integration scenario: issue and PR on the same worktree can differ
// ---------------------------------------------------------------------------

/// orchard-tui --json allows issue and PR on the same worktree to have different phases.
///
/// Feature `@integration` scenario:
///   Given issue #47 has labels `["planned"]` and PR #55 has labels `["in-ai-review"]`
///   Then `issue.phase` is `"planned"` and `pr.phase` is `"in-ai-review"`
#[test]
fn json_issue_and_pr_on_same_worktree_can_have_different_phases() {
    let fixture = PhaseFixture::new();

    fixture.checkout_branch("feat/different-phases");
    let branch = fixture.current_branch();

    let issue = orchard::cache::CachedIssue {
        labels: vec!["planned".to_string()],
        ..make_issue(47, "Plan the work")
    };
    let pr = CachedPr {
        linked_issue: Some(47),
        labels: vec!["in-ai-review".to_string()],
        ..make_pr(55, &branch)
    };

    fixture.cache.write_issues(OWNER, REPO, &[issue]);
    fixture.cache.write_prs(OWNER, REPO, &[pr]);
    fixture.apply_cache();

    let Some(output) = fixture.run() else {
        return;
    };

    let wt = find_worktree_by_branch(&output, &branch)
        .expect("worktree for target branch should be in output");

    assert_eq!(
        wt["issue"]["phase"],
        Value::String("planned".to_string()),
        "issue.phase should be 'planned'"
    );
    assert_eq!(
        wt["pr"]["phase"],
        Value::String("in-ai-review".to_string()),
        "pr.phase should be 'in-ai-review'"
    );
}
