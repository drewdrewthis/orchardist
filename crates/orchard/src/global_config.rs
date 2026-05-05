//! Global Orchard configuration loaded from `~/.config/orchard/config.json`.
//!
//! Holds the repo registry (slug + path + optional remotes) and user-local
//! preferences such as the preferred terminal app bundle ID for notifications.
//! Machine-local preferences live here rather than per-repo config because they
//! describe the *user's environment*, not the repository.

use std::collections::HashSet;
use std::path::PathBuf;

use serde::{Deserialize, Serialize};

use crate::logger::LOG;
use crate::remote_adapter::RemoteKind;
use crate::session::StandaloneConfig;

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

/// Remote host configuration for a managed repository.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct RemoteConfig {
    /// Logical name for this remote (e.g. "remmy", "gpu-box").
    #[serde(default = "default_remote_name")]
    pub name: String,
    /// SSH target, e.g. "user@host".
    pub host: String,
    /// Absolute path on the remote host.
    pub path: String,
    /// Connection shell: "ssh" or "mosh".
    #[serde(default = "default_shell")]
    pub shell: String,
    /// The adapter kind to use for this remote.
    ///
    /// Serialized as the `"type"` JSON field. Required — configs without this
    /// field are rejected at parse time, preventing silent misclassification.
    #[serde(rename = "type")]
    pub kind: RemoteKind,
    /// When `true`, the transitive-federation walker will follow this remote's
    /// own `orchard list-remotes` output to discover grandchild nodes.
    ///
    /// Defaults to `false` — opt-in per root so existing single-hop configs
    /// are unaffected by deserialization of configs that predate this field.
    #[serde(default)]
    pub allow_transitive: bool,
}

fn default_remote_name() -> String {
    "default".to_string()
}

fn default_shell() -> String {
    "ssh".to_string()
}

/// A single repository entry in the global config.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct RepoConfig {
    /// GitHub slug in "owner/repo" format.
    pub slug: String,
    /// Absolute local path to the repository.
    pub path: String,
    /// Remote hosts for SSH-based worktrees.
    #[serde(default)]
    pub remotes: Vec<RemoteConfig>,
}

impl RepoConfig {
    /// Returns the owner portion of the slug (before '/').
    pub fn owner(&self) -> &str {
        self.slug.split('/').next().unwrap_or("")
    }

    /// Returns the repository name portion of the slug (after '/').
    pub fn repo_name(&self) -> &str {
        self.slug.split('/').nth(1).unwrap_or("")
    }

    /// Returns the first remote, if any. Convenience for single-remote repos.
    pub fn first_remote(&self) -> Option<&RemoteConfig> {
        self.remotes.first()
    }

    /// Finds the remote config whose host matches `host`.
    pub fn remote_for_host(&self, host: &str) -> Option<&RemoteConfig> {
        self.remotes.iter().find(|r| r.host == host)
    }
}

/// Configuration for the `orchard watch` daemon.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct WatchConfig {
    /// How often (seconds) to refresh local sources (worktrees, tmux sessions).
    #[serde(default = "default_local_poll_secs")]
    pub local_poll_secs: u64,
    /// How often (seconds) to do a full refresh including GitHub API calls.
    #[serde(default = "default_full_poll_secs")]
    pub full_poll_secs: u64,
    /// Minimum seconds between repeated threshold notifications for the same metric.
    #[serde(default = "default_threshold_cooldown_secs")]
    pub threshold_cooldown_secs: u64,
    /// Whether to send desktop notifications for watch events.
    #[serde(default = "default_notifications")]
    pub notifications: bool,
    /// Optional override for the webhook server's bound port.
    /// Precedence: CLI --port > ORCHARD_WEBHOOK_PORT env > this field > 8477 default.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub webhook_port: Option<u16>,
}

fn default_local_poll_secs() -> u64 {
    5
}
fn default_full_poll_secs() -> u64 {
    60
}
fn default_threshold_cooldown_secs() -> u64 {
    300
}
fn default_notifications() -> bool {
    true
}

impl Default for WatchConfig {
    fn default() -> Self {
        WatchConfig {
            local_poll_secs: default_local_poll_secs(),
            full_poll_secs: default_full_poll_secs(),
            threshold_cooldown_secs: default_threshold_cooldown_secs(),
            notifications: default_notifications(),
            webhook_port: None,
        }
    }
}

/// Top-level global configuration for Orchard.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct GlobalConfig {
    /// Registered repositories.
    #[serde(default)]
    pub repos: Vec<RepoConfig>,
    /// macOS bundle ID of the terminal app to activate when a notification is clicked.
    ///
    /// Defaults to `"com.apple.Terminal"`. Common values:
    /// - `"com.apple.Terminal"` — Terminal.app
    /// - `"com.googlecode.iterm2"` — iTerm2
    /// - `"dev.warp.Warp-Stable"` — Warp
    /// - `"org.alacritty"` — Alacritty
    /// - `"com.mitchellh.ghostty"` — Ghostty
    #[serde(default = "default_terminal_app")]
    pub terminal_app: String,
    /// Standalone tmux sessions not tied to any worktree (e.g. a shepherd session).
    ///
    /// Each entry defines a named tmux session with a command and working directory.
    /// Sessions with `start_on_launch: true` are auto-created when orchard starts.
    #[serde(default)]
    pub tmux_sessions: Vec<StandaloneConfig>,
    /// The orchardist tmux session name to use as the default target for `orchard chat`.
    ///
    /// Set by the `orchard init` wizard. When `None` (the default), `orchard chat`
    /// falls back to the first entry in `tmux_sessions`.
    #[serde(default)]
    pub chat_target: Option<String>,
    /// Configuration for the `orchard watch` daemon.
    #[serde(default)]
    pub watch: WatchConfig,
    /// Glob patterns for gate checks (process/policy checks, not code CI).
    ///
    /// A check whose name matches any of these patterns is classified as a
    /// *gate* check with its own `ci_gate_state` rollup, separate from the
    /// `ci_code_state` rollup for ordinary CI checks.
    ///
    /// Patterns are case-insensitive. A single `*` does not cross `/`;
    /// use `**` for recursive matching. Defaults to the three standard gate
    /// checks shipped with Orchard.
    #[serde(default = "default_ci_gate_patterns")]
    pub ci_gate_patterns: Vec<String>,
}

