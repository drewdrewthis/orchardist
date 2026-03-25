#![allow(dead_code)]

/// Shared test harness for integration tests.
///
/// Provides:
/// - `TestCacheDir` — a temp dir with helpers to write cache fixture files.
/// - `TestRepo` — a temp dir containing an initialised git repository.
/// - Fixture builder functions for all cache entry types.
use orchard::cache::{
    cache_path, tmux_cache_path, write_cache, CachedIssue, CachedPr, CachedTmuxSession,
    CachedWorktree,
};
use std::path::PathBuf;
use tempfile::TempDir;

// ---------------------------------------------------------------------------
// TestCacheDir
// ---------------------------------------------------------------------------

/// A temporary cache directory with write helpers for test fixtures.
pub struct TestCacheDir {
    pub dir: TempDir,
}

impl TestCacheDir {
    /// Creates a new temporary directory to use as the cache root.
    pub fn new() -> Self {
        Self {
            dir: TempDir::new().expect("create temp cache dir"),
        }
    }

    /// Returns the root path of the temp cache directory.
    pub fn path(&self) -> &std::path::Path {
        self.dir.path()
    }

    /// Writes issues cache for `owner/repo` using the standard naming convention
    /// under this dir.
    pub fn write_issues(&self, owner: &str, repo: &str, entries: &[CachedIssue]) {
        let path = self.repo_cache_path(owner, repo, "issues");
        write_cache(&path, entries).expect("write issues cache");
    }

    /// Writes PRs cache for `owner/repo`.
    pub fn write_prs(&self, owner: &str, repo: &str, entries: &[CachedPr]) {
        let path = self.repo_cache_path(owner, repo, "prs");
        write_cache(&path, entries).expect("write prs cache");
    }

    /// Writes worktrees cache for `owner/repo`.
    pub fn write_worktrees(&self, owner: &str, repo: &str, entries: &[CachedWorktree]) {
        let path = self.repo_cache_path(owner, repo, "worktrees");
        write_cache(&path, entries).expect("write worktrees cache");
    }

    /// Writes tmux sessions cache (local, no host).
    pub fn write_tmux_sessions(&self, entries: &[CachedTmuxSession]) {
        let global = tmux_cache_path(None);
        let filename = global
            .file_name()
            .expect("tmux cache path has filename");
        let path = self.dir.path().join(filename);
        write_cache(&path, entries).expect("write tmux sessions cache");
    }

    /// Writes a raw config JSON file as `config.json` in the temp dir.
    pub fn write_config(&self, config_json: &str) {
        let path = self.dir.path().join("config.json");
        std::fs::write(&path, config_json).expect("write config.json");
    }

    /// Constructs a per-repo cache path rooted at this dir (mirrors the
    /// production naming convention: `{owner}_{repo}_{source}.json`).
    pub fn repo_cache_path(&self, owner: &str, repo: &str, source: &str) -> PathBuf {
        // Production `cache_path` roots under `~/.cache/orchard/` — we just
        // want the filename so we can place it in our temp dir instead.
        let global = cache_path(owner, repo, source);
        let filename = global.file_name().expect("cache path has filename");
        self.dir.path().join(filename)
    }
}

// ---------------------------------------------------------------------------
// TestRepo — minimal git repo in a temp dir
// ---------------------------------------------------------------------------

/// A temporary directory containing an initialised git repository.
pub struct TestRepo {
    pub dir: TempDir,
}

impl TestRepo {
    /// Creates a new temp dir and runs `git init` inside it.
    pub fn new() -> Self {
        let dir = TempDir::new().expect("create temp repo dir");
        std::process::Command::new("git")
            .arg("init")
            .current_dir(dir.path())
            .output()
            .expect("git init");
        // Configure a dummy identity so commits don't fail later.
        std::process::Command::new("git")
            .args(["config", "user.email", "test@example.com"])
            .current_dir(dir.path())
            .output()
            .expect("git config email");
        std::process::Command::new("git")
            .args(["config", "user.name", "Test"])
            .current_dir(dir.path())
            .output()
            .expect("git config name");
        Self { dir }
    }

    /// Returns the repo root path.
    pub fn path(&self) -> &std::path::Path {
        self.dir.path()
    }
}

// ---------------------------------------------------------------------------
// Fixture builders (mirrors the unit-test helpers in cache.rs / derive.rs)
// ---------------------------------------------------------------------------

pub fn make_issue(number: u32, title: &str) -> CachedIssue {
    CachedIssue {
        number,
        title: title.to_string(),
        state: "open".to_string(),
        labels: vec![],
    }
}

pub fn make_pr(number: u32, branch: &str) -> CachedPr {
    CachedPr {
        number,
        branch: branch.to_string(),
        linked_issue: None,
        state: "open".to_string(),
        review_decision: None,
        checks_state: None,
        has_conflicts: false,
        unresolved_threads: 0,
    }
}

pub fn make_approved_pr(number: u32, branch: &str) -> CachedPr {
    CachedPr {
        review_decision: Some("approved".to_string()),
        checks_state: Some("passing".to_string()),
        ..make_pr(number, branch)
    }
}

pub fn make_changes_requested_pr(number: u32, branch: &str) -> CachedPr {
    CachedPr {
        review_decision: Some("changes_requested".to_string()),
        ..make_pr(number, branch)
    }
}

pub fn make_worktree(path: &str, branch: &str) -> CachedWorktree {
    CachedWorktree {
        path: path.to_string(),
        branch: branch.to_string(),
        is_bare: false,
        is_locked: false,
        host: None,
    }
}

pub fn make_session(name: &str, path: &str, pane_commands: Vec<&str>) -> CachedTmuxSession {
    CachedTmuxSession {
        name: name.to_string(),
        path: path.to_string(),
        pane_titles: vec![],
        pane_commands: pane_commands.into_iter().map(|s| s.to_string()).collect(),
        host: None,
        last_output_lines: vec![],
    }
}

pub fn make_claude_session(name: &str, path: &str) -> CachedTmuxSession {
    CachedTmuxSession {
        // Simulate a Claude Code working indicator so `claude_is_working` is derived.
        last_output_lines: vec!["✢ Thinking... (1m 5s · ↑ 2.3k tokens)".to_string()],
        ..make_session(name, path, vec!["claude"])
    }
}
