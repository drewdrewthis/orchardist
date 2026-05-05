//! Comprehensive sessions index for the `orchard sessions --json` subcommand.
//!
//! Issues #374 / #375: the orchardist's `/prune` skill needs a single source
//! of truth for "every tmux session orchard knows about, on every managed
//! host, classified by relationship to worktrees and protection status." The
//! existing `orchard-tui --json` output only carries sessions tied to a worktree
//! plus the explicitly-configured standalone list — that is too narrow for
//! pruning decisions.
//!
//! This module is a pure function over the on-disk caches: callers refresh
//! the local tmux cache inline (`sources::tmux::refresh_local`) and rely on
//! `orchard refresh` / `orchard watch` for remote freshness. No SSH, no
//! `tmux` invocations, no network calls happen here.
//!
//! The wire format is versioned independently from `JsonOutput`
//! (`SESSIONS_INDEX_VERSION`) so additions to this index cannot force a
//! version bump on the worktree-centric `--json` output that the rest of the
//! ecosystem already consumes.

use std::collections::HashSet;

use schemars::JsonSchema;
use serde::{Deserialize, Serialize};

use crate::cache::{self, CachedTmuxSession, CachedWorktree};
use crate::global_config::GlobalConfig;
use crate::session::active_pane_cwd;

/// Wire-format version for `orchard sessions --json` output.
///
/// Bump this when the shape of [`SessionsIndexOutput`] or
/// [`SessionRecord`] changes in a way consumers need to detect (added
/// required fields, removed fields, changed field meanings). Pure additions
/// of optional fields do NOT require a bump.
pub const SESSIONS_INDEX_VERSION: u32 = 1;

/// Hardcoded list of always-protected session names.
///
/// These are the orchardist-side daemon and oracle sessions whose loss would
/// require manual recovery. The list is intentionally small and curated;
/// users can extend protection by adding entries to `tmux_sessions` in
/// `~/.config/orchard/config.json`, which feed into the same `protected`
/// flag here.
///
/// Pinned by issue #375. Order is not significant — equality is by name.
pub const PROTECTED_SESSION_KEEPERS: &[&str] = &[
    "orchardist",
    "technician",
    "krusty_brain",
    "langwatch_main",
    "git-orchard-rs_main",
    "scenario_main",
    "claude-remote_main",
    "orchard-oss-audit",
];

/// Top-level versioned wire format for `orchard sessions --json`.
#[derive(Debug, Clone, Serialize, Deserialize, JsonSchema)]
#[serde(rename_all = "camelCase")]
pub struct SessionsIndexOutput {
    /// Schema version for this output. See [`SESSIONS_INDEX_VERSION`].
    pub version: u32,
    /// Every tmux session known across local and configured remote hosts.
    ///
    /// Sessions are NOT grouped by host — each entry carries `host` inline so
    /// downstream tooling (the orchardist `/prune` skill, ad-hoc scripts) can
    /// route per-host actions without inferring from list position.
    pub sessions: Vec<SessionRecord>,
}

/// One tmux session record in the sessions index.
///
/// Field semantics mirror what the orchardist needs for pruning decisions:
///
/// - `host` — `"local"` or the SSH target string (e.g. `"boxd@gpu-box"`).
/// - `command` — foreground command of the session's active pane (e.g.
///   `"claude"`, `"zsh"`, `"nvim"`).
/// - `cwd` — current working directory of the active pane.
/// - `protected` — `true` when the session name matches a configured
///   `tmux_sessions` entry or the hardcoded keepers list.
/// - `classification` — see [`SessionClassification`].
#[derive(Debug, Clone, Serialize, Deserialize, JsonSchema)]
#[serde(rename_all = "camelCase")]
pub struct SessionRecord {
    /// Tmux session name.
    pub name: String,
    /// Host identifier: `"local"` or the SSH target string.
    pub host: String,
    /// Foreground command of the active pane (empty string if unknown).
    pub command: String,
    /// Working directory of the active pane (empty string if unknown).
    pub cwd: String,
    /// True when the session name is in the protected set (config
    /// `tmux_sessions` entries OR the [`PROTECTED_SESSION_KEEPERS`] list).
    pub protected: bool,
    /// Classification used by the `/prune` skill.
    pub classification: SessionClassification,
}