impl Default for GlobalConfig {
    fn default() -> Self {
        GlobalConfig {
            repos: Vec::new(),
            terminal_app: default_terminal_app(),
            tmux_sessions: Vec::new(),
            chat_target: None,
            watch: WatchConfig::default(),
            ci_gate_patterns: default_ci_gate_patterns(),
        }
    }
}

/// Returns the default set of gate check patterns.
///
/// Includes the GitHub Apps approval check, Mintlify docs deployment, and
/// the `license/*` family of CLA checks.
fn default_ci_gate_patterns() -> Vec<String> {
    vec![
        "check-approval-or-label".to_string(),
        "Mintlify Deployment".to_string(),
        "license/*".to_string(),
    ]
}

fn default_terminal_app() -> String {
    "com.apple.Terminal".to_string()
}

// ---------------------------------------------------------------------------
// Config location
// ---------------------------------------------------------------------------

/// Returns the canonical path for writing the global config.
///
/// Always writes to `~/.config/orchard/config.json` (XDG location).
fn global_config_write_path() -> Option<PathBuf> {
    dirs::home_dir().map(|h| h.join(".config").join("orchard").join("config.json"))
}

fn global_config_path() -> Option<PathBuf> {
    // Check XDG-style ~/.config first (cross-platform convention),
    // then fall back to platform-native config dir (~/Library/Application Support on macOS).
    let xdg = dirs::home_dir().map(|h| h.join(".config").join("orchard").join("config.json"));
    if let Some(ref p) = xdg
        && p.exists()
    {
        return xdg;
    }
    dirs::config_dir().map(|d| d.join("orchard").join("config.json"))
}

// ---------------------------------------------------------------------------
// Loading
// ---------------------------------------------------------------------------

/// Loads the global Orchard configuration.
///
/// Pure: reads from disk (or returns the CWD-based fallback) without any
/// side effects. Does not auto-register the current directory.
///
/// Resolution order:
/// 1. `~/.config/orchard/config.json` — explicit multi-repo config.
/// 2. CWD-based single-repo fallback: calls `gh repo view` to detect the
///    current repo slug, uses CWD as the path, and reads `.git/orchard.json`
///    for optional remote config.
/// 3. Empty `GlobalConfig` if neither succeeds.
pub fn load_global_config() -> GlobalConfig {
    if let Some(path) = global_config_path()
        && path.exists()
    {
        return load_from_path(&path);
    }

    fallback_single_repo()
}

/// Registers the CWD repo in `cfg` if it is not already present.
///
/// Detects the current repo via `gh repo view`, appends it to `cfg`, and
/// persists the updated config to disk. On save failure, logs a warning but
/// still returns `true` because the in-memory config was mutated.
///
/// Returns `true` if the config was mutated (a new repo was added), `false`
/// if the CWD was already covered by an existing entry or could not be
/// detected.
///
/// Call this from interactive startup only (TUI launch path). Do **not** call
/// it from `--json`, `heal`, `setup-remote`, or tests.
pub fn register_cwd_repo_if_new(cfg: &mut GlobalConfig) -> bool {
    if !ensure_cwd_repo(cfg) {
        return false;
    }
    if let Err(e) = save_global_config(cfg) {
        LOG.warn(&format!(
            "global_config: failed to persist auto-registered CWD repo: {e}"
        ));
    }
    true
}

/// Persists the given `GlobalConfig` to `~/.config/orchard/config.json`.
///
/// Thin wrapper around [`save_to_path`] that resolves the canonical write
/// path. Creates the parent directory if it does not exist.
pub fn save_global_config(cfg: &GlobalConfig) -> Result<(), String> {
    let path = global_config_write_path()
        .ok_or_else(|| "Could not determine home directory".to_string())?;
    save_to_path(cfg, &path)
}

/// Persists `cfg` to the given `path`.
///
/// Creates the parent directory if needed. Writes atomically via a temporary
/// file to avoid partial writes. Used directly in tests to avoid touching
/// the real `~/.config/orchard/config.json`.
pub fn save_to_path(cfg: &GlobalConfig, path: &std::path::Path) -> Result<(), String> {
    let dir = path
        .parent()
        .ok_or_else(|| "config path has no parent directory".to_string())?;

    std::fs::create_dir_all(dir).map_err(|e| format!("creating {}: {e}", dir.display()))?;

    let json = serde_json::to_string_pretty(cfg).map_err(|e| format!("serializing config: {e}"))?;

    let tmp = path.with_extension("json.tmp");
    std::fs::write(&tmp, &json).map_err(|e| format!("writing {}: {e}", tmp.display()))?;
    std::fs::rename(&tmp, path).map_err(|e| format!("renaming to {}: {e}", path.display()))?;

    LOG.info(&format!("global_config: saved to {}", path.display()));
    Ok(())
}

/// Pure inner logic: appends `(cwd, slug, remotes)` to `cfg` if not already present.
///
/// Returns `true` if the config was mutated (a new repo was appended), `false` otherwise.
/// This function performs no I/O and is fully unit-testable.
fn append_repo_if_new(
    cfg: &mut GlobalConfig,
    cwd: &str,
    slug: &str,
    remotes: Vec<RemoteConfig>,
) -> bool {
    // Check if CWD is inside any configured repo path.
    // Use Path-aware prefix matching so "/workspace/my-project2" does not
    // falsely match a registered "/workspace/my-project".
    // Use Path::starts_with which matches on component boundaries, so
    // "/workspace/my-project2" does not falsely match "/workspace/my-project".
    let cwd_path = std::path::Path::new(cwd);
    if cfg.repos.iter().any(|r| cwd_path.starts_with(&r.path)) {
        return false;
    }

    // Guard against duplicate slugs (repo could be checked out at a different path).
    if cfg.repos.iter().any(|r| r.slug == slug) {
        return false;
    }

    LOG.info(&format!(
        "global_config: appending CWD repo {slug} at {cwd}"
    ));
    cfg.repos.push(RepoConfig {
        slug: slug.to_string(),
        path: cwd.to_string(),
        remotes,
    });
    true
}

