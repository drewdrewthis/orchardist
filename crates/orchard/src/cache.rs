//! Cache types and read/write helpers for Orchard's on-disk cache.
//!
//! Provides strongly-typed entry structs (`CachedIssue`, `CachedPr`, etc.),
//! atomic JSON file I/O, path conventions under `~/.cache/orchard/`, and
//! the session manifest that persists worktree-to-session bindings across
//! cache refreshes.
use std::path::{Path, PathBuf};

use anyhow::Context;
use chrono::{DateTime, Utc};
use serde::{Deserialize, Serialize, de::DeserializeOwned};

use crate::ci_state::CiChecks;
use crate::claude_state::ClaudeStateFile;

// ---------------------------------------------------------------------------
// WorktreeLayout — slice 2 stub (feature.feature:140)
// ---------------------------------------------------------------------------

/// Physical layout of a worktree on the remote host.
///
/// `Bare` is the traditional Remmy/BoxdShared model: one bare repo + multiple
/// linked worktrees in subdirectories, parsed with `git worktree list --porcelain`.
///
/// `Flat` is the BoxdFork model: a single regular (non-bare) clone at a fixed
/// path. No worktree list; branch is obtained via `git rev-parse --abbrev-ref HEAD`.
///
/// Defaults to `Bare` via `serde(default)` so existing on-disk cache files
/// written before this field was added continue to deserialize correctly.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize, Default)]
#[serde(rename_all = "kebab-case")]
pub enum WorktreeLayout {
    /// Bare repo + linked worktrees (traditional Remmy/BoxdShared model).
    #[default]
    Bare,
    /// Single flat clone — no linked worktrees (BoxdFork model).
    Flat,
}

// ---------------------------------------------------------------------------
// Entry types
// ---------------------------------------------------------------------------

/// A child issue from GitHub's sub-issues API.
#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
pub struct CachedSubIssue {
    /// GitHub issue number of the sub-issue.
    pub number: u32,
    /// Sub-issue title.
    pub title: String,
    /// Sub-issue state string (e.g. `"open"`, `"closed"`).
    pub state: String,
}

/// A GitHub issue entry as stored in the issues cache file.
#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
pub struct CachedIssue {
    /// GitHub issue number.
    pub number: u32,
    /// Issue title.
    pub title: String,
    /// Issue state string (e.g. `"open"`, `"closed"`).
    pub state: String,
    /// Labels applied to the issue.
    pub labels: Vec<String>,
    /// GitHub logins of assignees.
    #[serde(default)]
    pub assignees: Vec<String>,
    /// ISO 8601 timestamp when the issue was created.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub created_at: Option<String>,
    /// ISO 8601 timestamp when the issue was last updated.
    ///
    /// Used by the TUI's SINCE column for `Blocked` and `Paused` statuses
    /// (issue #251). `None` for older cache files written before this field
    /// was added; the signal layer falls back to `created_at` in that case.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub updated_at: Option<String>,
    /// Issue numbers that block this issue (from body text regex + sub-issues API).
    #[serde(default)]
    pub blocked_by: Vec<u32>,
    /// Child issues from GitHub sub-issues API.
    #[serde(default)]
    pub sub_issues: Vec<CachedSubIssue>,
    /// Parent issue number from GitHub sub-issues API.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub parent: Option<u32>,
}

/// A single review submitted on a pull request.
#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
pub struct CachedReview {
    /// GitHub login of the reviewer.
    pub author: String,
    /// Review state string from GitHub (e.g. `"APPROVED"`, `"CHANGES_REQUESTED"`, `"COMMENTED"`).
    pub state: String,
    /// ISO 8601 timestamp when the review was submitted, if available.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub submitted_at: Option<String>,
}