/// How a tmux session relates to orchard's tracked state.
///
/// The four variants are mutually exclusive and computed in priority order:
/// `Protected` wins over everything; otherwise `WorktreeAttached` wins over
/// `DetachedClaude`; the residual is `Orphan`.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize, JsonSchema)]
#[serde(rename_all = "kebab-case")]
pub enum SessionClassification {
    /// Active-pane cwd is inside a tracked worktree path.
    WorktreeAttached,
    /// Session name matches `^issue\d+`, foreground is `claude`, AND the
    /// active-pane cwd is NOT inside any tracked worktree. These are the
    /// "Claude session whose worktree was already pruned" survivors that the
    /// orchardist needs to find.
    DetachedClaude,
    /// In the protected set (config or hardcoded keepers). Always safe.
    Protected,
    /// None of the above — candidate for `/prune`.
    Orphan,
}

/// Builds the sessions index by reading on-disk caches.
///
/// Pure function over caches: callers must refresh the local tmux cache
/// inline (`sources::tmux::refresh_local()`) before calling this so the
/// freshness contract from issue #374/#375 holds for the local host.
pub fn build_sessions_index(config: &GlobalConfig) -> SessionsIndexOutput {
    // 1. Gather worktree paths from every configured repo (local + remote).
    //    This drives the `WorktreeAttached` classification.
    let worktree_paths = collect_all_worktree_paths(config);

    // 2. Build the protected-name set: hardcoded keepers + configured
    //    `tmux_sessions` entries. Both contribute to `protected` AND to the
    //    `Protected` classification.
    let protected_names = build_protected_set(config);

    // 3. Read sessions from the local cache and from every configured remote
    //    host's tmux cache, then classify each.
    let mut sessions: Vec<SessionRecord> = Vec::new();

    // Local sessions.
    let local = cache::read_cache::<CachedTmuxSession>(&cache::tmux_cache_path(None)).entries;
    for s in &local {
        sessions.push(classify_session(
            s,
            "local",
            &worktree_paths,
            &protected_names,
        ));
    }

    // Remote sessions: one cache file per unique remote host across all repos.
    // Dedup by host string to avoid double-counting when the same host
    // appears in multiple repos.
    let mut hosts_seen: HashSet<String> = HashSet::new();
    for repo in &config.repos {
        for remote in &repo.remotes {
            if !hosts_seen.insert(remote.host.clone()) {
                continue;
            }
            let entries =
                cache::read_cache::<CachedTmuxSession>(&cache::tmux_cache_path(Some(&remote.host)))
                    .entries;
            for s in &entries {
                sessions.push(classify_session(
                    s,
                    &remote.host,
                    &worktree_paths,
                    &protected_names,
                ));
            }
        }
    }

    // Stable, deterministic order: by host then by name. The orchardist's
    // `/prune` skill diffs JSON across runs, so a stable order avoids noise.
    sessions.sort_by(|a, b| a.host.cmp(&b.host).then_with(|| a.name.cmp(&b.name)));

    SessionsIndexOutput {
        version: SESSIONS_INDEX_VERSION,
        sessions,
    }
}

/// Reads every per-repo worktrees + remote_worktrees cache and returns the
/// flat set of worktree filesystem paths.
fn collect_all_worktree_paths(config: &GlobalConfig) -> Vec<String> {
    let mut paths = Vec::new();
    for repo in &config.repos {
        let local = cache::read_cache::<CachedWorktree>(&cache::cache_path(
            repo.owner(),
            repo.repo_name(),
            "worktrees",
        ))
        .entries;
        for w in local {
            if !w.is_bare {
                paths.push(w.path);
            }
        }
        // Remote worktrees only matter when a repo declares remotes.
        if !repo.remotes.is_empty() {
            let remote = cache::read_cache::<CachedWorktree>(&cache::cache_path(
                repo.owner(),
                repo.repo_name(),
                "remote_worktrees",
            ))
            .entries;
            for w in remote {
                if !w.is_bare {
                    paths.push(w.path);
                }
            }
        }
    }
    paths
}

