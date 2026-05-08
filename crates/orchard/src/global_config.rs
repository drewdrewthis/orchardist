//! Global Orchard configuration loaded from `~/.orchard/config.json`.
//!
//! Holds the repo registry (slug + path + optional remotes) and user-local
//! preferences such as the preferred terminal app bundle ID for notifications.
//! Machine-local preferences live here rather than per-repo config because they
//! describe the *user's environment*, not the repository.

use std::collections::HashSet;
use std::path::{Path, PathBuf};

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

/// Returns the canonical path for the global config: `~/.orchard/config.json`.
///
/// Both read and write operations use this path. XDG_CONFIG_HOME and the
/// macOS `~/Library/Application Support` directory are intentionally ignored —
/// orchard follows the dotdir convention (`~/.aws`, `~/.kube`, `~/.cargo`,
/// `~/.claude`) rather than platform-native config dirs.
pub fn global_config_path() -> Option<PathBuf> {
    dirs::home_dir().map(|h| h.join(".orchard").join("config.json"))
}

/// Returns the canonical write path for the global config: `~/.orchard/config.json`.
///
/// Identical to [`global_config_path`]. Both functions exist so callers can
/// be explicit about intent (read vs. write) without the path differing.
pub fn global_config_write_path() -> Option<PathBuf> {
    dirs::home_dir().map(|h| h.join(".orchard").join("config.json"))
}

// ---------------------------------------------------------------------------
// Loading
// ---------------------------------------------------------------------------

/// Returns `true` when the legacy config file at `<base>/.config/orchard/config.json`
/// exists. Used exclusively at the config-load failure site to decide whether to
/// emit a migration hint.
///
/// `base` is the user's home directory. Accepting it as a parameter makes this
/// testable without touching the real home directory.
pub(crate) fn legacy_config_exists_at(base: &std::path::Path) -> bool {
    base.join(".config")
        .join("orchard")
        .join("config.json")
        .exists()
}

/// Returns `true` when `~/.config/orchard/config.json` exists.
///
/// Performs a single `stat` call. Called at most once per process at the
/// config-load failure site — never from `global_config_path()`,
/// `global_config_write_path()`, or any other helper.
fn legacy_config_exists() -> bool {
    dirs::home_dir()
        .map(|h| legacy_config_exists_at(&h))
        .unwrap_or(false)
}

/// Builds the migration hint message text.
///
/// Separated from the LOG call so the text itself is unit-testable.
pub(crate) fn migration_hint_message() -> String {
    "Found legacy config at ~/.config/orchard/config.json — \
     the canonical location is now ~/.orchard/config.json. \
     To migrate: mv ~/.config/orchard ~/.orchard"
        .to_string()
}