/// Checks whether CWD belongs to a configured repo. If not, adds it.
///
/// Returns `true` if the config was mutated (a new repo was appended), `false` otherwise.
fn ensure_cwd_repo(cfg: &mut GlobalConfig) -> bool {
    let cwd = match std::env::current_dir() {
        Ok(d) => d.to_string_lossy().to_string(),
        Err(_) => return false,
    };

    // CWD is not in any configured repo — try to detect it.
    let (owner, name) = match crate::github::get_repo() {
        Ok(pair) => pair,
        Err(_) => return false,
    };

    let slug = format!("{owner}/{name}");
    let remotes = load_orchard_json_remotes(&std::path::PathBuf::from(&cwd));

    append_repo_if_new(cfg, &cwd, &slug, remotes)
}

/// Emits a `LOG.warn` for every remote that still carries the legacy
/// `fallbackKind` JSON field, which was removed as part of ADR-008
/// (issue #329). The field is silently dropped by serde; this helper
/// re-parses the raw JSON as an untyped `Value` to detect its presence.
///
/// Called from [`load_from_path`] in non-test builds (`cfg!(test)` guard).
/// Exposed `pub(crate)` for direct unit testing of the detection logic.
pub(crate) fn warn_legacy_fallback_kind(data: &[u8]) {
    let raw_value: serde_json::Value = match serde_json::from_slice(data) {
        Ok(v) => v,
        Err(_) => return,
    };

    let Some(repos_arr) = raw_value.get("repos").and_then(|v| v.as_array()) else {
        return;
    };

    let warn_if_has_fallback_kind = |remote_val: &serde_json::Value| {
        if remote_val.get("fallbackKind").is_some() {
            let name = remote_val
                .get("name")
                .and_then(|v| v.as_str())
                .unwrap_or("<unknown>");
            LOG.warn(&format!(
                "global_config: remote '{}' has legacy 'fallbackKind' setting which is no \
                 longer honored — see docs/adr/008-federated-discovery.md. To opt into \
                 legacy behaviour for this host, change 'type' to 'remmy'.",
                name
            ));
        }
    };

    for repo_val in repos_arr {
        // Check `remotes` array.
        if let Some(arr) = repo_val.get("remotes").and_then(|v| v.as_array()) {
            for remote_val in arr {
                warn_if_has_fallback_kind(remote_val);
            }
        }
        // Check singular `remote` object.
        if let Some(remote_val) = repo_val.get("remote") {
            warn_if_has_fallback_kind(remote_val);
        }
    }
}

fn load_from_path(path: &PathBuf) -> GlobalConfig {
    let data = match std::fs::read(path) {
        Ok(d) => d,
        Err(e) => {
            LOG.warn(&format!(
                "global_config: failed to read {}: {}",
                path.display(),
                e
            ));
            return GlobalConfig::default();
        }
    };

    // Use a raw intermediate struct so we can accept both `remote` (singular)
    // and `remotes` (plural) per repo entry, plus the legacy `repoPath` alias.
    // `kind` is optional here to support legacy configs that predate the `type`
    // field; entries without `kind` are silently skipped.
    #[derive(Deserialize)]
    struct RawRemote {
        #[serde(default = "default_remote_name")]
        name: String,
        host: String,
        #[serde(default)]
        path: Option<String>,
        #[serde(rename = "repoPath", default)]
        repo_path: Option<String>,
        #[serde(default = "default_shell")]
        shell: String,
        #[serde(rename = "type", default)]
        kind: Option<RemoteKind>,
        #[serde(default)]
        allow_transitive: bool,
        // Note: existing configs may carry a `fallback_kind` field; serde
        // silently ignores unknown fields, so no explicit field is needed.
    }

    #[derive(Deserialize)]
    struct RawRepo {
        slug: String,
        path: String,
        #[serde(default)]
        remote: Option<RawRemote>,
        #[serde(default)]
        remotes: Vec<RawRemote>,
    }

    #[derive(Deserialize)]
    struct RawGlobalConfig {
        #[serde(default)]
        repos: Vec<RawRepo>,
        #[serde(default = "default_terminal_app")]
        terminal_app: String,
        #[serde(default)]
        tmux_sessions: Vec<StandaloneConfig>,
        #[serde(default)]
        chat_target: Option<String>,
        #[serde(default)]
        watch: WatchConfig,
        #[serde(default = "default_ci_gate_patterns")]
        ci_gate_patterns: Vec<String>,
    }

    let raw: RawGlobalConfig = match serde_json::from_slice(&data) {
        Ok(r) => r,
        Err(e) => {
            LOG.warn(&format!(
                "global_config: failed to parse {}: {}",
                path.display(),
                e
            ));
            return GlobalConfig::default();
        }
    };

    // Detect legacy `fallbackKind` field in remotes (silently dropped by serde
    // since the field no longer exists on RemoteConfig). Warn once per remote
    // so users know to reconfigure. Suppressed in test builds to keep test
    // output clean.
    if !cfg!(test) {
        warn_legacy_fallback_kind(&data);
    }

    let repos = raw
        .repos
        .into_iter()
        .map(|raw_repo| {
            let mut remotes: Vec<RemoteConfig> = raw_repo
                .remotes
                .into_iter()
                .map(|r| RemoteConfig {
                    name: r.name,
                    host: r.host,
                    path: r.path.or(r.repo_path).unwrap_or_default(),
                    shell: r.shell,
                    // Legacy configs predate the `type` field; default to Remmy
                    // for backward compatibility when loading from disk.
                    kind: r.kind.unwrap_or(RemoteKind::Remmy),
                    allow_transitive: r.allow_transitive,
                })
                .collect();

            // If `remote` (singular) is present and there are no `remotes`,
            // promote it as the sole entry.
            if remotes.is_empty()
                && let Some(r) = raw_repo.remote
            {
                remotes.push(RemoteConfig {
                    name: r.name,
                    host: r.host,
                    path: r.path.or(r.repo_path).unwrap_or_default(),
                    shell: r.shell,
                    kind: r.kind.unwrap_or(RemoteKind::Remmy),
                    allow_transitive: r.allow_transitive,
                });
            }

            RepoConfig {
                slug: raw_repo.slug,
                path: raw_repo.path,
                remotes,
            }
        })
        .collect();

    // Validate: reject duplicate standalone session names.
    let tmux_sessions = raw.tmux_sessions;
    let mut seen_names: HashSet<String> = HashSet::new();
    for session in &tmux_sessions {
        if !seen_names.insert(session.name.clone()) {
            LOG.warn(&format!(
                "global_config: duplicate standalone session name '{}' in {}",
                session.name,
                path.display()
            ));
            return GlobalConfig::default();
        }
    }

    let cfg = GlobalConfig {
        repos,
        terminal_app: raw.terminal_app,
        tmux_sessions,
        chat_target: raw.chat_target,
        watch: raw.watch,
        ci_gate_patterns: raw.ci_gate_patterns,
    };
    LOG.info(&format!(
        "global_config: loaded {} repo(s), {} standalone session(s) from {}",
        cfg.repos.len(),
        cfg.tmux_sessions.len(),
        path.display()
    ));
    cfg
}