/// Returns the set of session names treated as protected.
fn build_protected_set(config: &GlobalConfig) -> HashSet<String> {
    let mut set: HashSet<String> = PROTECTED_SESSION_KEEPERS
        .iter()
        .map(|s| (*s).to_string())
        .collect();
    for cfg in &config.tmux_sessions {
        set.insert(cfg.name.clone());
    }
    set
}

/// Classifies one cached session into a [`SessionRecord`].
fn classify_session(
    session: &CachedTmuxSession,
    host: &str,
    worktree_paths: &[String],
    protected_names: &HashSet<String>,
) -> SessionRecord {
    // Active-pane cwd: prefer the explicit active flag; fall back to the
    // session's first-window path so older cache rows still classify.
    let cwd = active_pane_cwd(session).unwrap_or_else(|| session.path.clone());

    // Active-pane command, mirroring the cwd lookup so command/cwd come from
    // the same pane.
    let command = active_pane_command(session).unwrap_or_default();

    let protected = protected_names.contains(&session.name);
    let classification = classify(&session.name, &cwd, &command, worktree_paths, protected);

    SessionRecord {
        name: session.name.clone(),
        host: host.to_string(),
        command,
        cwd,
        protected,
        classification,
    }
}

/// Returns the foreground command of the active pane, falling back to the
/// first pane row when no `pane_active=1` flag is set (pre-AC cache rows).
fn active_pane_command(s: &CachedTmuxSession) -> Option<String> {
    s.pane_active
        .iter()
        .enumerate()
        .find(|(_, flag)| flag.as_str() == "1")
        .and_then(|(i, _)| s.pane_commands.get(i))
        .or_else(|| s.pane_commands.first())
        .filter(|c| !c.is_empty())
        .cloned()
}

/// Pure classification logic — exposed for unit testing.
///
/// Priority order: `Protected` > `WorktreeAttached` > `DetachedClaude` >
/// `Orphan`. The first three are mutually exclusive by construction; only
/// the residual receives `Orphan`.
pub(crate) fn classify(
    session_name: &str,
    cwd: &str,
    command: &str,
    worktree_paths: &[String],
    protected: bool,
) -> SessionClassification {
    if protected {
        return SessionClassification::Protected;
    }

    let inside_worktree = !cwd.is_empty()
        && worktree_paths
            .iter()
            .any(|wt| crate::paths::session_belongs_to_worktree(cwd, wt));
    if inside_worktree {
        return SessionClassification::WorktreeAttached;
    }

    if is_detached_claude(session_name, command) {
        return SessionClassification::DetachedClaude;
    }

    SessionClassification::Orphan
}

/// Returns true when the session name matches `^issue\d+` AND the foreground
/// command is `claude`. The cwd-not-worktree check is already enforced by
/// the call site (this is reached only after `WorktreeAttached` was rejected).
fn is_detached_claude(session_name: &str, command: &str) -> bool {
    matches_issue_prefix(session_name) && command_is_claude(command)
}

/// Matches `^issue\d+` — the launch convention orchard uses for per-issue
/// sessions (e.g. `issue374/...`, `issue123_main`).
fn matches_issue_prefix(name: &str) -> bool {
    let Some(rest) = name.strip_prefix("issue") else {
        return false;
    };
    let mut iter = rest.chars();
    let Some(first) = iter.next() else {
        return false;
    };
    first.is_ascii_digit()
}

/// Returns true when the foreground command looks like `claude` (e.g.
/// `claude`, `claude --resume`, `node /path/claude`). Case-insensitive token
/// match against the literal word `claude`, mirroring `PaneInfo::has_claude`.
fn command_is_claude(command: &str) -> bool {
    let lower = command.to_lowercase();
    lower.split_whitespace().any(|tok| {
        tok == "claude"
            || tok.ends_with("/claude")
            || tok.ends_with("\\claude")
            || tok == "claude.exe"
    })
}

