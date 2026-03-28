/// Integration tests for the cache module (`orchard::cache`).
///
/// These tests exercise the cache read/write API against real temporary
/// directories, covering the acceptance criteria from `cache-architecture.feature`.
mod common;

use common::{TestCacheDir, make_issue, make_pr, make_session, make_worktree};
use orchard::cache::{CachedIssue, CachedPr, read_cache, write_cache, write_cache_if_nonempty};

// ---------------------------------------------------------------------------
// Independence: different source types are independent
// ---------------------------------------------------------------------------

/// Writing issues and PRs to separate cache files does not cause interference.
/// Reading one does not corrupt or affect the other.
#[test]
fn background_refresh_sources_are_independent() {
    let cache = TestCacheDir::new();

    let issue = make_issue(1, "First issue");
    let pr = make_pr(10, "feat/x");

    cache.write_issues("owner", "repo", std::slice::from_ref(&issue));
    cache.write_prs("owner", "repo", std::slice::from_ref(&pr));

    let issues_path = cache.repo_cache_path("owner", "repo", "issues");
    let prs_path = cache.repo_cache_path("owner", "repo", "prs");

    let loaded_issues = read_cache::<CachedIssue>(&issues_path);
    let loaded_prs = read_cache::<CachedPr>(&prs_path);

    assert_eq!(loaded_issues.entries.len(), 1);
    assert_eq!(loaded_issues.entries[0].number, 1);
    assert_eq!(loaded_prs.entries.len(), 1);
    assert_eq!(loaded_prs.entries[0].number, 10);
}

// ---------------------------------------------------------------------------
// Per-repo isolation: different repos write to independent files
// ---------------------------------------------------------------------------

/// Two repos each write to their own cache files. Reading one does not return
/// data from the other.
#[test]
fn each_repo_writes_to_own_cache_files() {
    let cache = TestCacheDir::new();

    cache.write_issues("owner1", "repo1", &[make_issue(1, "Repo1 issue")]);
    cache.write_issues("owner2", "repo2", &[make_issue(2, "Repo2 issue")]);

    let path1 = cache.repo_cache_path("owner1", "repo1", "issues");
    let path2 = cache.repo_cache_path("owner2", "repo2", "issues");

    // Files exist at separate paths.
    assert!(path1.exists());
    assert!(path2.exists());
    assert_ne!(path1, path2);

    let issues1 = read_cache::<CachedIssue>(&path1);
    let issues2 = read_cache::<CachedIssue>(&path2);

    assert_eq!(issues1.entries[0].number, 1);
    assert_eq!(issues2.entries[0].number, 2);
}

// ---------------------------------------------------------------------------
// write_cache_if_nonempty: empty input does not overwrite existing data
// ---------------------------------------------------------------------------

/// When `write_cache_if_nonempty` is called with an empty slice and the cache
/// file already exists, the existing data must be preserved.
#[test]
fn failed_api_call_does_not_overwrite_cache() {
    let cache = TestCacheDir::new();
    let path = cache.repo_cache_path("owner", "repo", "issues");

    // Prime the cache with real data.
    cache.write_issues("owner", "repo", &[make_issue(42, "Existing")]);
    let mtime_before = std::fs::metadata(&path).unwrap().modified().unwrap();

    // Simulate a failed API call returning no entries.
    write_cache_if_nonempty::<CachedIssue>(&path, &[]).unwrap();

    let mtime_after = std::fs::metadata(&path).unwrap().modified().unwrap();
    assert_eq!(mtime_before, mtime_after, "file must not be touched");

    let loaded = read_cache::<CachedIssue>(&path);
    assert_eq!(loaded.entries.len(), 1);
    assert_eq!(loaded.entries[0].title, "Existing");
}

// ---------------------------------------------------------------------------
// last_refreshed timestamp is written
// ---------------------------------------------------------------------------

/// A freshly written cache file contains a `last_refreshed` field that is
/// close to the current time.
#[test]
fn cache_file_includes_last_refreshed_timestamp() {
    let cache = TestCacheDir::new();
    let path = cache.repo_cache_path("owner", "repo", "issues");

    let before = chrono::Utc::now();
    cache.write_issues("owner", "repo", &[make_issue(1, "t")]);
    let after = chrono::Utc::now();

    let loaded = read_cache::<CachedIssue>(&path);
    assert!(
        loaded.last_refreshed >= before && loaded.last_refreshed <= after,
        "last_refreshed {:?} should be between {:?} and {:?}",
        loaded.last_refreshed,
        before,
        after
    );
}