/// Builds a single-repo `GlobalConfig` from the current working directory.
fn fallback_single_repo() -> GlobalConfig {
    let (owner, name) = match crate::github::get_repo() {
        Ok(pair) => pair,
        Err(e) => {
            LOG.info(&format!(
                "global_config: could not detect repo from CWD: {e}"
            ));
            return GlobalConfig::default();
        }
    };

    let cwd = match std::env::current_dir() {
        Ok(d) => d,
        Err(e) => {
            LOG.warn(&format!("global_config: could not read CWD: {e}"));
            return GlobalConfig::default();
        }
    };

    let remotes = load_orchard_json_remotes(&cwd);

    let repo = RepoConfig {
        slug: format!("{owner}/{name}"),
        path: cwd.to_string_lossy().to_string(),
        remotes,
    };

    LOG.info(&format!(
        "global_config: single-repo fallback for {}",
        repo.slug
    ));

    GlobalConfig {
        repos: vec![repo],
        terminal_app: default_terminal_app(),
        tmux_sessions: Vec::new(),
        chat_target: None,
        watch: WatchConfig::default(),
        ci_gate_patterns: default_ci_gate_patterns(),
    }
}

/// Reads `.git/orchard.json` from `repo_root` and extracts all `RemoteConfig`
/// entries. Supports:
/// - `{ "remotes": [{ "name": "...", "host": "...", "repoPath": "..." }] }` (per-repo array)
/// - `{ "remote": { "host": "...", "path": "..." } }` (singular, wrapped as "default")
///
/// Returns an empty vec if the file does not exist or contains no valid remotes.
fn load_orchard_json_remotes(repo_root: &PathBuf) -> Vec<RemoteConfig> {
    // Pure-fs replacement for `git rev-parse --absolute-git-dir` — handles
    // both `.git` directory and worktree-pointer file forms. See #426
    // thin-shell rip-out.
    let git_dir = match crate::paths::resolve_git_dir(repo_root) {
        Some(p) => p,
        None => return Vec::new(),
    };
    let orchard_json = git_dir.join("orchard.json");

    let data = match std::fs::read(&orchard_json) {
        Ok(d) => d,
        Err(_) => return Vec::new(),
    };

    #[derive(Deserialize)]
    struct RawRemote {
        host: Option<String>,
        path: Option<String>,
        #[serde(rename = "repoPath")]
        repo_path: Option<String>,
        #[serde(default)]
        name: Option<String>,
        #[serde(default)]
        shell: Option<String>,
        #[serde(rename = "type", default)]
        kind: Option<RemoteKind>,
        #[serde(default)]
        allow_transitive: bool,
    }

    #[derive(Deserialize)]
    struct RawOrchardJson {
        remote: Option<RawRemote>,
        remotes: Option<Vec<RawRemote>>,
    }

    let raw: RawOrchardJson = match serde_json::from_slice(&data) {
        Ok(r) => r,
        Err(_) => return Vec::new(),
    };

    let mut results = Vec::new();

    // Process the `remotes` array first (preferred format).
    // Legacy per-repo files may omit `"type"`; default to Remmy for back-compat.
    if let Some(entries) = raw.remotes {
        for r in entries {
            if let Some(host) = r.host.filter(|s| !s.is_empty())
                && let Some(path) = r.path.or(r.repo_path).filter(|s| !s.is_empty())
            {
                results.push(RemoteConfig {
                    name: r.name.unwrap_or_else(|| "default".to_string()),
                    host,
                    path,
                    shell: r.shell.unwrap_or_else(|| "ssh".to_string()),
                    kind: r.kind.unwrap_or(RemoteKind::Remmy),
                    allow_transitive: r.allow_transitive,
                });
            }
        }
    }

    // Fall back to singular `remote` only when `remotes` produced nothing.
    if results.is_empty()
        && let Some(r) = raw.remote
        && let Some(host) = r.host.filter(|s| !s.is_empty())
        && let Some(path) = r.path.or(r.repo_path).filter(|s| !s.is_empty())
    {
        results.push(RemoteConfig {
            name: r.name.unwrap_or_else(|| "default".to_string()),
            host,
            path,
            shell: r.shell.unwrap_or_else(|| "ssh".to_string()),
            kind: r.kind.unwrap_or(RemoteKind::Remmy),
            allow_transitive: r.allow_transitive,
        });
    }

    results
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

#[cfg(test)]
mod tests {
    use super::*;
    use std::io::Write;
    use tempfile::tempdir;

    fn write_config(dir: &std::path::Path, json: &str) -> PathBuf {
        let path = dir.join("config.json");
        let mut f = std::fs::File::create(&path).unwrap();
        f.write_all(json.as_bytes()).unwrap();
        path
    }

    #[test]
    fn load_config_from_file() {
        let dir = tempdir().unwrap();
        let json = r#"{
            "repos": [
                { "slug": "owner/repo-a", "path": "/workspace/repo-a" },
                {
                    "slug": "owner/repo-b",
                    "path": "/workspace/repo-b",
                    "remote": { "host": "user@host", "path": "/remote/repo-b" }
                }
            ]
        }"#;
        let path = write_config(dir.path(), json);
        let cfg = load_from_path(&path);

        assert_eq!(cfg.repos.len(), 2);
        assert_eq!(cfg.repos[0].slug, "owner/repo-a");
        assert_eq!(cfg.repos[1].slug, "owner/repo-b");
    }

    #[test]
    fn repo_config_owner_and_name_parsing() {
        let repo = RepoConfig {
            slug: "acme/webapp".to_string(),
            path: "/workspace/webapp".to_string(),
            remotes: vec![],
        };

        assert_eq!(repo.owner(), "acme");
        assert_eq!(repo.repo_name(), "webapp");
    }

    #[test]
    fn empty_repos_returns_empty_config() {
        let dir = tempdir().unwrap();
        let path = write_config(dir.path(), r#"{ "repos": [] }"#);
        let cfg = load_from_path(&path);

        assert_eq!(cfg.repos.len(), 0);
    }

    #[test]
    fn config_with_singular_remote() {
        let dir = tempdir().unwrap();
        let json = r#"{
            "repos": [
                {
                    "slug": "owner/repo",
                    "path": "/workspace/repo",
                    "remote": { "host": "ubuntu@10.0.0.1", "path": "/home/ubuntu/repo" }
                }
            ]
        }"#;
        let path = write_config(dir.path(), json);
        let cfg = load_from_path(&path);

        assert_eq!(cfg.repos[0].remotes.len(), 1);
        let remote = &cfg.repos[0].remotes[0];
        assert_eq!(remote.host, "ubuntu@10.0.0.1");
        assert_eq!(remote.path, "/home/ubuntu/repo");
        assert_eq!(remote.name, "default");
        assert_eq!(remote.shell, "ssh");
    }

    #[test]
    fn config_with_remotes_array() {
        let dir = tempdir().unwrap();
        let json = r#"{
            "repos": [
                {
                    "slug": "owner/repo",
                    "path": "/workspace/repo",
                    "remotes": [
                        { "name": "gpu", "host": "ubuntu@10.0.0.1", "path": "/home/ubuntu/repo", "shell": "mosh" },
                        { "name": "cpu", "host": "ubuntu@10.0.0.2", "path": "/home/ubuntu/repo" }
                    ]
                }
            ]
        }"#;
        let path = write_config(dir.path(), json);
        let cfg = load_from_path(&path);

        assert_eq!(cfg.repos[0].remotes.len(), 2);
        assert_eq!(cfg.repos[0].remotes[0].name, "gpu");
        assert_eq!(cfg.repos[0].remotes[0].shell, "mosh");
        assert_eq!(cfg.repos[0].remotes[1].name, "cpu");
        assert_eq!(cfg.repos[0].remotes[1].shell, "ssh");
    }

    #[test]
    fn config_with_remotes_array_using_repo_path() {
        let dir = tempdir().unwrap();
        let json = r#"{
            "repos": [
                {
                    "slug": "owner/repo",
                    "path": "/workspace/repo",
                    "remotes": [
                        { "name": "remmy", "host": "user@10.0.0.1", "repoPath": "~/webapp-workspace/webapp-bare", "shell": "mosh" }
                    ]
                }
            ]
        }"#;
        let path = write_config(dir.path(), json);
        let cfg = load_from_path(&path);

        assert_eq!(cfg.repos[0].remotes.len(), 1);
        assert_eq!(
            cfg.repos[0].remotes[0].path,
            "~/webapp-workspace/webapp-bare"
        );
    }

    #[test]
    fn config_without_remote() {
        let dir = tempdir().unwrap();
        let json = r#"{
            "repos": [
                { "slug": "owner/repo", "path": "/workspace/repo" }
            ]
        }"#;
        let path = write_config(dir.path(), json);
        let cfg = load_from_path(&path);

        assert!(cfg.repos[0].remotes.is_empty());
    }

    #[test]
    fn first_remote_returns_first_entry() {
        let repo = RepoConfig {
            slug: "owner/repo".to_string(),
            path: "/workspace/repo".to_string(),
            remotes: vec![
                RemoteConfig {
                    name: "gpu".to_string(),
                    host: "ubuntu@10.0.0.1".to_string(),
                    path: "/home/ubuntu/repo".to_string(),
                    shell: "mosh".to_string(),
                    kind: RemoteKind::Remmy,
                    allow_transitive: false,
                },
                RemoteConfig {
                    name: "cpu".to_string(),
                    host: "ubuntu@10.0.0.2".to_string(),
                    path: "/home/ubuntu/repo".to_string(),
                    shell: "ssh".to_string(),
                    kind: RemoteKind::Remmy,
                    allow_transitive: false,
                },
            ],
        };

        let first = repo.first_remote().unwrap();
        assert_eq!(first.name, "gpu");
    }

    #[test]
    fn first_remote_returns_none_when_empty() {
        let repo = RepoConfig {
            slug: "owner/repo".to_string(),
            path: "/workspace/repo".to_string(),
            remotes: vec![],
        };

        assert!(repo.first_remote().is_none());
    }

    #[test]
    fn remote_for_host_finds_matching_remote() {
        let repo = RepoConfig {
            slug: "owner/repo".to_string(),
            path: "/workspace/repo".to_string(),
            remotes: vec![
                RemoteConfig {
                    name: "gpu".to_string(),
                    host: "ubuntu@10.0.0.1".to_string(),
                    path: "/home/ubuntu/repo".to_string(),
                    shell: "mosh".to_string(),
                    kind: RemoteKind::Remmy,
                    allow_transitive: false,
                },
                RemoteConfig {
                    name: "cpu".to_string(),
                    host: "ubuntu@10.0.0.2".to_string(),
                    path: "/home/ubuntu/repo".to_string(),
                    shell: "ssh".to_string(),
                    kind: RemoteKind::Remmy,
                    allow_transitive: false,
                },
            ],
        };

        let found = repo.remote_for_host("ubuntu@10.0.0.2").unwrap();
        assert_eq!(found.name, "cpu");
        assert!(repo.remote_for_host("nonexistent").is_none());
    }

    #[test]
    fn repo_config_owner_with_different_owner_and_name() {
        let repo = RepoConfig {
            slug: "acme/my-project".to_string(),
            path: "/workspace/git-orchard-rs".to_string(),
            remotes: vec![],
        };

        assert_eq!(repo.owner(), "acme");
        assert_eq!(repo.repo_name(), "my-project");
    }

    #[test]
    fn invalid_json_returns_empty_config() {
        let dir = tempdir().unwrap();
        let path = write_config(dir.path(), "not valid json");
        let cfg = load_from_path(&path);

        assert_eq!(cfg.repos.len(), 0);
    }

    #[test]
    fn terminal_app_field_loads_via_load_from_path() {
        let dir = tempdir().unwrap();
        let json = r#"{ "terminal_app": "com.googlecode.iterm2", "repos": [] }"#;
        let path = write_config(dir.path(), json);
        let cfg = load_from_path(&path);

        assert_eq!(cfg.terminal_app, "com.googlecode.iterm2");
    }

    #[test]
    fn terminal_app_defaults_to_terminal_app_when_absent() {
        let dir = tempdir().unwrap();
        let path = write_config(dir.path(), r#"{ "repos": [] }"#);
        let cfg = load_from_path(&path);

        assert_eq!(cfg.terminal_app, "com.apple.Terminal");
    }

    #[test]
    fn terminal_app_serializes_in_global_config() {
        let cfg = GlobalConfig {
            repos: vec![],
            terminal_app: "dev.warp.Warp-Stable".to_string(),
            tmux_sessions: vec![],
            chat_target: None,
            watch: WatchConfig::default(),
            ci_gate_patterns: default_ci_gate_patterns(),
        };
        let json = serde_json::to_string(&cfg).unwrap();

        assert!(json.contains(r#""terminal_app":"dev.warp.Warp-Stable""#));
    }

    #[test]
    fn existing_config_without_terminal_app_loads_with_default() {
        let dir = tempdir().unwrap();
        let json = r#"{
            "repos": [
                { "slug": "owner/repo", "path": "/workspace/repo" }
            ]
        }"#;
        let path = write_config(dir.path(), json);
        let cfg = load_from_path(&path);

        assert_eq!(cfg.repos.len(), 1);
        assert_eq!(cfg.terminal_app, "com.apple.Terminal");
    }

    #[test]
    fn save_global_config_round_trips() {
        let dir = tempdir().unwrap();
        let path = dir.path().join("config.json");

        // We can't use save_global_config directly because it writes to the
        // real home dir; test the serialization/deserialization round trip instead.
        let cfg = GlobalConfig {
            repos: vec![],
            terminal_app: "org.alacritty".to_string(),
            tmux_sessions: vec![],
            chat_target: None,
            watch: WatchConfig::default(),
            ci_gate_patterns: default_ci_gate_patterns(),
        };
        let json = serde_json::to_string_pretty(&cfg).unwrap();
        let mut f = std::fs::File::create(&path).unwrap();
        f.write_all(json.as_bytes()).unwrap();
        drop(f);

        let loaded = load_from_path(&path);
        assert_eq!(loaded.terminal_app, "org.alacritty");
    }

    #[test]
    fn legacy_remotes_array_parses_correctly() {
        #[derive(serde::Deserialize)]
        struct RawRemote {
            host: Option<String>,
            path: Option<String>,
            #[serde(rename = "repoPath")]
            repo_path: Option<String>,
            #[serde(default)]
            name: Option<String>,
            #[serde(default)]
            shell: Option<String>,
        }
        #[derive(serde::Deserialize)]
        struct RawOrchardJson {
            remote: Option<RawRemote>,
            remotes: Option<Vec<RawRemote>>,
        }

        let json = r#"{"remotes":[{"name":"remmy","host":"user@10.0.0.1","repoPath":"~/webapp-workspace/webapp-bare","shell":"mosh"}]}"#;
        let raw: RawOrchardJson = serde_json::from_str(json).unwrap();

        assert!(raw.remote.is_none());
        let remotes = raw.remotes.unwrap();
        assert_eq!(remotes.len(), 1);
        assert_eq!(remotes[0].host.as_deref(), Some("user@10.0.0.1"));
        assert_eq!(
            remotes[0].repo_path.as_deref(),
            Some("~/webapp-workspace/webapp-bare")
        );
        assert_eq!(remotes[0].name.as_deref(), Some("remmy"));
        assert_eq!(remotes[0].shell.as_deref(), Some("mosh"));
        assert!(remotes[0].path.is_none());
    }

    #[test]
    fn remotes_array_takes_precedence_over_singular_remote() {
        let dir = tempdir().unwrap();
        let json = r#"{
            "repos": [
                {
                    "slug": "owner/repo",
                    "path": "/workspace/repo",
                    "remote": { "host": "old@host", "path": "/old" },
                    "remotes": [
                        { "name": "new", "host": "new@host", "path": "/new" }
                    ]
                }
            ]
        }"#;
        let path = write_config(dir.path(), json);
        let cfg = load_from_path(&path);

        assert_eq!(cfg.repos[0].remotes.len(), 1);
        assert_eq!(cfg.repos[0].remotes[0].host, "new@host");
    }

    #[test]
    fn duplicate_standalone_session_names_returns_default_config() {
        let json = r#"{
            "repos": [],
            "tmux_sessions": [
                { "name": "shepherd", "command": "echo 1", "cwd": "/tmp" },
                { "name": "shepherd", "command": "echo 2", "cwd": "/tmp" }
            ]
        }"#;
        let dir = tempfile::tempdir().unwrap();
        let path = write_config(dir.path(), json);
        let cfg = load_from_path(&path);
        // Duplicate names cause fallback to default (empty tmux_sessions).
        assert!(cfg.tmux_sessions.is_empty());
    }

    #[test]
    fn tmux_sessions_default_to_empty_when_omitted() {
        let json = r#"{ "repos": [] }"#;
        let dir = tempfile::tempdir().unwrap();
        let path = write_config(dir.path(), json);
        let cfg = load_from_path(&path);
        assert!(cfg.tmux_sessions.is_empty());
    }

    #[test]
    fn tmux_sessions_load_correctly() {
        let json = r#"{
            "repos": [],
            "tmux_sessions": [
                {
                    "name": "shepherd",
                    "command": "claude --agent shepherd",
                    "cwd": "~/.config/orchard",
                    "start_on_launch": true
                }
            ]
        }"#;
        let dir = tempfile::tempdir().unwrap();
        let path = write_config(dir.path(), json);
        let cfg = load_from_path(&path);
        assert_eq!(cfg.tmux_sessions.len(), 1);
        assert_eq!(cfg.tmux_sessions[0].name, "shepherd");
        assert_eq!(cfg.tmux_sessions[0].command, "claude --agent shepherd");
        assert!(cfg.tmux_sessions[0].start_on_launch);
    }

    // -----------------------------------------------------------------------
    // append_repo_if_new / ensure-and-persist regression tests (issue #158)
    // -----------------------------------------------------------------------

    #[test]
    fn append_repo_if_new_adds_unknown_repo() {
        let mut cfg = GlobalConfig::default();
        let mutated =
            append_repo_if_new(&mut cfg, "/workspace/my-project", "acme/my-project", vec![]);
        assert!(mutated);
        assert_eq!(cfg.repos.len(), 1);
        assert_eq!(cfg.repos[0].slug, "acme/my-project");
        assert_eq!(cfg.repos[0].path, "/workspace/my-project");
    }

    #[test]
    fn append_repo_if_new_skips_when_cwd_inside_known_path() {
        let mut cfg = GlobalConfig {
            repos: vec![RepoConfig {
                slug: "acme/my-project".to_string(),
                path: "/workspace/my-project".to_string(),
                remotes: vec![],
            }],
            terminal_app: default_terminal_app(),
            tmux_sessions: vec![],
            chat_target: None,
            watch: WatchConfig::default(),
            ci_gate_patterns: default_ci_gate_patterns(),
        };
        // CWD is a sub-directory of the already-registered path.
        let mutated = append_repo_if_new(
            &mut cfg,
            "/workspace/my-project/.worktrees/feature",
            "acme/my-project",
            vec![],
        );
        assert!(!mutated);
        assert_eq!(cfg.repos.len(), 1);
    }

    #[test]
    fn append_repo_if_new_skips_duplicate_slug() {
        let mut cfg = GlobalConfig {
            repos: vec![RepoConfig {
                slug: "acme/my-project".to_string(),
                path: "/other/path".to_string(),
                remotes: vec![],
            }],
            terminal_app: default_terminal_app(),
            tmux_sessions: vec![],
            chat_target: None,
            watch: WatchConfig::default(),
            ci_gate_patterns: default_ci_gate_patterns(),
        };
        let mutated =
            append_repo_if_new(&mut cfg, "/workspace/my-project", "acme/my-project", vec![]);
        assert!(!mutated);
        assert_eq!(cfg.repos.len(), 1);
    }

    #[test]
    fn append_repo_if_new_does_not_match_path_prefix_of_sibling() {
        // Regression: "/workspace/my-project2" must NOT match a registered
        // "/workspace/my-project" — raw string prefix match would falsely skip it.
        let mut cfg = GlobalConfig {
            repos: vec![RepoConfig {
                slug: "acme/my-project".to_string(),
                path: "/workspace/my-project".to_string(),
                remotes: vec![],
            }],
            terminal_app: default_terminal_app(),
            tmux_sessions: vec![],
            chat_target: None,
            watch: WatchConfig::default(),
            ci_gate_patterns: default_ci_gate_patterns(),
        };
        let mutated = append_repo_if_new(
            &mut cfg,
            "/workspace/my-project2",
            "acme/my-project2",
            vec![],
        );
        assert!(mutated);
        assert_eq!(cfg.repos.len(), 2);
    }

    #[test]
    fn append_repo_if_new_persists_via_save_to_path() {
        let dir = tempfile::tempdir().unwrap();
        let config_path = dir.path().join("config.json");

        let mut cfg = GlobalConfig::default();
        let mutated =
            append_repo_if_new(&mut cfg, "/workspace/my-project", "acme/my-project", vec![]);
        assert!(mutated);

        save_to_path(&cfg, &config_path).expect("save_to_path failed");

        let reloaded = load_from_path(&config_path);
        assert_eq!(reloaded.repos.len(), 1);
        assert_eq!(reloaded.repos[0].slug, "acme/my-project");
        assert_eq!(reloaded.repos[0].path, "/workspace/my-project");
    }

    // -----------------------------------------------------------------------
    // chat_target field tests (issue #165)
    // -----------------------------------------------------------------------

    #[test]
    fn chat_target_defaults_to_none_when_absent() {
        let dir = tempfile::tempdir().unwrap();
        let path = write_config(dir.path(), r#"{ "repos": [] }"#);
        let cfg = load_from_path(&path);
        assert!(
            cfg.chat_target.is_none(),
            "chat_target must default to None for backward compatibility"
        );
    }

    #[test]
    fn chat_target_loads_when_present() {
        let dir = tempfile::tempdir().unwrap();
        let json = r#"{ "repos": [], "chat_target": "orchardist" }"#;
        let path = write_config(dir.path(), json);
        let cfg = load_from_path(&path);
        assert_eq!(cfg.chat_target.as_deref(), Some("orchardist"));
    }

    #[test]
    fn chat_target_serializes_in_global_config() {
        let cfg = GlobalConfig {
            repos: vec![],
            terminal_app: "com.apple.Terminal".to_string(),
            tmux_sessions: vec![],
            chat_target: Some("orchardist".to_string()),
            watch: WatchConfig::default(),
            ci_gate_patterns: default_ci_gate_patterns(),
        };
        let json = serde_json::to_string(&cfg).unwrap();
        assert!(json.contains(r#""chat_target":"orchardist""#));
    }

    #[test]
    fn chat_target_none_omits_field_or_serializes_null() {
        let cfg = GlobalConfig::default();
        // Backward-compatible: None serializes as null or is omitted — either is fine.
        // We just verify it round-trips.
        let json = serde_json::to_string_pretty(&cfg).unwrap();
        let reloaded: serde_json::Value = serde_json::from_str(&json).unwrap();
        // chat_target should be null or absent — not a non-null value.
        let ct = &reloaded["chat_target"];
        assert!(
            ct.is_null() || ct == &serde_json::Value::Null,
            "chat_target None must serialize as null"
        );
    }

    // -----------------------------------------------------------------------
    // WatchConfig tests
    // -----------------------------------------------------------------------

    #[test]
    fn watch_config_defaults_applied_when_absent_from_config() {
        let dir = tempfile::tempdir().unwrap();
        let path = write_config(dir.path(), r#"{ "repos": [] }"#);
        let cfg = load_from_path(&path);

        assert_eq!(cfg.watch.local_poll_secs, 5);
        assert_eq!(cfg.watch.full_poll_secs, 60);
        assert_eq!(cfg.watch.threshold_cooldown_secs, 300);
        assert!(cfg.watch.notifications);
    }

    #[test]
    fn watch_config_override_loads_from_file() {
        let dir = tempfile::tempdir().unwrap();
        let json = r#"{
            "repos": [],
            "watch": {
                "local_poll_secs": 10,
                "full_poll_secs": 120,
                "threshold_cooldown_secs": 600,
                "notifications": false
            }
        }"#;
        let path = write_config(dir.path(), json);
        let cfg = load_from_path(&path);

        assert_eq!(cfg.watch.local_poll_secs, 10);
        assert_eq!(cfg.watch.full_poll_secs, 120);
        assert_eq!(cfg.watch.threshold_cooldown_secs, 600);
        assert!(!cfg.watch.notifications);
    }

    // -----------------------------------------------------------------------
    // ci_gate_patterns tests (tasks #20, #21, #22)
    // -----------------------------------------------------------------------

    /// Task #20: missing ci_gate_patterns field in config → defaults load.
    #[test]
    fn ci_gate_patterns_defaults_when_field_absent() {
        let dir = tempfile::tempdir().unwrap();
        let path = write_config(dir.path(), r#"{ "repos": [] }"#);
        let cfg = load_from_path(&path);
        assert_eq!(
            cfg.ci_gate_patterns,
            vec![
                "check-approval-or-label".to_string(),
                "Mintlify Deployment".to_string(),
                "license/*".to_string(),
            ]
        );
    }

    /// Task #21: ci_gate_patterns serializes in snake_case on disk.
    #[test]
    fn ci_gate_patterns_serializes_in_snake_case() {
        let cfg = GlobalConfig {
            repos: vec![],
            terminal_app: default_terminal_app(),
            tmux_sessions: vec![],
            chat_target: None,
            watch: WatchConfig::default(),
            ci_gate_patterns: vec!["custom-gate".to_string()],
        };
        let json = serde_json::to_string(&cfg).unwrap();
        assert!(
            json.contains(r#""ci_gate_patterns""#),
            "key must be snake_case ci_gate_patterns, got: {json}"
        );
    }

    /// Task #22: custom pattern from GlobalConfig is used by classify_check.
    #[test]
    fn custom_gate_pattern_via_global_config_classifies_check() {
        use crate::ci_state::{CheckBucket, GateMatcher, classify_check};

        let dir = tempfile::tempdir().unwrap();
        let json = r#"{
            "repos": [],
            "ci_gate_patterns": [
                "check-approval-or-label",
                "Mintlify Deployment",
                "license/*",
                "security-review"
            ]
        }"#;
        let path = write_config(dir.path(), json);
        let cfg = load_from_path(&path);

        let matcher = GateMatcher::new(&cfg.ci_gate_patterns);
        assert_eq!(
            classify_check("security-review", &matcher),
            CheckBucket::Gate
        );
        assert_eq!(classify_check("test-unit", &matcher), CheckBucket::Code);
    }

    // -----------------------------------------------------------------------
    // fallbackKind deprecation detection tests (issue #329)
    // -----------------------------------------------------------------------

    /// Config containing `fallbackKind` in a `remotes` array entry still
    /// deserializes correctly (serde silently drops unknown fields).
    #[test]
    fn config_with_legacy_fallback_kind_still_loads() {
        let dir = tempdir().unwrap();
        let json = r#"{
            "repos": [
                {
                    "slug": "owner/repo",
                    "path": "/workspace/repo",
                    "remotes": [
                        {
                            "name": "boxd",
                            "host": "user@vm.boxd.sh",
                            "path": "/remote/repo",
                            "type": "orchard-proxy",
                            "fallbackKind": "remmy"
                        }
                    ]
                }
            ]
        }"#;
        let path = write_config(dir.path(), json);
        let cfg = load_from_path(&path);

        // Config loads despite unknown fallbackKind — serde ignores it.
        assert_eq!(cfg.repos.len(), 1);
        assert_eq!(cfg.repos[0].remotes.len(), 1);
        assert_eq!(cfg.repos[0].remotes[0].name, "boxd");
    }

    /// `warn_legacy_fallback_kind` does not panic on well-formed JSON without
    /// the deprecated field.
    #[test]
    fn warn_legacy_fallback_kind_no_panic_on_clean_config() {
        let json = r#"{
            "repos": [
                {
                    "slug": "owner/repo",
                    "path": "/workspace/repo",
                    "remotes": [
                        { "name": "boxd", "host": "user@vm", "path": "/repo", "type": "orchard-proxy" }
                    ]
                }
            ]
        }"#;
        // Should not panic — no fallbackKind present.
        warn_legacy_fallback_kind(json.as_bytes());
    }

    /// `warn_legacy_fallback_kind` does not panic on JSON with `fallbackKind`
    /// present in a singular `remote` entry.
    #[test]
    fn warn_legacy_fallback_kind_no_panic_on_singular_remote_with_field() {
        let json = r#"{
            "repos": [
                {
                    "slug": "owner/repo",
                    "path": "/workspace/repo",
                    "remote": {
                        "name": "boxd",
                        "host": "user@vm",
                        "path": "/repo",
                        "fallbackKind": "remmy"
                    }
                }
            ]
        }"#;
        // Should not panic.
        warn_legacy_fallback_kind(json.as_bytes());
    }
}