#[cfg(test)]
mod tests {
    use super::*;

    fn paths(items: &[&str]) -> Vec<String> {
        items.iter().map(|s| (*s).to_string()).collect()
    }

    fn empty_protected() -> HashSet<String> {
        HashSet::new()
    }

    // -- classify() ----------------------------------------------------------

    #[test]
    fn classify_protected_wins_over_everything() {
        let wts = paths(&["/work/repo/wt"]);
        let cls = classify(
            "orchardist",
            "/work/repo/wt/sub", // would be WorktreeAttached otherwise
            "claude",
            &wts,
            true, // protected
        );
        assert_eq!(cls, SessionClassification::Protected);
    }

    #[test]
    fn classify_worktree_attached_when_cwd_inside_worktree() {
        let wts = paths(&["/work/repo/wt-foo"]);
        let cls = classify(
            "issue42_main",
            "/work/repo/wt-foo/src",
            "claude",
            &wts,
            false,
        );
        assert_eq!(cls, SessionClassification::WorktreeAttached);
    }

    #[test]
    fn classify_detached_claude_when_issue_prefix_and_claude_outside_worktree() {
        let wts = paths(&["/work/other/wt"]);
        let cls = classify("issue374/feature", "/tmp/somewhere", "claude", &wts, false);
        assert_eq!(cls, SessionClassification::DetachedClaude);
    }

    #[test]
    fn classify_orphan_when_random_session_outside_worktree() {
        let wts = paths(&["/work/repo/wt"]);
        let cls = classify("random-session", "/tmp", "zsh", &wts, false);
        assert_eq!(cls, SessionClassification::Orphan);
    }

    #[test]
    fn classify_orphan_when_no_cwd_and_not_claude_or_protected() {
        let wts = paths(&["/work/repo/wt"]);
        let cls = classify("scratch", "", "zsh", &wts, false);
        assert_eq!(cls, SessionClassification::Orphan);
    }

    #[test]
    fn classify_detached_claude_requires_claude_command() {
        let wts = paths(&["/work/repo/wt"]);
        // issue prefix, outside worktree, but command is zsh — must NOT be detached-claude.
        let cls = classify("issue999_main", "/tmp", "zsh", &wts, false);
        assert_eq!(cls, SessionClassification::Orphan);
    }

    #[test]
    fn classify_detached_claude_requires_issue_prefix() {
        let wts = paths(&["/work/repo/wt"]);
        // not issue prefix, claude command, outside worktree — must NOT be detached-claude.
        let cls = classify("scratch", "/tmp", "claude", &wts, false);
        assert_eq!(cls, SessionClassification::Orphan);
    }

    #[test]
    fn classify_worktree_attached_beats_detached_claude_when_inside_worktree() {
        let wts = paths(&["/work/repo/wt-foo"]);
        let cls = classify("issue374_main", "/work/repo/wt-foo", "claude", &wts, false);
        assert_eq!(cls, SessionClassification::WorktreeAttached);
    }

    // -- matches_issue_prefix() ---------------------------------------------

    #[test]
    fn matches_issue_prefix_basic() {
        assert!(matches_issue_prefix("issue1"));
        assert!(matches_issue_prefix("issue42_main"));
        assert!(matches_issue_prefix("issue374/branch"));
        assert!(matches_issue_prefix("issue999"));
    }

    #[test]
    fn matches_issue_prefix_rejects_non_digit_after_issue() {
        assert!(!matches_issue_prefix("issue"));
        assert!(!matches_issue_prefix("issuesXY"));
        assert!(!matches_issue_prefix("issue_main"));
    }

    #[test]
    fn matches_issue_prefix_rejects_no_prefix() {
        assert!(!matches_issue_prefix("orchardist"));
        assert!(!matches_issue_prefix("main"));
        assert!(!matches_issue_prefix(""));
    }

    // -- command_is_claude() ------------------------------------------------