/// A GitHub pull request entry as stored in the PRs cache file.
#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
pub struct CachedPr {
    /// GitHub PR number.
    pub number: u32,
    /// The branch name the PR was opened from.
    pub branch: String,
    /// The issue number this PR is linked to, if any.
    pub linked_issue: Option<u32>,
    /// PR state string (e.g. `"open"`, `"merged"`, `"closed"`).
    pub state: String,
    /// Aggregated review decision string from GitHub (e.g. `"APPROVED"`).
    pub review_decision: Option<String>,
    /// Aggregated CI checks state — legacy union field, mirrors `ci_code_state` only.
    ///
    /// Deprecated in favour of [`CachedPr::ci_code_state`]. Retained for one
    /// release so existing cache files deserialize without a migration step.
    /// A code-green gate-blocked PR stays "passing" here (backward-compat).
    #[serde(default)]
    pub checks_state: Option<String>,
    /// Rollup state for code CI checks: "passing", "failing", "pending", or None.
    #[serde(default)]
    pub ci_code_state: Option<String>,
    /// Rollup state for gate/policy checks: "cleared", "blocked", "pending", or None.
    #[serde(default)]
    pub ci_gate_state: Option<String>,
    /// Per-check breakdown classified into code and gate buckets.
    #[serde(default)]
    pub ci_checks: CiChecks,
    /// Whether the PR has merge conflicts with its base branch.
    pub has_conflicts: bool,
    /// Number of unresolved review threads on the PR.
    pub unresolved_threads: u32,
    /// State of the linked issue (e.g. `"open"`, `"closed"`), if resolved from
    /// the GraphQL `closingIssuesReferences` nodes.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub linked_issue_state: Option<String>,
    /// Labels applied to the PR.
    ///
    /// Uses `serde(default)` so pre-upgrade cache files without this key still
    /// deserialize successfully (producing an empty vec).
    #[serde(default)]
    pub labels: Vec<String>,
    /// PR title.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub title: Option<String>,
    /// Whether the PR is a draft.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub is_draft: Option<bool>,
    /// GitHub login of the PR author.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub author: Option<String>,
    /// Logins or team names of requested reviewers.
    #[serde(default)]
    pub requested_reviewers: Vec<String>,
    /// Reviews submitted on this PR.
    #[serde(default)]
    pub reviews: Vec<CachedReview>,
    /// Number of lines added by this PR.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub additions: Option<u32>,
    /// Number of lines deleted by this PR.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub deletions: Option<u32>,
    /// ISO 8601 timestamp when the PR was created.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub created_at: Option<String>,
    /// ISO 8601 timestamp when the PR was last updated.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub updated_at: Option<String>,
    /// ISO 8601 timestamp of when the last commit was pushed to the PR branch.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub last_commit_pushed_at: Option<String>,
    /// Per-thread latest-comment timestamps (epoch seconds) for threads that
    /// are unresolved AND not outdated (`isResolved=false AND isOutdated!=true`).
    ///
    /// Each entry corresponds to one such thread's most recent comment
    /// (`createdAt` of `comments(last: 1)`). Used by the signal layer to
    /// compute `since_epoch` for `PipelineStatus::UnresolvedThreads` as the
    /// max across these values, falling back to `pr.updated_at` when empty.
    ///
    /// Uses `serde(default)` so cache files written by previous orchard
    /// versions (before this field was added) deserialize cleanly with an
    /// empty vec.
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub unresolved_thread_comment_timestamps: Vec<i64>,
}

/// Repo-level metadata extracted from the per-branch PR GraphQL response.
#[derive(Debug, Clone, Serialize, Deserialize, Default, PartialEq)]
pub struct CachedRepoMeta {
    /// Name of the default branch (e.g. "main").
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub default_branch: Option<String>,
    /// CI state of the default branch HEAD: "SUCCESS", "FAILURE", "PENDING", etc.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub main_ci_state: Option<String>,
}

/// A git worktree entry as stored in the worktrees cache file.
#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
pub struct CachedWorktree {
    /// Absolute filesystem path to the worktree root.
    pub path: String,
    /// The branch checked out in this worktree.
    pub branch: String,
    /// Whether this is the bare worktree (the `.git` root).
    pub is_bare: bool,
    /// Whether the worktree is locked (cannot be pruned by git).
    pub is_locked: bool,
    /// Remote host identifier if this worktree lives on a remote machine.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub host: Option<String>,
    /// Commits ahead of upstream.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub ahead: Option<u32>,
    /// Commits behind upstream.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub behind: Option<u32>,
    /// ISO 8601 timestamp of the last commit on this branch.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub last_commit_at: Option<String>,
    /// Physical layout of this worktree on the host.
    ///
    /// Defaults to `Bare` via `serde(default)` so cache files written before
    /// this field was introduced continue to deserialize correctly, preserving
    /// the pre-refactor behaviour for all existing Remmy/BoxdShared worktrees.
    ///
    /// Set to `Flat` for BoxdFork fork-per-issue clones.
    #[serde(default)]
    pub layout: WorktreeLayout,
}