// ---------------------------------------------------------------------------
// Reading missing files returns empty data
// ---------------------------------------------------------------------------

/// Reading a non-existent issues cache returns an empty entry list.
#[test]
fn reading_missing_cache_returns_empty_data() {
    let cache = TestCacheDir::new();
    let path = cache.repo_cache_path("nobody", "norepo", "issues");

    // File has never been written.
    assert!(!path.exists());

    let loaded = read_cache::<CachedIssue>(&path);
    assert!(loaded.entries.is_empty());
}

/// Reading a non-existent tmux sessions cache returns an empty entry list.
#[test]
fn reading_missing_tmux_sessions_cache_returns_empty_data() {
    let cache = TestCacheDir::new();
    let path = cache.dir.path().join("tmux_sessions.json");

    assert!(!path.exists());

    let loaded = read_cache::<orchard::cache::CachedTmuxSession>(&path);
    assert!(loaded.entries.is_empty());
}

// ---------------------------------------------------------------------------
// Atomic write: no .tmp file remains after write
// ---------------------------------------------------------------------------

/// After `write_cache` completes there must be no `*.json.tmp` file alongside
/// the final cache file.
#[test]
fn cache_writes_are_atomic() {
    let cache = TestCacheDir::new();
    let path = cache.repo_cache_path("owner", "repo", "issues");
    let tmp_path = path.with_extension("json.tmp");

    assert!(!path.exists());
    assert!(!tmp_path.exists());

    write_cache(&path, &[make_issue(1, "t")]).unwrap();

    assert!(path.exists(), "final cache file should exist");
    assert!(!tmp_path.exists(), ".tmp file should be cleaned up");
}

// ---------------------------------------------------------------------------
// Each source reads and writes independently
// ---------------------------------------------------------------------------

/// Updating the PRs cache for a repo must not alter the issues cache file.
#[test]
fn each_source_cache_read_and_written_independently() {
    let cache = TestCacheDir::new();

    // Write both.
    cache.write_issues("owner", "repo", &[make_issue(1, "Issue")]);
    cache.write_prs("owner", "repo", &[make_pr(10, "feat/branch")]);

    let issues_path = cache.repo_cache_path("owner", "repo", "issues");
    let prs_path = cache.repo_cache_path("owner", "repo", "prs");

    let issues_mtime = std::fs::metadata(&issues_path).unwrap().modified().unwrap();

    // Update only PRs.
    write_cache(
        &prs_path,
        &[make_pr(10, "feat/branch"), make_pr(11, "fix/bug")],
    )
    .unwrap();

    let issues_mtime_after = std::fs::metadata(&issues_path).unwrap().modified().unwrap();
    assert_eq!(
        issues_mtime, issues_mtime_after,
        "issues file must not be touched when only PRs are updated"
    );

    let loaded_prs = read_cache::<CachedPr>(&prs_path);
    assert_eq!(loaded_prs.entries.len(), 2);
}

// ---------------------------------------------------------------------------
// Worktrees and tmux sessions also round-trip correctly
// ---------------------------------------------------------------------------

/// Worktrees cache round-trips correctly through a real temp file.
#[test]
fn worktrees_cache_round_trips_via_file() {
    let cache = TestCacheDir::new();
    let wt = make_worktree("/workspace/repo-feat", "feat/new-thing");
    cache.write_worktrees("owner", "repo", std::slice::from_ref(&wt));

    let path = cache.repo_cache_path("owner", "repo", "worktrees");
    let loaded = read_cache::<orchard::cache::CachedWorktree>(&path);

    assert_eq!(loaded.entries.len(), 1);
    assert_eq!(loaded.entries[0].path, wt.path);
    assert_eq!(loaded.entries[0].branch, wt.branch);
}

/// Tmux sessions cache round-trips correctly through a real temp file.
#[test]
fn tmux_sessions_cache_round_trips_via_file() {
    let cache = TestCacheDir::new();
    let session = make_session("my-session", "/workspace/repo", vec!["bash"]);
    cache.write_tmux_sessions(std::slice::from_ref(&session));

    let path = cache.dir.path().join("tmux_sessions.json");
    let loaded = read_cache::<orchard::cache::CachedTmuxSession>(&path);

    assert_eq!(loaded.entries.len(), 1);
    assert_eq!(loaded.entries[0].name, session.name);
    assert_eq!(loaded.entries[0].path, session.path);
}
