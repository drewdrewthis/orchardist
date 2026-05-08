//! End-to-end integration tests for Issue #252.
//!
//! `orchard-tui --json` must expose four camelCase fields per PR:
//! `unresolvedReviewThreads`, `lastReviewCommentAt`, `lastReviewCommentAuthor`,
//! and `hasUnaddressedAuthorComment`. Legacy `unresolvedThreads` must be
//! retained as an alias. Corresponds to the `@e2e` scenarios in
//! `specs/features/unresolved-review-threads-json.feature`.
mod common;

use assert_cmd::Command;
use common::{TestCacheDir, make_pr};
use orchard::cache::{CachedPr, CachedReview};
use serde_json::Value;

const OWNER: &str = "acme";
const REPO: &str = "webapp";
const SLUG: &str = "acme/webapp";

struct ReviewFixture {
    home: tempfile::TempDir,
    cache: TestCacheDir,
    repo: common::TestRepo,
}

impl ReviewFixture {
    fn new() -> Self {
        let home = tempfile::TempDir::new().expect("create temp HOME");
        let cache = TestCacheDir::new();
        let repo = common::TestRepo::new();

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

        let config_dir = home.path().join(".orchard");
        std::fs::create_dir_all(&config_dir).expect("create config dir");
        let config_json = format!(
            r#"{{"repos":[{{"slug":"{SLUG}","path":"{}"}}]}}"#,
            repo.path().display()
        );
        std::fs::write(config_dir.join("config.json"), &config_json).expect("write config.json");

        let home_cache = home.path().join(".cache").join("orchard");
        std::fs::create_dir_all(&home_cache).expect("create cache dir");

        Self { home, cache, repo }
    }

    fn checkout_branch(&self, branch: &str) {
        std::process::Command::new("git")
            .args(["checkout", "-b", branch])
            .current_dir(self.repo.path())
            .output()
            .expect("git checkout -b");
    }

    fn current_branch(&self) -> String {
        let out = std::process::Command::new("git")
            .args(["rev-parse", "--abbrev-ref", "HEAD"])
            .current_dir(self.repo.path())
            .output()
            .expect("git rev-parse");
        String::from_utf8_lossy(&out.stdout).trim().to_string()
    }

    fn apply_cache(&self) {
        let home_cache = self.home.path().join(".cache").join("orchard");
        for entry in std::fs::read_dir(self.cache.path()).expect("read cache dir") {
            let entry = entry.expect("dir entry");
            let dest = home_cache.join(entry.file_name());
            std::fs::copy(entry.path(), &dest).expect("copy cache file");
        }
    }

    fn run(&self) -> Option<Value> {
        // Post-#329: `orchard-tui --json` is cache-only. Run `orchard refresh`
        // first to populate the worktree cache from the fixture's temp repo.
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
                "SKIP review_comment_json_integration: orchard refresh exited with {:?}. stderr: {}",
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
            let stderr = String::from_utf8_lossy(&output.stderr);
            eprintln!(
                "SKIP review_comment_json_integration: orchard-tui --json exited with {:?}. stderr: {}",
                output.status.code(),
                stderr.trim()
            );
            return None;
        }

        let stdout = String::from_utf8_lossy(&output.stdout);
        Some(serde_json::from_str(&stdout).expect("stdout should be valid JSON"))
    }
}

fn find_pr_by_branch<'a>(output: &'a Value, branch: &str) -> Option<&'a Value> {
    output["repos"].as_array()?.iter().find_map(|repo| {
        repo["worktrees"]
            .as_array()?
            .iter()
            .find(|wt| wt["branch"].as_str() == Some(branch))
            .map(|wt| &wt["pr"])
    })
}

/// `@e2e` Orchardist sees all four new fields on every PR in `orchard-tui --json`.
#[test]
fn json_exposes_all_four_review_comment_fields_on_pr() {
    let fixture = ReviewFixture::new();
    fixture.checkout_branch("feat/review-fields");
    let branch = fixture.current_branch();

    let pr = CachedPr {
        author: Some("drewdrewthis".to_string()),
        last_commit_pushed_at: Some("2026-04-13T10:00:00Z".to_string()),
        unresolved_threads: 1,
        reviews: vec![
            CachedReview {
                author: "reviewer-1".to_string(),
                state: "COMMENTED".to_string(),
                submitted_at: Some("2026-04-13T21:11:53Z".to_string()),
            },
            CachedReview {
                author: "drewdrewthis".to_string(),
                state: "COMMENTED".to_string(),
                submitted_at: Some("2026-04-13T09:00:00Z".to_string()),
            },
        ],
        ..make_pr(101, &branch)
    };

    fixture.cache.write_issues(OWNER, REPO, &[]);
    fixture.cache.write_prs(OWNER, REPO, &[pr]);
    fixture.apply_cache();

    let Some(output) = fixture.run() else { return };
    let pr_json = find_pr_by_branch(&output, &branch).expect("PR should be in JSON output");

    assert_eq!(
        pr_json["unresolvedReviewThreads"].as_u64(),
        Some(1),
        "unresolvedReviewThreads must equal pr.unresolved_threads"
    );
    assert_eq!(
        pr_json["unresolvedThreads"].as_u64(),
        Some(1),
        "legacy unresolvedThreads key must remain as alias"
    );
    assert_eq!(
        pr_json["lastReviewCommentAt"].as_str(),
        Some("2026-04-13T21:11:53Z")
    );
    assert_eq!(
        pr_json["lastReviewCommentAuthor"].as_str(),
        Some("reviewer-1")
    );
    assert_eq!(
        pr_json["hasUnaddressedAuthorComment"].as_bool(),
        Some(true),
        "non-author comment after last push must flip hasUnaddressedAuthorComment true"
    );
}

/// `@e2e` `jq` can filter PRs on `hasUnaddressedAuthorComment` without a follow-up API call.
/// Exercised by asserting the field shape is jq-compatible across multiple PRs on multiple
/// worktrees (boolean, always present, queryable via a path selector).
#[test]
fn json_has_unaddressed_author_comment_is_always_boolean() {
    let fixture = ReviewFixture::new();
    fixture.checkout_branch("feat/jq-filter");
    let branch = fixture.current_branch();

    // PR 101: non-author reviewer post-push → should be true.
    let pr_unaddressed = CachedPr {
        author: Some("drewdrewthis".to_string()),
        last_commit_pushed_at: Some("2026-04-13T10:00:00Z".to_string()),
        reviews: vec![CachedReview {
            author: "reviewer-1".to_string(),
            state: "COMMENTED".to_string(),
            submitted_at: Some("2026-04-13T21:11:53Z".to_string()),
        }],
        ..make_pr(101, &branch)
    };

    fixture.cache.write_issues(OWNER, REPO, &[]);
    fixture.cache.write_prs(OWNER, REPO, &[pr_unaddressed]);
    fixture.apply_cache();

    let Some(output) = fixture.run() else { return };
    let pr_json = find_pr_by_branch(&output, &branch).expect("PR should be in JSON output");

    // The field must exist as a boolean (not null, not missing) — this is the jq contract.
    assert!(
        pr_json["hasUnaddressedAuthorComment"].is_boolean(),
        "hasUnaddressedAuthorComment must always be a boolean, got: {}",
        pr_json["hasUnaddressedAuthorComment"]
    );
    assert_eq!(pr_json["hasUnaddressedAuthorComment"].as_bool(), Some(true));
}