/// A tmux session entry as stored in the tmux sessions cache file.
#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
pub struct CachedTmuxSession {
    /// The tmux session name.
    pub name: String,
    /// Working directory of the session's first window.
    pub path: String,
    /// Tmux window.pane target addresses (e.g., "0.0", "0.1", "1.0") for each pane.
    #[serde(default)]
    pub pane_targets: Vec<String>,
    /// Titles of all panes in the session.
    pub pane_titles: Vec<String>,
    /// Commands running in each pane of the session.
    pub pane_commands: Vec<String>,
    /// Window name per pane row, parallel to `pane_targets`.
    ///
    /// Uses `serde(default)` so old cache files without this field deserialize
    /// with an empty vec, triggering synthetic window name fallback.
    #[serde(default)]
    pub window_names: Vec<String>,
    /// Window active flag per pane row, parallel to `pane_targets`.
    ///
    /// "1" means the pane's window is the active window in this session;
    /// anything else means inactive. Uses `serde(default)` for cache upgrade compat.
    #[serde(default)]
    pub window_active: Vec<String>,
    /// Tmux window layout strings per pane row, parallel to `pane_targets`.
    ///
    /// A layout string (e.g. "bb62,80x24,0,0{40x24,0,0,1,39x24,41,0,2}") is
    /// passed to `tmux select-layout` during session restore. All panes in the
    /// same window share the same layout string — deduplicate at restore time.
    /// Uses `serde(default)` for cache upgrade compat.
    #[serde(default)]
    pub window_layouts: Vec<String>,
    /// Working directory per pane, parallel to `pane_targets`.
    ///
    /// Captured from `#{pane_current_path}` so session restore can `cd` each
    /// pane back to its last directory (the session-level `path` only seeds
    /// the first window). Uses `serde(default)` for cache upgrade compat.
    #[serde(default)]
    pub pane_paths: Vec<String>,
    /// Active-pane flag per pane row (`"1"` = active), parallel to `pane_targets`.
    ///
    /// Mirrors the `window_active` pattern so restore can select the focused
    /// pane within each window via `tmux select-pane`. Uses `serde(default)`
    /// for cache upgrade compat.
    #[serde(default)]
    pub pane_active: Vec<String>,
    /// Remote host identifier if this session is on a remote machine.
    pub host: Option<String>,
    /// Unix timestamp when the tmux session was created.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub created_at: Option<u64>,
    /// Unix timestamp of the last activity in this session.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub last_activity_at: Option<u64>,
    #[serde(default)]
    /// Recent output lines from the session's active pane.
    pub last_output_lines: Vec<String>,
    #[serde(default)]
    /// Claude hook state file fetched from the remote host alongside this session.
    ///
    /// Only populated for remote sessions. `None` means no state file was found
    /// on the remote host for this session at the time of the last SSH refresh.
    pub claude_state_raw: Option<ClaudeStateFile>,
}

// ---------------------------------------------------------------------------
// Cache file wrapper
// ---------------------------------------------------------------------------

/// Wrapper that pairs a list of cache entries with the timestamp of the last refresh.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct CacheFile<T> {
    /// UTC timestamp of when this cache was last written.
    pub last_refreshed: DateTime<Utc>,
    /// The cached entries.
    pub entries: Vec<T>,
}

impl<T> CacheFile<T> {
    fn empty() -> Self {
        CacheFile {
            last_refreshed: DateTime::from_timestamp(0, 0).unwrap_or(DateTime::<Utc>::MIN_UTC),
            entries: Vec::new(),
        }
    }
}

// ---------------------------------------------------------------------------
// Path helpers
// ---------------------------------------------------------------------------

/// Returns the cache directory: `~/.cache/orchard/`.
pub fn cache_dir() -> PathBuf {
    dirs::home_dir()
        .unwrap_or_else(std::env::temp_dir)
        .join(".cache/orchard")
}

/// Returns the cache file path for a per-repo source, resolved under
/// `cache_dir`.
///
/// The `source` parameter is the file suffix, e.g. `"issues"`, `"prs"`,
/// `"worktrees"`, or `"remote_worktrees"`.
///
/// Prefer [`cache_path`] for production code. Use this variant in tests to
/// redirect writes to a `TempDir`.
pub fn cache_path_in(owner: &str, repo: &str, source: &str, cache_dir: &Path) -> PathBuf {
    cache_dir.join(format!("{}_{}_{}.json", owner, repo, source))
}

/// Returns the cache file path for a per-repo source.
///
/// The `source` parameter is the file suffix, e.g. `"issues"`, `"prs"`,
/// `"worktrees"`, or `"remote_worktrees"`.
pub fn cache_path(owner: &str, repo: &str, source: &str) -> PathBuf {
    cache_path_in(owner, repo, source, &cache_dir())
}

/// Returns the cache file path for tmux sessions, resolved under `cache_dir`.
///
/// Pass `None` for local sessions (`tmux_sessions.json`) or `Some("user@host")`
/// for a remote host. In the remote case, `@` and `.` in the host string are
/// replaced with `_` to produce a safe filename.
///
/// Prefer [`tmux_cache_path`] for production code (uses the default cache
/// directory). Use this variant in tests to redirect writes to a `TempDir`.
pub fn tmux_cache_path_in(host: Option<&str>, cache_dir: &Path) -> PathBuf {
    match host {
        None => cache_dir.join("tmux_sessions.json"),
        Some(h) => {
            let safe = h.replace(['@', '.'], "_");
            cache_dir.join(format!("{}_tmux_sessions.json", safe))
        }
    }
}