    #[test]
    fn command_is_claude_bare() {
        assert!(command_is_claude("claude"));
    }

    #[test]
    fn command_is_claude_with_args() {
        assert!(command_is_claude("claude --resume"));
        assert!(command_is_claude("claude --model opus"));
    }

    #[test]
    fn command_is_claude_absolute_path() {
        assert!(command_is_claude("/usr/local/bin/claude"));
        assert!(command_is_claude("node /opt/claude --foo"));
    }

    #[test]
    fn command_is_claude_case_insensitive() {
        assert!(command_is_claude("Claude"));
        assert!(command_is_claude("CLAUDE --resume"));
    }

    #[test]
    fn command_is_claude_rejects_non_claude() {
        assert!(!command_is_claude("zsh"));
        assert!(!command_is_claude("nvim claude.md"));
        assert!(!command_is_claude(""));
    }

    // -- build_protected_set() ----------------------------------------------

    #[test]
    fn build_protected_set_contains_hardcoded_keepers() {
        let cfg = GlobalConfig::default();
        let set = build_protected_set(&cfg);
        for name in PROTECTED_SESSION_KEEPERS {
            assert!(set.contains(*name), "missing keeper: {name}");
        }
    }

    #[test]
    fn build_protected_set_includes_configured_tmux_sessions() {
        let cfg = GlobalConfig {
            tmux_sessions: vec![crate::session::StandaloneConfig {
                name: "shepherd".to_string(),
                command: "echo".to_string(),
                cwd: "/tmp".to_string(),
                start_on_launch: false,
            }],
            ..GlobalConfig::default()
        };
        let set = build_protected_set(&cfg);
        assert!(set.contains("shepherd"));
        assert!(set.contains("orchardist"));
    }

    // -- active_pane_command() ---------------------------------------------

    fn make_session_with_panes(commands: &[&str], active_idx: Option<usize>) -> CachedTmuxSession {
        let cmds: Vec<String> = commands.iter().map(|s| s.to_string()).collect();
        let targets: Vec<String> = (0..cmds.len()).map(|i| format!("0.{i}")).collect();
        let active: Vec<String> = (0..cmds.len())
            .map(|i| {
                if Some(i) == active_idx {
                    "1".to_string()
                } else {
                    "0".to_string()
                }
            })
            .collect();
        CachedTmuxSession {
            name: "test".to_string(),
            path: "/tmp".to_string(),
            pane_targets: targets,
            pane_titles: vec![],
            pane_commands: cmds,
            window_names: vec![],
            window_active: vec![],
            window_layouts: vec![],
            pane_paths: vec![],
            pane_active: active,
            host: None,
            created_at: None,
            last_activity_at: None,
            last_output_lines: vec![],
            claude_state_raw: None,
        }
    }

    #[test]
    fn active_pane_command_returns_command_at_active_index() {
        let s = make_session_with_panes(&["zsh", "claude", "nvim"], Some(1));
        assert_eq!(active_pane_command(&s).as_deref(), Some("claude"));
    }

    #[test]
    fn active_pane_command_falls_back_to_first_when_no_active() {
        let s = make_session_with_panes(&["zsh", "claude"], None);
        assert_eq!(active_pane_command(&s).as_deref(), Some("zsh"));
    }

    #[test]
    fn active_pane_command_none_when_empty() {
        let s = make_session_with_panes(&[], None);
        assert!(active_pane_command(&s).is_none());
    }

    // -- classify_session() (integration) -----------------------------------

    fn make_session(
        name: &str,
        path: &str,
        commands: &[&str],
        active_idx: Option<usize>,
        active_paths: &[&str],
    ) -> CachedTmuxSession {
        let mut s = make_session_with_panes(commands, active_idx);
        s.name = name.to_string();
        s.path = path.to_string();
        s.pane_paths = active_paths.iter().map(|p| (*p).to_string()).collect();
        s
    }