/// Loads the global Orchard configuration.
///
/// Pure: reads from disk (or returns the CWD-based fallback) without any
/// side effects. Does not auto-register the current directory.
///
/// Resolution order:
/// 1. `~/.orchard/config.json` — explicit multi-repo config.
/// 2. CWD-based single-repo fallback: calls `gh repo view` to detect the
///    current repo slug, uses CWD as the path, and reads `.git/orchard.json`
///    for optional remote config.
/// 3. Empty `GlobalConfig` if neither succeeds.
///
/// When `~/.orchard/config.json` does not exist but the legacy path
/// `~/.config/orchard/config.json` does, a migration hint is logged to
/// `stderr` via [`LOG`] to guide the user. The legacy file is **never**
/// deserialized as a fallback — the hint is informational only.
pub fn load_global_config() -> GlobalConfig {
    if let Some(path) = global_config_path()
        && path.exists()
    {
        return load_from_path(&path);
    }

    // Emit a migration hint (once, at the failure site) when the new path is
    // absent but the legacy path still exists. The legacy file is never read.
    if legacy_config_exists() {
        LOG.warn(&migration_hint_message());
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

/// Persists the given `GlobalConfig` to `~/.orchard/config.json`.
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
/// Creates the parent directory if needed. Performs a shallow merge with the
/// existing on-disk config so that unknown top-level fields (fields not modeled
/// by [`GlobalConfig`]) are preserved as-is. Writes atomically via a temporary
/// file to avoid partial writes. Used directly in tests to avoid touching
/// the real `~/.orchard/config.json`.
///
/// Not safe against concurrent writers. The read-then-write window can lose
/// updates if another process writes between the read and the rename. The
/// merge narrows the clobber window from "any save wipes unknown keys" to
/// "concurrent saves race" — file a follow-up issue if multi-writer safety
/// is needed (e.g. advisory `flock`).
pub fn save_to_path(cfg: &GlobalConfig, path: &std::path::Path) -> Result<(), String> {
    let dir = path
        .parent()
        .ok_or_else(|| "config path has no parent directory".to_string())?;

    std::fs::create_dir_all(dir).map_err(|e| format!("creating {}: {e}", dir.display()))?;

    let cfg_value = serde_json::to_value(cfg).map_err(|e| format!("serializing config: {e}"))?;
    let serde_json::Value::Object(cfg_map) = cfg_value else {
        return Err("serialized config is not a JSON object".to_string());
    };

    let merged = merge_with_existing(path, cfg_map);

    let json =
        serde_json::to_string_pretty(&merged).map_err(|e| format!("serializing config: {e}"))?;

    let tmp = path.with_extension("json.tmp");
    std::fs::write(&tmp, &json).map_err(|e| format!("writing {}: {e}", tmp.display()))?;
    std::fs::rename(&tmp, path).map_err(|e| format!("renaming to {}: {e}", path.display()))?;

    LOG.info(&format!("global_config: saved to {}", path.display()));
    Ok(())
}

/// Reads the existing JSON object at `path` and shallow-merges `new_fields`
/// into it.
///
/// Keys from `new_fields` overwrite matching keys in the existing object.
/// Keys present in the existing object but absent from `new_fields` are
/// preserved as-is. If the file does not exist, cannot be read, or does not
/// parse as a top-level JSON object, `new_fields` is returned as-is.
fn merge_with_existing(
    path: &std::path::Path,
    new_fields: serde_json::Map<String, serde_json::Value>,
) -> serde_json::Value {
    let bytes = match std::fs::read(path) {
        Ok(b) => b,
        // File does not exist or is unreadable — no existing data to preserve.
        Err(_) => return serde_json::Value::Object(new_fields),
    };

    let existing: serde_json::Value = match serde_json::from_slice(&bytes) {
        Ok(v) => v,
        Err(e) => {
            LOG.warn(&format!(
                "global_config: could not parse existing config for merge — skipping: {e}"
            ));
            return serde_json::Value::Object(new_fields);
        }
    };

    match existing {
        serde_json::Value::Object(mut existing_map) => {
            for (key, value) in new_fields {
                existing_map.insert(key, value);
            }
            serde_json::Value::Object(existing_map)
        }
        _ => {
            LOG.warn("global_config: existing config is not a JSON object — skipping merge");
            serde_json::Value::Object(new_fields)
        }
    }
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
fn load_orchard_json_remotes(repo_root: &Path) -> Vec<RemoteConfig> {
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

    // -----------------------------------------------------------------------
    // Config location — dotdir convention tests (issue #424)
    // -----------------------------------------------------------------------

    /// global_config_path() returns a path ending in `.orchard/config.json`.
    #[test]
    fn config_path_ends_with_dotdir_config() {
        let path = global_config_path().expect("home dir must be resolvable in test env");
        let path_str = path.to_string_lossy();
        assert!(
            path_str.ends_with(".orchard/config.json"),
            "expected path ending in .orchard/config.json, got: {path_str}"
        );
    }

    /// global_config_write_path() returns the same path as global_config_path().
    #[test]
    fn write_path_matches_read_path() {
        let read_path = global_config_path().expect("home dir must be resolvable");
        let write_path = global_config_write_path().expect("home dir must be resolvable");
        assert_eq!(
            read_path, write_path,
            "global_config_path and global_config_write_path must return identical paths"
        );
    }

    /// global_config_path() is anchored to the real home dir (not some other base).
    #[test]
    fn config_path_is_inside_home_dir() {
        let home = dirs::home_dir().expect("home dir must be resolvable in test env");
        let path = global_config_path().expect("global_config_path must return Some");
        // The canonical path is <home>/.orchard/config.json
        let expected = home.join(".orchard").join("config.json");
        assert_eq!(
            path, expected,
            "global_config_path must equal {{home}}/.orchard/config.json"
        );
    }

    /// The returned path must NOT contain the legacy XDG component.
    #[test]
    fn config_path_does_not_contain_xdg_config_orchard() {
        let path = global_config_path().expect("home dir must be resolvable");
        let path_str = path.to_string_lossy();
        assert!(
            !path_str.contains(".config/orchard"),
            "path must not reference the legacy .config/orchard location, got: {path_str}"
        );
    }

    /// The returned path must NOT contain the macOS Application Support component.
    #[test]
    fn config_path_does_not_contain_application_support() {
        let path = global_config_path().expect("home dir must be resolvable");
        let path_str = path.to_string_lossy();
        assert!(
            !path_str.contains("Library/Application Support"),
            "path must not reference macOS Application Support, got: {path_str}"
        );
    }

    /// XDG_CONFIG_HOME is structurally irrelevant: the implementation uses
    /// `dirs::home_dir()` rather than `dirs::config_dir()`, so XDG is never
    /// consulted. We avoid mutating the process env (Rust 2024 made
    /// `std::env::set_var` unsafe and parallel test runs would race anyway)
    /// and instead inspect the resolved path directly.
    #[test]
    fn config_path_ignores_xdg_config_home() {
        let path = global_config_path().expect("home dir must be resolvable");
        let path_str = path.to_string_lossy();
        assert!(
            !path_str.contains(".config/orchard"),
            "path must not reference .config/orchard even when XDG_CONFIG_HOME is set elsewhere, got: {path_str}"
        );
        let xdg_value = std::env::var("XDG_CONFIG_HOME").unwrap_or_default();
        if !xdg_value.is_empty() {
            assert!(
                !path_str.contains(&xdg_value),
                "path must not include the active XDG_CONFIG_HOME value {xdg_value}, got: {path_str}"
            );
        }
    }

    // -----------------------------------------------------------------------
    // Migration hint tests (issue #424, task 3 of 8)
    // -----------------------------------------------------------------------

    /// `legacy_config_exists_at` returns false when the legacy file is absent.
    #[test]
    fn legacy_config_exists_at_returns_false_when_absent() {
        let dir = tempdir().unwrap();
        assert!(
            !legacy_config_exists_at(dir.path()),
            "expected false when .config/orchard/config.json does not exist"
        );
    }

    /// `legacy_config_exists_at` returns true when the legacy file is present.
    #[test]
    fn legacy_config_exists_at_returns_true_when_present() {
        let dir = tempdir().unwrap();
        let legacy = dir
            .path()
            .join(".config")
            .join("orchard")
            .join("config.json");
        std::fs::create_dir_all(legacy.parent().unwrap()).unwrap();
        std::fs::write(&legacy, b"{}").unwrap();
        assert!(
            legacy_config_exists_at(dir.path()),
            "expected true when .config/orchard/config.json exists"
        );
    }

    /// `migration_hint_message` contains all required hint substrings.
    #[test]
    fn migration_hint_message_contains_required_substrings() {
        let msg = migration_hint_message();
        assert!(
            msg.contains("Found legacy config at ~/.config/orchard/config.json"),
            "hint must name the legacy path, got: {msg}"
        );
        assert!(
            msg.contains("mv ~/.config/orchard ~/.orchard"),
            "hint must include the migration command, got: {msg}"
        );
        assert!(
            msg.contains("~/.orchard/config.json"),
            "hint must point at the new canonical path, got: {msg}"
        );
    }

    // -----------------------------------------------------------------------
    // save_to_path preserves unknown fields (issue #432)
    // -----------------------------------------------------------------------

    /// Regression test for issue #432: `save_to_path` must not clobber unknown
    /// top-level fields that are present in the on-disk config but not modeled
    /// by `GlobalConfig`.
    ///
    /// The current implementation serializes the struct directly, which drops
    /// any fields not in the struct (e.g. a future `peers` array). This test
    /// is intentionally written to FAIL against the pre-fix implementation so
    /// that the fix can be verified.
    #[test]
    fn save_to_path_preserves_unknown_top_level_peers_field() {
        let dir = tempdir().unwrap();
        let path = dir.path().join("config.json");

        // Write a config that contains both modeled fields AND an unknown `peers`
        // top-level array that the GlobalConfig struct does not model.
        let initial_json = r#"{
  "repos": [],
  "terminal_app": "com.apple.Terminal",
  "peers": [{"name": "boxd-vm", "address": "user@vm.example", "tls": true}]
}"#;
        std::fs::write(&path, initial_json).unwrap();

        // Call save_to_path with a mutated terminal_app (everything else default).
        let cfg = GlobalConfig {
            repos: vec![],
            terminal_app: "com.googlecode.iterm2".to_string(),
            tmux_sessions: vec![],
            chat_target: None,
            watch: WatchConfig::default(),
            ci_gate_patterns: default_ci_gate_patterns(),
        };
        save_to_path(&cfg, &path).expect("save_to_path must not fail");

        // Read back the raw JSON as an untyped Value to inspect what was written.
        let written = std::fs::read_to_string(&path).unwrap();
        let value: serde_json::Value = serde_json::from_str(&written).unwrap();

        // The struct mutation must have taken effect.
        assert_eq!(
            value["terminal_app"].as_str(),
            Some("com.googlecode.iterm2"),
            "terminal_app must reflect the saved GlobalConfig value"
        );

        // The unknown `peers` array must still be present — save_to_path must
        // not clobber fields it doesn't know about (issue #432).
        let peers = value["peers"].as_array().expect(
            "peers array must be preserved after save_to_path (issue #432: unknown fields are being dropped)"
        );
        assert_eq!(peers.len(), 1, "peers array must retain its single entry");
        assert_eq!(
            peers[0]["name"].as_str(),
            Some("boxd-vm"),
            "peers[0].name must be 'boxd-vm'"
        );
    }

    // -----------------------------------------------------------------------
    // Additional save_to_path scenario tests (issue #432, BDD feature coverage)
    // -----------------------------------------------------------------------

    #[test]
    fn save_to_path_preserves_multiple_unknown_top_level_keys() {
        let dir = tempdir().unwrap();
        let path = write_config(
            dir.path(),
            r#"{
            "repos": [],
            "terminal_app": "com.apple.Terminal",
            "peers": [{"name": "boxd-vm"}],
            "watch_observability": {"enabled": true},
            "federation_tls_pin": "sha256:abc123"
        }"#,
        );
        let cfg = GlobalConfig {
            terminal_app: "com.googlecode.iterm2".to_string(),
            ..GlobalConfig::default()
        };
        save_to_path(&cfg, &path).unwrap();
        let v: serde_json::Value = serde_json::from_slice(&std::fs::read(&path).unwrap()).unwrap();
        assert!(v["peers"].is_array(), "peers must be preserved");
        assert!(
            v["watch_observability"].is_object(),
            "watch_observability must be preserved"
        );
        assert_eq!(
            v["federation_tls_pin"].as_str(),
            Some("sha256:abc123"),
            "federation_tls_pin must be preserved"
        );
    }

    #[test]
    fn save_to_path_preserves_nested_values_inside_unknown_top_level_key() {
        let dir = tempdir().unwrap();
        let path = write_config(
            dir.path(),
            r#"{
            "repos": [],
            "peers": [{"name": "boxd-vm", "address": "user@vm.example", "tls": true, "metadata": {"region": "us-east-1"}}]
        }"#,
        );
        let cfg = GlobalConfig::default();
        save_to_path(&cfg, &path).unwrap();
        let v: serde_json::Value = serde_json::from_slice(&std::fs::read(&path).unwrap()).unwrap();
        let peer = &v["peers"][0];
        assert_eq!(peer["name"].as_str(), Some("boxd-vm"));
        assert_eq!(peer["address"].as_str(), Some("user@vm.example"));
        assert_eq!(peer["tls"].as_bool(), Some(true));
        assert_eq!(peer["metadata"]["region"].as_str(), Some("us-east-1"));
    }

    #[test]
    fn save_to_path_overwrites_known_keys_present_in_struct() {
        let dir = tempdir().unwrap();
        let path = write_config(
            dir.path(),
            r#"{
            "repos": [],
            "terminal_app": "old.app",
            "peers": [{"name": "boxd-vm"}]
        }"#,
        );
        let cfg = GlobalConfig {
            terminal_app: "new.app".to_string(),
            ..GlobalConfig::default()
        };
        save_to_path(&cfg, &path).unwrap();
        let v: serde_json::Value = serde_json::from_slice(&std::fs::read(&path).unwrap()).unwrap();
        assert_eq!(
            v["terminal_app"].as_str(),
            Some("new.app"),
            "struct must win for known keys"
        );
        assert!(v["peers"].is_array(), "unknown peers must still exist");
    }

    #[test]
    fn save_to_path_writes_struct_when_file_absent() {
        let dir = tempdir().unwrap();
        let path = dir.path().join("config.json");
        let cfg = GlobalConfig {
            terminal_app: "com.mitchellh.ghostty".to_string(),
            ..GlobalConfig::default()
        };
        save_to_path(&cfg, &path).unwrap();
        let v: serde_json::Value = serde_json::from_slice(&std::fs::read(&path).unwrap()).unwrap();
        assert_eq!(v["terminal_app"].as_str(), Some("com.mitchellh.ghostty"));
        // No spurious keys beyond what GlobalConfig serializes.
        let known_keys = [
            "repos",
            "terminal_app",
            "tmux_sessions",
            "chat_target",
            "watch",
            "ci_gate_patterns",
        ];
        let obj = v.as_object().unwrap();
        for key in obj.keys() {
            assert!(
                known_keys.contains(&key.as_str()),
                "unexpected key on disk: {key}"
            );
        }
    }

    #[test]
    fn save_to_path_falls_back_to_struct_write_on_malformed_existing_file() {
        let dir = tempdir().unwrap();
        let path = write_config(dir.path(), "not valid {");
        let cfg = GlobalConfig::default();
        let result = save_to_path(&cfg, &path);
        assert!(
            result.is_ok(),
            "save must succeed even when existing file is malformed"
        );
        let v: serde_json::Value = serde_json::from_slice(&std::fs::read(&path).unwrap()).unwrap();
        assert!(
            v.is_object(),
            "file must contain valid JSON after fallback write"
        );
    }

    #[test]
    fn save_to_path_preserves_unknown_keys_with_edge_case_values() {
        let dir = tempdir().unwrap();
        let path = write_config(
            dir.path(),
            r#"{
            "repos": [],
            "peers": [],
            "watch_observability": null,
            "feature_flag_x": false,
            "empty_object_field": {}
        }"#,
        );
        let cfg = GlobalConfig::default();
        save_to_path(&cfg, &path).unwrap();
        let v: serde_json::Value = serde_json::from_slice(&std::fs::read(&path).unwrap()).unwrap();
        assert_eq!(
            v["peers"],
            serde_json::Value::Array(vec![]),
            "empty array must be preserved"
        );
        assert_eq!(
            v["watch_observability"],
            serde_json::Value::Null,
            "null must be preserved"
        );
        assert_eq!(
            v["feature_flag_x"],
            serde_json::Value::Bool(false),
            "false must be preserved"
        );
        assert_eq!(
            v["empty_object_field"],
            serde_json::json!({}),
            "empty object must be preserved"
        );
    }

    #[test]
    fn save_to_path_shallow_merge_drops_nested_unknown_under_known_key() {
        let dir = tempdir().unwrap();
        let path = write_config(
            dir.path(),
            r#"{
            "repos": [],
            "watch": {"local_poll_secs": 10, "full_poll_secs": 60, "threshold_cooldown_secs": 300, "notifications": true, "experimental_flag": true}
        }"#,
        );
        let cfg = GlobalConfig::default();
        save_to_path(&cfg, &path).unwrap();
        let v: serde_json::Value = serde_json::from_slice(&std::fs::read(&path).unwrap()).unwrap();
        // Shallow merge: `watch` is overwritten by the struct's serialization.
        // The unknown nested key `experimental_flag` is dropped — by design.
        assert!(
            v["watch"]["experimental_flag"].is_null(),
            "nested unknown under known key must be dropped (shallow merge)"
        );
    }

    #[test]
    fn save_to_path_remains_atomic() {
        let dir = tempdir().unwrap();
        let path = write_config(
            dir.path(),
            r#"{"repos": [], "peers": [{"name": "boxd-vm"}]}"#,
        );
        let tmp = path.with_extension("json.tmp");
        let cfg = GlobalConfig::default();
        save_to_path(&cfg, &path).unwrap();
        // After a successful save the .tmp staging file must not remain.
        assert!(
            !tmp.exists(),
            ".tmp file must not remain after successful atomic save"
        );
        // And the final file must be valid JSON.
        let v: serde_json::Value = serde_json::from_slice(&std::fs::read(&path).unwrap()).unwrap();
        assert!(v["peers"].is_array(), "peers must be present in final file");
    }

    #[test]
    fn save_to_path_struct_only_round_trip_unchanged_when_no_unknown_keys() {
        let dir = tempdir().unwrap();
        let path = dir.path().join("config.json");
        let cfg = GlobalConfig {
            repos: vec![RepoConfig {
                slug: "owner/repo".to_string(),
                path: "/ws/repo".to_string(),
                remotes: vec![],
            }],
            terminal_app: "org.alacritty".to_string(),
            tmux_sessions: vec![],
            chat_target: Some("orchardist".to_string()),
            watch: WatchConfig {
                local_poll_secs: 7,
                ..WatchConfig::default()
            },
            ci_gate_patterns: vec!["custom-gate".to_string()],
        };
        save_to_path(&cfg, &path).unwrap();
        save_to_path(&cfg, &path).unwrap(); // Second save: no unknown keys to merge.
        let v: serde_json::Value = serde_json::from_slice(&std::fs::read(&path).unwrap()).unwrap();
        assert_eq!(v["terminal_app"].as_str(), Some("org.alacritty"));
        assert_eq!(v["chat_target"].as_str(), Some("orchardist"));
        assert_eq!(v["watch"]["local_poll_secs"].as_u64(), Some(7));
        assert_eq!(v["ci_gate_patterns"][0].as_str(), Some("custom-gate"));
        assert_eq!(v["repos"][0]["slug"].as_str(), Some("owner/repo"));
        // No extra keys beyond the struct fields.
        let known_keys = [
            "repos",
            "terminal_app",
            "tmux_sessions",
            "chat_target",
            "watch",
            "ci_gate_patterns",
        ];
        for key in v.as_object().unwrap().keys() {
            assert!(
                known_keys.contains(&key.as_str()),
                "unexpected key after round-trip: {key}"
            );
        }
    }

    #[test]
    fn save_to_path_preserves_peers_across_back_to_back_saves() {
        let dir = tempdir().unwrap();
        let path = write_config(
            dir.path(),
            r#"{"repos": [], "peers": [{"name": "boxd-vm"}]}"#,
        );
        let cfg1 = GlobalConfig {
            terminal_app: "org.alacritty".to_string(),
            ..GlobalConfig::default()
        };
        save_to_path(&cfg1, &path).unwrap();
        let cfg2 = GlobalConfig {
            chat_target: Some("orchardist".to_string()),
            ..GlobalConfig::default()
        };
        save_to_path(&cfg2, &path).unwrap();
        let v: serde_json::Value = serde_json::from_slice(&std::fs::read(&path).unwrap()).unwrap();
        let peers = v["peers"]
            .as_array()
            .expect("peers must survive back-to-back saves");
        assert_eq!(peers.len(), 1);
        assert_eq!(peers[0]["name"].as_str(), Some("boxd-vm"));
    }

    #[test]
    fn save_to_path_preserves_peers_when_auto_registering_new_repo() {
        let dir = tempdir().unwrap();
        let path = write_config(
            dir.path(),
            r#"{"repos": [], "peers": [{"name": "boxd-vm"}]}"#,
        );
        let mut cfg = GlobalConfig::default();
        append_repo_if_new(&mut cfg, "/workspace/my-project", "acme/my-project", vec![]);
        save_to_path(&cfg, &path).unwrap();
        let v: serde_json::Value = serde_json::from_slice(&std::fs::read(&path).unwrap()).unwrap();
        assert!(
            v["repos"].as_array().map(|a| a.len() == 1).unwrap_or(false),
            "new repo must be in repos[]"
        );
        let peers = v["peers"]
            .as_array()
            .expect("peers must survive append_repo_if_new + save");
        assert_eq!(peers.len(), 1);
        assert_eq!(peers[0]["name"].as_str(), Some("boxd-vm"));
    }

    /// Legacy path is never loaded as a fallback (issue #424, scenario line 165-169).
    ///
    /// Simulates the situation where `~/.config/orchard/config.json` exists but
    /// `~/.orchard/config.json` does not. Verifies that `load_global_config`
    /// does NOT return data from the legacy file.
    ///
    /// Note: this test cannot redirect `dirs::home_dir()` at runtime, so it
    /// tests the property indirectly: `global_config_path()` never points at
    /// `.config/orchard`, so `load_from_path` is never called with a legacy
    /// path by the production code path.
    #[test]
    fn legacy_path_is_never_loaded_as_fallback() {
        // global_config_path() is the sole gating function. If it never
        // returns a path containing ".config/orchard", the legacy file can
        // never be opened by load_global_config.
        let path = global_config_path().expect("home dir must be resolvable");
        let path_str = path.to_string_lossy();
        assert!(
            !path_str.contains(".config/orchard"),
            "global_config_path must not point at legacy path .config/orchard, got: {path_str}"
        );
        // Additionally confirm the path ends with the new dotdir location.
        assert!(
            path_str.ends_with(".orchard/config.json"),
            "global_config_path must end with .orchard/config.json, got: {path_str}"
        );
    }
}