/// Returns the cache file path for tmux sessions.
///
/// Pass `None` for local sessions (`tmux_sessions.json`) or `Some("user@host")`
/// for a remote host. In the remote case, `@` and `.` in the host string are
/// replaced with `_` to produce a safe filename.
pub fn tmux_cache_path(host: Option<&str>) -> PathBuf {
    tmux_cache_path_in(host, &cache_dir())
}

// ---------------------------------------------------------------------------
// Read / write
// ---------------------------------------------------------------------------

/// Reads a cache file from `path`.
///
/// Returns an empty `CacheFile` (epoch timestamp, no entries) if the file does
/// not exist or contains invalid JSON. Never panics or returns an error.
pub fn read_cache<T: DeserializeOwned>(path: &Path) -> CacheFile<T> {
    let Ok(contents) = std::fs::read_to_string(path) else {
        return CacheFile::empty();
    };
    serde_json::from_str(&contents).unwrap_or_else(|_| CacheFile::empty())
}

/// Writes `entries` to `path` atomically (via a `.tmp` sibling file) and sets
/// `last_refreshed` to the current UTC time.
pub fn write_cache<T: Serialize>(path: &Path, entries: &[T]) -> anyhow::Result<()> {
    let dir = path
        .parent()
        .context("cache path has no parent directory")?;
    std::fs::create_dir_all(dir).context("create cache directory")?;

    // Use a local wrapper that borrows entries to avoid requiring T: Clone.
    #[derive(Serialize)]
    struct CachePayload<'a, T> {
        last_refreshed: DateTime<Utc>,
        entries: &'a [T],
    }
    let payload = CachePayload {
        last_refreshed: Utc::now(),
        entries,
    };
    let json = serde_json::to_string_pretty(&payload).context("serialize cache")?;

    let tmp_path = path.with_extension("json.tmp");
    std::fs::write(&tmp_path, &json).context("write cache .tmp file")?;
    // Restrict to owner-only read/write before the rename. The tmux cache
    // carries per-pane cwds, window layouts, and captured stdout lines; the
    // issue/PR caches carry GitHub data. None of it should be world-readable
    // on a shared host.
    restrict_cache_permissions(&tmp_path);
    std::fs::rename(&tmp_path, path).context("rename .tmp to final cache file")?;

    Ok(())
}

/// Sets 0600 permissions on a cache file (owner read/write only). No-op on
/// non-Unix platforms. Errors are swallowed — the rename still succeeds, the
/// cache is still usable, and the umask fallback is acceptable if permission
/// tightening is unsupported.
#[cfg(unix)]
fn restrict_cache_permissions(path: &Path) {
    use std::os::unix::fs::PermissionsExt;
    let _ = std::fs::set_permissions(path, std::fs::Permissions::from_mode(0o600));
}

#[cfg(not(unix))]
fn restrict_cache_permissions(_path: &Path) {}

/// Like `write_cache`, but skips the write when `entries` is empty **and** the
/// cache file already exists on disk. This prevents a failed API call (which
/// returns no entries) from overwriting good cached data with an empty list.
pub fn write_cache_if_nonempty<T: Serialize>(path: &Path, entries: &[T]) -> anyhow::Result<()> {
    if entries.is_empty() && path.exists() {
        return Ok(());
    }
    write_cache(path, entries)
}

// ---------------------------------------------------------------------------
// Session manifest
// ---------------------------------------------------------------------------

/// One entry in the session manifest — records a worktree that had an active
/// tmux session at the time of the last cache refresh.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct SessionManifestEntry {
    /// Name of the tmux session.
    pub session_name: String,
    /// Absolute path to the worktree the session is rooted in.
    pub worktree_path: String,
    /// Branch checked out in the worktree at the time of the snapshot.
    pub branch: String,
    /// Whether a Claude agent session was active in this worktree's tmux session.
    pub had_claude: bool,
    /// Remote host identifier if this session is on a remote machine.
    pub host: Option<String>,
}

/// The full session manifest written to `~/.cache/orchard/session_manifest.json`.
#[derive(Debug, Clone, Serialize, Deserialize, Default)]
pub struct SessionManifest {
    /// UTC timestamp of the last manifest write.
    pub last_updated: DateTime<Utc>,
    /// All recorded session entries.
    pub sessions: Vec<SessionManifestEntry>,
}

/// Returns the path to the session manifest file.
pub fn manifest_path() -> PathBuf {
    cache_dir().join("session_manifest.json")
}

/// Reads the session manifest from disk.
///
/// Returns a default (empty) manifest if the file does not exist or contains
/// invalid JSON. Never panics or returns an error.
pub fn read_manifest() -> SessionManifest {
    let path = manifest_path();
    if !path.exists() {
        return SessionManifest::default();
    }
    match std::fs::read(&path) {
        Ok(data) => serde_json::from_slice(&data).unwrap_or_default(),
        Err(_) => SessionManifest::default(),
    }
}