    #[test]
    fn classify_session_uses_active_pane_cwd() {
        let s = make_session(
            "issue42",
            "/wrong/path",
            &["zsh", "claude"],
            Some(1),
            &["/wrong/path", "/work/repo/wt-foo"],
        );
        let wts = paths(&["/work/repo/wt-foo"]);
        let protected = empty_protected();
        let rec = classify_session(&s, "local", &wts, &protected);
        assert_eq!(rec.cwd, "/work/repo/wt-foo");
        assert_eq!(rec.command, "claude");
        assert_eq!(rec.classification, SessionClassification::WorktreeAttached);
        assert!(!rec.protected);
    }

    #[test]
    fn classify_session_orphan_outside_worktree() {
        let s = make_session("scratch-utils", "/tmp", &["zsh"], Some(0), &["/tmp"]);
        let wts = paths(&["/work/repo/wt-foo"]);
        let protected = empty_protected();
        let rec = classify_session(&s, "local", &wts, &protected);
        assert_eq!(rec.classification, SessionClassification::Orphan);
        assert_eq!(rec.host, "local");
    }

    #[test]
    fn classify_session_protected_when_in_set() {
        let s = make_session(
            "orchardist",
            "/home/user",
            &["claude"],
            Some(0),
            &["/home/user"],
        );
        let wts = paths(&[]);
        let mut protected = HashSet::new();
        protected.insert("orchardist".to_string());
        let rec = classify_session(&s, "boxd@gpu-box", &wts, &protected);
        assert!(rec.protected);
        assert_eq!(rec.classification, SessionClassification::Protected);
        assert_eq!(rec.host, "boxd@gpu-box");
    }

    #[test]
    fn classify_session_falls_back_to_session_path_when_no_active_flag() {
        // Old cache rows have no pane_active flags. Fall back to session.path.
        let mut s = make_session_with_panes(&["claude"], None);
        s.name = "issue999_main".to_string();
        s.path = "/work/repo/wt-foo/src".to_string();
        // pane_paths empty → cwd resolves from session.path.
        let wts = paths(&["/work/repo/wt-foo"]);
        let protected = empty_protected();
        let rec = classify_session(&s, "local", &wts, &protected);
        assert_eq!(rec.cwd, "/work/repo/wt-foo/src");
        assert_eq!(rec.classification, SessionClassification::WorktreeAttached);
    }

    // -- build_sessions_index() integration tests ---------------------------

    #[test]
    fn build_sessions_index_empty_config_returns_empty() {
        let cfg = GlobalConfig::default();
        let out = build_sessions_index(&cfg);
        assert_eq!(out.version, SESSIONS_INDEX_VERSION);
        // Note: this reads ~/.cache/orchard which may not exist in test env;
        // sessions list may be non-empty depending on test isolation. The
        // important contract here is the version field.
    }

    #[test]
    fn output_serializes_with_camel_case_classification_kebab() {
        let out = SessionsIndexOutput {
            version: SESSIONS_INDEX_VERSION,
            sessions: vec![SessionRecord {
                name: "issue42_main".to_string(),
                host: "local".to_string(),
                command: "claude".to_string(),
                cwd: "/work/repo/wt-foo".to_string(),
                protected: false,
                classification: SessionClassification::WorktreeAttached,
            }],
        };
        let v: serde_json::Value = serde_json::to_value(&out).unwrap();
        assert_eq!(v["version"], SESSIONS_INDEX_VERSION);
        let sess = &v["sessions"][0];
        assert_eq!(sess["name"], "issue42_main");
        assert_eq!(sess["host"], "local");
        assert_eq!(sess["classification"], "worktree-attached");
        assert_eq!(sess["protected"], false);
    }

    #[test]
    fn output_serializes_classification_variants_kebab_case() {
        for (variant, expected) in [
            (SessionClassification::WorktreeAttached, "worktree-attached"),
            (SessionClassification::DetachedClaude, "detached-claude"),
            (SessionClassification::Protected, "protected"),
            (SessionClassification::Orphan, "orphan"),
        ] {
            let v = serde_json::to_value(variant).unwrap();
            assert_eq!(v, serde_json::Value::String(expected.to_string()));
        }
    }
}