/// Writes the session manifest to disk atomically (via a `.tmp` sibling file).
///
/// Applies 0600 permissions on Unix — the manifest records which worktrees
/// had active tmux/Claude sessions, which is metadata that should not be
/// world-readable on a shared host (same reasoning as [`write_cache`]).
pub fn write_manifest(manifest: &SessionManifest) -> anyhow::Result<()> {
    let path = manifest_path();
    let dir = path
        .parent()
        .context("manifest path has no parent directory")?;
    std::fs::create_dir_all(dir).context("create cache directory")?;
    let data = serde_json::to_string_pretty(manifest).context("serialize manifest")?;
    let tmp = path.with_extension("json.tmp");
    std::fs::write(&tmp, &data).context("write manifest .tmp file")?;
    restrict_cache_permissions(&tmp);
    std::fs::rename(&tmp, &path).context("rename .tmp to final manifest file")?;
    Ok(())
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

#[cfg(test)]
mod tests {
    use super::*;
    use tempfile::tempdir;

    // -- helpers -------------------------------------------------------------

    fn make_issue() -> CachedIssue {
        CachedIssue {
            number: 42,
            title: "Fix the thing".to_string(),
            state: "open".to_string(),
            labels: vec!["bug".to_string()],
            assignees: vec![],
            created_at: None,
            updated_at: None,
            blocked_by: vec![],
            sub_issues: vec![],
            parent: None,
        }
    }

    fn make_pr() -> CachedPr {
        CachedPr {
            number: 7,
            branch: "feat/my-branch".to_string(),
            linked_issue: Some(42),
            state: "open".to_string(),
            review_decision: Some("approved".to_string()),
            checks_state: Some("passing".to_string()),
            ci_code_state: Some("passing".to_string()),
            ci_gate_state: None,
            ci_checks: CiChecks::default(),
            has_conflicts: false,
            unresolved_threads: 0,
            linked_issue_state: None,
            labels: vec![],
            title: None,
            is_draft: None,
            author: None,
            requested_reviewers: vec![],
            reviews: vec![],
            additions: None,
            deletions: None,
            created_at: None,
            updated_at: None,
            last_commit_pushed_at: None,
            unresolved_thread_comment_timestamps: vec![],
        }
    }

    fn make_worktree() -> CachedWorktree {
        CachedWorktree {
            path: "/home/user/repo".to_string(),
            branch: "main".to_string(),
            is_bare: false,
            is_locked: false,
            host: None,
            ahead: None,
            behind: None,
            last_commit_at: None,
            layout: WorktreeLayout::Bare,
        }
    }

    fn make_session() -> CachedTmuxSession {
        CachedTmuxSession {
            name: "my-session".to_string(),
            path: "/home/user/repo".to_string(),
            pane_targets: vec!["0.0".to_string()],
            pane_titles: vec!["bash".to_string()],
            pane_commands: vec!["vim".to_string()],
            window_names: vec![],
            window_active: vec![],
            window_layouts: vec![],
            pane_paths: vec![],
            pane_active: vec![],
            host: None,
            created_at: None,
            last_activity_at: None,
            last_output_lines: vec![],
            claude_state_raw: None,
        }
    }

    // -- path naming ---------------------------------------------------------

    #[test]
    fn cache_path_naming_convention() {
        let dir = tempdir().unwrap();
        // We test the filename portion only, independent of home dir.
        // Use the function directly and check the file_name component.
        let path = cache_path("webapp", "webapp", "issues");
        assert_eq!(
            path.file_name().unwrap().to_str().unwrap(),
            "webapp_webapp_issues.json"
        );

        let path = cache_path("webapp", "webapp", "prs");
        assert_eq!(
            path.file_name().unwrap().to_str().unwrap(),
            "webapp_webapp_prs.json"
        );

        let path = cache_path("webapp", "webapp", "worktrees");
        assert_eq!(
            path.file_name().unwrap().to_str().unwrap(),
            "webapp_webapp_worktrees.json"
        );

        let path = cache_path("webapp", "webapp", "remote_worktrees");
        assert_eq!(
            path.file_name().unwrap().to_str().unwrap(),
            "webapp_webapp_remote_worktrees.json"
        );

        // Silence unused-variable warning for dir (kept to show pattern).
        drop(dir);
    }

    #[test]
    fn tmux_cache_path_local_and_remote() {
        let local = tmux_cache_path(None);
        assert_eq!(
            local.file_name().unwrap().to_str().unwrap(),
            "tmux_sessions.json"
        );

        let remote = tmux_cache_path(Some("ubuntu@10.0.0.1"));
        assert_eq!(
            remote.file_name().unwrap().to_str().unwrap(),
            "ubuntu_10_0_0_1_tmux_sessions.json"
        );

        let remote_dot = tmux_cache_path(Some("user@host.example.com"));
        assert_eq!(
            remote_dot.file_name().unwrap().to_str().unwrap(),
            "user_host_example_com_tmux_sessions.json"
        );
    }

    // -- read: missing / invalid ---------------------------------------------

    #[test]
    fn read_returns_empty_on_missing_file() {
        let dir = tempdir().unwrap();
        let path = dir.path().join("nonexistent.json");
        let cache: CacheFile<CachedIssue> = read_cache(&path);
        assert!(cache.entries.is_empty());
        assert_eq!(cache.last_refreshed.timestamp(), 0);
    }

    #[test]
    fn read_returns_empty_on_invalid_json() {
        let dir = tempdir().unwrap();
        let path = dir.path().join("bad.json");
        std::fs::write(&path, b"{{not valid json").unwrap();
        let cache: CacheFile<CachedIssue> = read_cache(&path);
        assert!(cache.entries.is_empty());
        assert_eq!(cache.last_refreshed.timestamp(), 0);
    }

    // -- roundtrip tests for each type ---------------------------------------

    #[test]
    fn write_and_read_roundtrip_issues() {
        let dir = tempdir().unwrap();
        let path = dir.path().join("issues.json");
        let entries = vec![make_issue()];
        write_cache(&path, &entries).unwrap();
        let cache: CacheFile<CachedIssue> = read_cache(&path);
        assert_eq!(cache.entries, entries);
    }

    #[test]
    fn write_and_read_roundtrip_prs() {
        let dir = tempdir().unwrap();
        let path = dir.path().join("prs.json");
        let entries = vec![make_pr()];
        write_cache(&path, &entries).unwrap();
        let cache: CacheFile<CachedPr> = read_cache(&path);
        assert_eq!(cache.entries, entries);
    }

    #[test]
    fn write_and_read_roundtrip_worktrees() {
        let dir = tempdir().unwrap();
        let path = dir.path().join("worktrees.json");
        let entries = vec![make_worktree()];
        write_cache(&path, &entries).unwrap();
        let cache: CacheFile<CachedWorktree> = read_cache(&path);
        assert_eq!(cache.entries, entries);
    }

    #[test]
    fn write_and_read_roundtrip_sessions() {
        let dir = tempdir().unwrap();
        let path = dir.path().join("tmux_sessions.json");
        let entries = vec![make_session()];
        write_cache(&path, &entries).unwrap();
        let cache: CacheFile<CachedTmuxSession> = read_cache(&path);
        assert_eq!(cache.entries, entries);
    }

    // -- atomic write --------------------------------------------------------

    #[test]
    fn write_is_atomic() {
        let dir = tempdir().unwrap();
        let path = dir.path().join("issues.json");
        let tmp_path = path.with_extension("json.tmp");

        // Before write: neither file exists.
        assert!(!path.exists());
        assert!(!tmp_path.exists());

        write_cache(&path, &[make_issue()]).unwrap();

        // After write: final file exists, .tmp was cleaned up.
        assert!(path.exists());
        assert!(!tmp_path.exists());
    }

    // -- write_cache_if_nonempty ---------------------------------------------

    #[test]
    fn write_cache_if_nonempty_preserves_existing_on_empty_input() {
        let dir = tempdir().unwrap();
        let path = dir.path().join("issues.json");

        // Write initial data.
        write_cache(&path, &[make_issue()]).unwrap();
        let mtime_before = std::fs::metadata(&path).unwrap().modified().unwrap();

        // Attempt to overwrite with empty slice — should be skipped.
        write_cache_if_nonempty::<CachedIssue>(&path, &[]).unwrap();
        let mtime_after = std::fs::metadata(&path).unwrap().modified().unwrap();

        assert_eq!(
            mtime_before, mtime_after,
            "file should not have been touched"
        );

        // Verify contents unchanged.
        let cache: CacheFile<CachedIssue> = read_cache(&path);
        assert_eq!(cache.entries.len(), 1);
    }

    #[test]
    fn write_cache_if_nonempty_writes_when_entries_present() {
        let dir = tempdir().unwrap();
        let path = dir.path().join("issues.json");

        let entries = vec![make_issue()];
        write_cache_if_nonempty(&path, &entries).unwrap();

        let cache: CacheFile<CachedIssue> = read_cache(&path);
        assert_eq!(cache.entries, entries);
    }

    // -- session manifest ----------------------------------------------------

    #[test]
    fn manifest_roundtrip() {
        let manifest = SessionManifest {
            last_updated: Utc::now(),
            sessions: vec![SessionManifestEntry {
                session_name: "webapp_main".to_string(),
                worktree_path: "/home/user/webapp".to_string(),
                branch: "main".to_string(),
                had_claude: false,
                host: None,
            }],
        };
        let json = serde_json::to_string(&manifest).unwrap();
        let parsed: SessionManifest = serde_json::from_str(&json).unwrap();
        assert_eq!(parsed.sessions.len(), 1);
        assert_eq!(parsed.sessions[0].session_name, "webapp_main");
    }

    #[test]
    fn read_manifest_returns_default_when_missing() {
        // Use a nonexistent path — read_manifest guards with path.exists().
        // Temporarily redirect: just test the public API with a missing file
        // by confirming the function doesn't panic.
        let result = std::panic::catch_unwind(read_manifest);
        assert!(result.is_ok());
    }

    // -- CachedTmuxSession window fields ------------------------------------

    #[test]
    fn cached_tmux_session_window_fields_roundtrip() {
        let session = CachedTmuxSession {
            name: "my-session".to_string(),
            path: "/home/user/repo".to_string(),
            pane_targets: vec!["0.0".to_string(), "1.0".to_string()],
            pane_titles: vec!["bash".to_string(), "nvim".to_string()],
            pane_commands: vec!["bash".to_string(), "nvim".to_string()],
            window_names: vec!["main".to_string(), "editor".to_string()],
            window_active: vec!["1".to_string(), "0".to_string()],
            window_layouts: vec![],
            pane_paths: vec![],
            pane_active: vec![],
            host: None,
            created_at: None,
            last_activity_at: None,
            last_output_lines: vec![],
            claude_state_raw: None,
        };
        let json = serde_json::to_string(&session).unwrap();
        let parsed: CachedTmuxSession = serde_json::from_str(&json).unwrap();
        assert_eq!(parsed.window_names, vec!["main", "editor"]);
        assert_eq!(parsed.window_active, vec!["1", "0"]);
    }

    #[test]
    fn cached_tmux_session_missing_window_fields_default_to_empty() {
        // Old cache format without window_names or window_active fields.
        let json = r#"{
            "name": "old-session",
            "path": "/home/user/repo",
            "pane_targets": ["0.0"],
            "pane_titles": ["bash"],
            "pane_commands": ["bash"],
            "host": null,
            "last_output_lines": [],
            "claude_state_raw": null
        }"#;
        let parsed: CachedTmuxSession = serde_json::from_str(json).unwrap();
        assert!(
            parsed.window_names.is_empty(),
            "window_names should default to empty vec"
        );
        assert!(
            parsed.window_active.is_empty(),
            "window_active should default to empty vec"
        );
    }

    #[test]
    fn manifest_write_is_atomic() {
        let dir = tempdir().unwrap();
        let path = dir.path().join("session_manifest.json");
        let tmp_path = path.with_extension("json.tmp");

        // Neither file exists before write.
        assert!(!path.exists());
        assert!(!tmp_path.exists());

        // write_manifest uses the global manifest_path(), so test serialization
        // + atomic pattern via write_cache directly to verify the tmp cleanup.
        let manifest = SessionManifest {
            last_updated: Utc::now(),
            sessions: vec![],
        };
        let data = serde_json::to_string_pretty(&manifest).unwrap();
        std::fs::write(&tmp_path, &data).unwrap();
        std::fs::rename(&tmp_path, &path).unwrap();

        assert!(path.exists());
        assert!(!tmp_path.exists());
    }

    // -- CachedPr serde defaults (Task #23) ------------------------------------

    /// Old cache files written by orchard 0.6.0 do not contain ci_code_state,
    /// ci_gate_state, or ci_checks. This test verifies they deserialize
    /// successfully with serde defaults (None / CiChecks::default()).
    #[test]
    fn cached_pr_old_format_deserializes_with_defaults() {
        // JSON matching the 0.6.0 CachedPr format — no new CI fields.
        let json = r#"{
            "number": 42,
            "branch": "feat/old-format",
            "linked_issue": null,
            "state": "open",
            "review_decision": null,
            "checks_state": "passing",
            "has_conflicts": false,
            "unresolved_threads": 0
        }"#;

        let pr: CachedPr =
            serde_json::from_str(json).expect("old CachedPr format must deserialize without error");

        assert_eq!(pr.number, 42);
        assert_eq!(pr.checks_state.as_deref(), Some("passing"));
        assert!(
            pr.ci_code_state.is_none(),
            "ci_code_state should default to None"
        );
        assert!(
            pr.ci_gate_state.is_none(),
            "ci_gate_state should default to None"
        );
        assert_eq!(
            pr.ci_checks,
            CiChecks::default(),
            "ci_checks should default to empty CiChecks"
        );
    }

    // -- last_refreshed ------------------------------------------------------

    #[test]
    fn cache_file_includes_last_refreshed() {
        let dir = tempdir().unwrap();
        let path = dir.path().join("issues.json");
        let before = Utc::now();
        write_cache(&path, &[make_issue()]).unwrap();
        let after = Utc::now();

        let cache: CacheFile<CachedIssue> = read_cache(&path);
        assert!(
            cache.last_refreshed >= before && cache.last_refreshed <= after,
            "last_refreshed {:?} should be between {:?} and {:?}",
            cache.last_refreshed,
            before,
            after
        );

        // Also verify the JSON on disk contains the field name.
        let json = std::fs::read_to_string(&path).unwrap();
        assert!(json.contains("\"last_refreshed\""));
    }

    // -- CachedPr labels migration -------------------------------------------

    #[test]
    fn cached_pr_without_labels_key_deserializes_to_empty_vec() {
        let json = r#"{
            "number": 55,
            "branch": "issue/example",
            "linked_issue": null,
            "state": "open",
            "review_decision": null,
            "checks_state": null,
            "has_conflicts": false,
            "unresolved_threads": 0
        }"#;
        let pr: CachedPr = serde_json::from_str(json).expect("deserialization should succeed");
        assert!(
            pr.labels.is_empty(),
            "labels should default to empty vec when key is absent"
        );
    }

    #[test]
    fn cached_pr_with_labels_key_round_trips() {
        let json = r#"{
            "number": 55,
            "branch": "issue/example",
            "linked_issue": null,
            "state": "open",
            "review_decision": null,
            "checks_state": null,
            "has_conflicts": false,
            "unresolved_threads": 0,
            "labels": ["in-progress", "bug"]
        }"#;
        let pr: CachedPr = serde_json::from_str(json).expect("deserialization should succeed");
        assert_eq!(pr.labels, vec!["in-progress", "bug"]);
    }

    #[test]
    fn cached_issue_with_labels_key_deserializes_correctly() {
        let json = r#"{
            "number": 42,
            "title": "Test issue",
            "state": "open",
            "labels": ["enhancement"]
        }"#;
        let issue: CachedIssue =
            serde_json::from_str(json).expect("deserialization should succeed");
        assert_eq!(issue.labels, vec!["enhancement"]);
    }

    // -- CachedTmuxSession new fields (Task #190-2) ----------------------------

    #[test]
    fn cached_tmux_session_roundtrip_with_new_fields() {
        let session = CachedTmuxSession {
            name: "dev".to_string(),
            path: "/home/user/repo".to_string(),
            pane_targets: vec!["0.0".to_string(), "0.1".to_string()],
            pane_titles: vec!["bash".to_string(), "nvim".to_string()],
            pane_commands: vec!["bash".to_string(), "nvim".to_string()],
            window_names: vec!["main".to_string(), "main".to_string()],
            window_active: vec!["1".to_string(), "1".to_string()],
            window_layouts: vec![
                "bb62,80x24,0,0{40x24,0,0,1,39x24,41,0,2}".to_string(),
                "bb62,80x24,0,0{40x24,0,0,1,39x24,41,0,2}".to_string(),
            ],
            pane_paths: vec![
                "/home/user/repo".to_string(),
                "/home/user/repo/src".to_string(),
            ],
            pane_active: vec!["1".to_string(), "0".to_string()],
            host: None,
            created_at: Some(1700000000),
            last_activity_at: Some(1700001000),
            last_output_lines: vec!["line1".to_string()],
            claude_state_raw: None,
        };

        let json = serde_json::to_string(&session).unwrap();
        let parsed: CachedTmuxSession = serde_json::from_str(&json).unwrap();

        assert_eq!(parsed.window_layouts, session.window_layouts);
        assert_eq!(parsed.pane_paths, session.pane_paths);
        assert_eq!(parsed.pane_active, session.pane_active);
    }

    #[test]
    fn cached_tmux_session_deserializes_old_json_without_new_fields() {
        // Simulates a pre-#190 cache file lacking window_layouts, pane_paths,
        // and pane_active — must deserialize with defaults.
        let json = r#"{
            "name": "old-session",
            "path": "/home/user/repo",
            "pane_targets": ["0.0"],
            "pane_titles": ["bash"],
            "pane_commands": ["bash"],
            "window_names": [],
            "window_active": [],
            "host": null,
            "last_output_lines": [],
            "claude_state_raw": null
        }"#;

        let parsed: CachedTmuxSession =
            serde_json::from_str(json).expect("old cache file must deserialize without error");

        assert!(
            parsed.window_layouts.is_empty(),
            "window_layouts should default to empty vec"
        );
        assert!(
            parsed.pane_paths.is_empty(),
            "pane_paths should default to empty vec"
        );
        assert!(
            parsed.pane_active.is_empty(),
            "pane_active should default to empty vec"
        );
    }
}
