//! Per-host orchard snapshot cache.
//!
//! After a successful `OrchardProxyAdapter` round-trip, the raw `JsonOutput`
//! is persisted to `~/.cache/orchard/{safe_host}_orchard_snapshot.json`.
//! On TUI cold start, these snapshots are read back before any SSH occurs,
//! so remote rows appear immediately in the first render.
//!
//! Snapshots with a `version` outside `SUPPORTED_JSON_OUTPUT_VERSIONS` are
//! treated as absent and a diagnostic is written to `events.jsonl`.
//!
//! # Integration points
//!
//! - **Writer**: `OrchardProxyAdapter::fetch_snapshot` calls
//!   [`write_snapshot`] on success (best-effort — write failure is logged but
//!   does not fail the call).
//! - **Reader**: [`load_cached_snapshots`] is called at TUI cold start and
//!   passes the resulting `Vec<(String, JsonOutput)>` to
//!   `merge_remote::build_state_with_snapshots`.

use std::path::{Path, PathBuf};

use serde_json::Value;

use crate::cache::cache_dir;
use crate::events::log_event;
use crate::global_config::GlobalConfig;
use crate::json_output::{JsonOutput, check_json_output_version};
use crate::remote_adapter::RemoteKind;

// ---------------------------------------------------------------------------
// Path helpers
// ---------------------------------------------------------------------------

/// Returns the snapshot cache path for `host` under the default cache directory.
///
/// `@` and `.` in the host string are replaced with `_` to produce a
/// filesystem-safe filename — mirroring the `tmux_cache_path` convention.
///
/// # Examples
///
/// ```
/// use orchard::orchard_snapshot::orchard_snapshot_path;
/// let p = orchard_snapshot_path("boxd@vm.boxd.sh");
/// assert!(p.to_string_lossy().ends_with("boxd_vm_boxd_sh_orchard_snapshot.json"));
/// ```
pub fn orchard_snapshot_path(host: &str) -> PathBuf {
    orchard_snapshot_path_in(host, &cache_dir())
}

/// Returns the snapshot cache path for `host` under a custom `cache_dir`.
///
/// Intended for tests that redirect writes to a [`tempfile::TempDir`].
///
/// # Examples
///
/// ```
/// use std::path::Path;
/// use orchard::orchard_snapshot::orchard_snapshot_path_in;
/// let p = orchard_snapshot_path_in("vm.boxd.sh", Path::new("/tmp/cache"));
/// assert_eq!(p, Path::new("/tmp/cache/vm_boxd_sh_orchard_snapshot.json"));
/// ```
pub fn orchard_snapshot_path_in(host: &str, cache_dir: &Path) -> PathBuf {
    let safe = host.replace(['@', '.'], "_");
    cache_dir.join(format!("{}_orchard_snapshot.json", safe))
}

// ---------------------------------------------------------------------------
// Write
// ---------------------------------------------------------------------------

/// Writes `snapshot` atomically to `{safe_host}_orchard_snapshot.json` under
/// the default cache directory.
///
/// Uses write-to-tmp-then-rename for crash safety. Logs a
/// `remote_snapshot.written` event on success.
///
/// Returns an error only if the write itself fails; callers should treat this
/// as best-effort and log rather than propagate.
pub fn write_snapshot(host: &str, snapshot: &JsonOutput) -> anyhow::Result<()> {
    write_snapshot_to(host, snapshot, &cache_dir())
}

/// Like [`write_snapshot`] but writes under `cache_dir` (testing variant).
pub fn write_snapshot_to(
    host: &str,
    snapshot: &JsonOutput,
    cache_dir: &Path,
) -> anyhow::Result<()> {
    use anyhow::Context as _;

    let path = orchard_snapshot_path_in(host, cache_dir);
    let dir = path.parent().context("snapshot path has no parent")?;
    std::fs::create_dir_all(dir).context("create snapshot cache directory")?;

    let json = serde_json::to_string_pretty(snapshot).context("serialize snapshot")?;
    let tmp_path = path.with_extension("json.tmp");
    std::fs::write(&tmp_path, &json).context("write snapshot .tmp file")?;
    restrict_snapshot_permissions(&tmp_path);
    std::fs::rename(&tmp_path, &path).context("rename snapshot .tmp to final")?;

    log_event(
        "remote_snapshot.written",
        &[
            ("host", Value::String(host.to_string())),
            ("version", Value::Number(snapshot.version.into())),
        ],
    );

    Ok(())
}

/// Sets 0600 permissions on the snapshot file. No-op on non-Unix platforms.
#[cfg(unix)]
fn restrict_snapshot_permissions(path: &Path) {
    use std::os::unix::fs::PermissionsExt;
    let _ = std::fs::set_permissions(path, std::fs::Permissions::from_mode(0o600));
}

#[cfg(not(unix))]
fn restrict_snapshot_permissions(_path: &Path) {}

// ---------------------------------------------------------------------------
// Read
// ---------------------------------------------------------------------------

/// Reads `{safe_host}_orchard_snapshot.json` from the default cache directory.
///
/// Returns `None` if the file is missing, unparseable, or contains a `version`
/// outside [`crate::json_output::SUPPORTED_JSON_OUTPUT_VERSIONS`]. Version
/// mismatches are logged via `events.jsonl` as `remote_snapshot.invalidated`
/// so operators can observe skew.
pub fn read_snapshot(host: &str) -> Option<JsonOutput> {
    read_snapshot_from(host, &cache_dir())
}

/// Like [`read_snapshot`] but reads under `cache_dir` (testing variant).
pub fn read_snapshot_from(host: &str, cache_dir: &Path) -> Option<JsonOutput> {
    let path = orchard_snapshot_path_in(host, cache_dir);
    let contents = std::fs::read_to_string(&path).ok()?;
    let snapshot: JsonOutput = serde_json::from_str(&contents).ok()?;

    if let Err(e) = check_json_output_version(snapshot.version) {
        log_event(
            "remote_snapshot.invalidated",
            &[
                ("host", Value::String(host.to_string())),
                ("version", Value::Number(snapshot.version.into())),
                ("reason", Value::String(format!("version skew: {e}"))),
            ],
        );
        return None;
    }

    Some(snapshot)
}

// ---------------------------------------------------------------------------
// Cold-start loader
// ---------------------------------------------------------------------------

/// Loads all cached orchard snapshots for `OrchardProxy` remotes in `config`.
///
/// Iterates every `RemoteConfig` across all repos in the global config, filters
/// for `kind == OrchardProxy`, and calls [`read_snapshot`] for each unique host.
/// Invalid or missing snapshots are silently skipped (they will be populated on
/// the first successful SSH round-trip).
///
/// Returns a `Vec<(host, JsonOutput)>` suitable for passing to
/// [`merge_remote::build_state_with_snapshots`].
pub fn load_cached_snapshots(config: &GlobalConfig) -> Vec<(String, JsonOutput)> {
    load_cached_snapshots_from(config, &cache_dir())
}

/// Like [`load_cached_snapshots`] but reads under `cache_dir` (testing variant).
pub fn load_cached_snapshots_from(
    config: &GlobalConfig,
    cache_dir: &Path,
) -> Vec<(String, JsonOutput)> {
    let mut results: Vec<(String, JsonOutput)> = Vec::new();
    let mut seen_hosts: std::collections::HashSet<String> = std::collections::HashSet::new();

    for repo in &config.repos {
        for remote in &repo.remotes {
            if remote.kind == RemoteKind::OrchardProxy
                && seen_hosts.insert(remote.host.clone())
                && let Some(snapshot) = read_snapshot_from(&remote.host, cache_dir)
            {
                results.push((remote.host.clone(), snapshot));
            }
        }
    }

    // Also load snapshots for transitively-discovered hosts from topology.
    // These hosts are not in config.repos.remotes but were discovered on a
    // prior successful walk and persisted in federation_topology.json.
    if let Some(topology) = crate::federation_topology::read_topology_from(cache_dir) {
        let transitive_hosts =
            crate::federation_topology::transitive_hosts_from_topology(&topology);
        for host in transitive_hosts {
            if seen_hosts.insert(host.clone()) {
                if let Some(snapshot) = read_snapshot_from(&host, cache_dir) {
                    results.push((host, snapshot));
                }
            }
        }
    }

    results
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

#[cfg(test)]
mod tests {
    use super::*;
    use std::collections::HashMap;
    use tempfile::TempDir;

    use crate::json_output::{JsonOutput, JsonRepo, JsonWorktree};

    fn minimal_json_output(version: u32) -> JsonOutput {
        JsonOutput {
            version,
            tmux_sessions: vec![],
            repos: vec![],
            hosts: HashMap::new(),
        }
    }

    fn json_output_with_worktree(version: u32, path: &str, branch: &str) -> JsonOutput {
        JsonOutput {
            version,
            tmux_sessions: vec![],
            repos: vec![JsonRepo {
                slug: "owner/repo".to_string(),
                default_branch: None,
                main_ci_state: None,
                worktrees: vec![JsonWorktree {
                    path: path.to_string(),
                    branch: branch.to_string(),
                    host: None,
                    layout: "bare".to_string(),
                    ahead_behind: None,
                    last_commit_at: None,
                    issue: None,
                    pr: None,
                    sessions: vec![],
                    display_group: "other".to_string(),
                    status: "ready".to_string(),
                    status_glyph: "\u{1f7e2}".to_string(),
                    is_main_worktree: false,
                    last_activity_at: None,
                }],
            }],
            hosts: HashMap::new(),
        }
    }

    // ---- AC8: write snapshot ------------------------------------------------

    /// AC8 — Successful refresh writes `{host}_orchard_snapshot.json`.
    ///
    /// After a write, the file exists, is parseable, contains the version field,
    /// and the full JsonOutput survives the round-trip.
    #[test]
    fn ac8_write_snapshot_creates_file_with_version() {
        let dir = TempDir::new().unwrap();
        let snapshot = minimal_json_output(6);

        write_snapshot_to("vm.boxd.sh", &snapshot, dir.path()).unwrap();

        let path = orchard_snapshot_path_in("vm.boxd.sh", dir.path());
        assert!(path.exists(), "snapshot file must exist after write");

        let contents = std::fs::read_to_string(&path).unwrap();
        let parsed: serde_json::Value = serde_json::from_str(&contents).unwrap();
        assert!(
            parsed.get("version").is_some(),
            "snapshot file must include the `version` field"
        );
        assert_eq!(
            parsed["version"].as_u64().unwrap(),
            6,
            "version must round-trip as 6"
        );
    }

    /// AC8 — Filename uses safe host (@ and . replaced with _).
    #[test]
    fn ac8_write_snapshot_uses_safe_hostname() {
        let dir = TempDir::new().unwrap();
        let snapshot = minimal_json_output(6);

        write_snapshot_to("boxd@vm.boxd.sh", &snapshot, dir.path()).unwrap();

        let path = orchard_snapshot_path_in("boxd@vm.boxd.sh", dir.path());
        assert!(
            path.to_string_lossy()
                .ends_with("boxd_vm_boxd_sh_orchard_snapshot.json"),
            "filename must replace @ and . with _"
        );
        assert!(path.exists());
    }

    /// AC8 — Write is atomic: final file is always a complete JSON object (no
    /// partial content). Simulated by checking no .tmp file is left behind.
    #[test]
    fn ac8_write_is_atomic_no_tmp_file_left() {
        let dir = TempDir::new().unwrap();
        let snapshot = minimal_json_output(6);

        write_snapshot_to("vm.boxd.sh", &snapshot, dir.path()).unwrap();

        let tmp = dir.path().join("vm_boxd_sh_orchard_snapshot.json.tmp");
        assert!(
            !tmp.exists(),
            ".tmp file must be removed after atomic rename"
        );
    }

    // ---- AC8: read snapshot -------------------------------------------------

    /// AC8 — TUI cold start reads snapshot for instant render.
    ///
    /// `read_snapshot_from` returns `Some(JsonOutput)` when a valid snapshot exists.
    #[test]
    fn ac8_read_snapshot_returns_some_for_valid_file() {
        let dir = TempDir::new().unwrap();
        let snapshot = json_output_with_worktree(6, "/remote/main", "main");

        write_snapshot_to("vm.boxd.sh", &snapshot, dir.path()).unwrap();

        let loaded = read_snapshot_from("vm.boxd.sh", dir.path());
        assert!(loaded.is_some(), "must return Some for a valid snapshot");
        let loaded = loaded.unwrap();
        assert_eq!(loaded.repos.len(), 1);
        assert_eq!(loaded.repos[0].worktrees[0].branch, "main");
    }

    /// AC8 — `read_snapshot_from` returns `None` when no file exists.
    #[test]
    fn ac8_read_snapshot_returns_none_for_missing_file() {
        let dir = TempDir::new().unwrap();
        let result = read_snapshot_from("nonexistent.host", dir.path());
        assert!(result.is_none(), "missing file must return None");
    }

    // ---- AC8: version invalidation ------------------------------------------

    /// AC8 — Snapshot with unsupported version is treated as absent.
    ///
    /// A file with `"version": 99` must return `None` (not merged into state).
    #[test]
    fn ac8_snapshot_with_bad_version_returns_none() {
        let dir = TempDir::new().unwrap();
        // Write a raw JSON file with an unsupported version bypassing write_snapshot's
        // version check (write_snapshot does not validate version on write).
        let bad_json = r#"{"version":99,"tmuxSessions":[],"repos":[],"hosts":{}}"#;
        let path = orchard_snapshot_path_in("vm.boxd.sh", dir.path());
        std::fs::create_dir_all(dir.path()).unwrap();
        std::fs::write(&path, bad_json).unwrap();

        let result = read_snapshot_from("vm.boxd.sh", dir.path());
        assert!(
            result.is_none(),
            "snapshot with unsupported version 99 must be treated as absent"
        );
    }

    /// AC8 — Version mismatch writes a diagnostic to events.jsonl.
    ///
    /// We can't easily assert on the live events file in tests, but we verify
    /// the code path executes without panic and produces None.
    #[test]
    fn ac8_version_mismatch_does_not_panic() {
        let dir = TempDir::new().unwrap();
        let bad_json = r#"{"version":99,"tmuxSessions":[],"repos":[],"hosts":{}}"#;
        let path = orchard_snapshot_path_in("vm.boxd.sh", dir.path());
        std::fs::create_dir_all(dir.path()).unwrap();
        std::fs::write(&path, bad_json).unwrap();

        // Must not panic — log_event is best-effort.
        let result = read_snapshot_from("vm.boxd.sh", dir.path());
        assert!(result.is_none());
    }

    // ---- AC8: overwrite --------------------------------------------------------

    /// AC8 (integration) — Snapshot refresh overwrites the prior file.
    ///
    /// Prior file has 1 worktree; fresh write has 2 worktrees.
    /// After the write the file must contain exactly 2 worktrees.
    #[test]
    fn ac8_overwrite_replaces_prior_snapshot() {
        let dir = TempDir::new().unwrap();

        // Write initial snapshot with 1 worktree.
        let first = json_output_with_worktree(6, "/remote/wt1", "issue100/branch");
        write_snapshot_to("vm.boxd.sh", &first, dir.path()).unwrap();

        // Write second snapshot with 2 worktrees.
        let second = JsonOutput {
            version: 6,
            tmux_sessions: vec![],
            repos: vec![JsonRepo {
                slug: "owner/repo".to_string(),
                default_branch: None,
                main_ci_state: None,
                worktrees: vec![
                    JsonWorktree {
                        path: "/remote/wt1".to_string(),
                        branch: "issue100/branch".to_string(),
                        host: None,
                        layout: "bare".to_string(),
                        ahead_behind: None,
                        last_commit_at: None,
                        issue: None,
                        pr: None,
                        sessions: vec![],
                        display_group: "other".to_string(),
                        status: "ready".to_string(),
                        status_glyph: "\u{1f7e2}".to_string(),
                        is_main_worktree: false,
                        last_activity_at: None,
                    },
                    JsonWorktree {
                        path: "/remote/wt2".to_string(),
                        branch: "issue101/branch".to_string(),
                        host: None,
                        layout: "bare".to_string(),
                        ahead_behind: None,
                        last_commit_at: None,
                        issue: None,
                        pr: None,
                        sessions: vec![],
                        display_group: "other".to_string(),
                        status: "ready".to_string(),
                        status_glyph: "\u{1f7e2}".to_string(),
                        is_main_worktree: false,
                        last_activity_at: None,
                    },
                ],
            }],
            hosts: HashMap::new(),
        };
        write_snapshot_to("vm.boxd.sh", &second, dir.path()).unwrap();

        let loaded = read_snapshot_from("vm.boxd.sh", dir.path()).unwrap();
        assert_eq!(
            loaded.repos[0].worktrees.len(),
            2,
            "second write must replace the prior file with exactly 2 worktrees"
        );
    }

    // ---- load_cached_snapshots ----------------------------------------------

    /// `load_cached_snapshots_from` returns snapshots for OrchardProxy remotes.
    #[test]
    fn load_cached_snapshots_returns_proxy_snapshots() {
        use crate::global_config::{GlobalConfig, RemoteConfig, RepoConfig};
        use crate::remote_adapter::RemoteKind;

        let dir = TempDir::new().unwrap();
        let snapshot = json_output_with_worktree(6, "/remote/main", "main");
        write_snapshot_to("vm.boxd.sh", &snapshot, dir.path()).unwrap();

        let config = GlobalConfig {
            repos: vec![RepoConfig {
                slug: "owner/repo".to_string(),
                path: "/local/repo".to_string(),
                remotes: vec![RemoteConfig {
                    name: "vm".to_string(),
                    host: "vm.boxd.sh".to_string(),
                    path: "/remote/repo".to_string(),
                    shell: "ssh".to_string(),
                    kind: RemoteKind::OrchardProxy,
                    allow_transitive: false,
                }],
            }],
            ..GlobalConfig::default()
        };

        let snapshots = load_cached_snapshots_from(&config, dir.path());
        assert_eq!(snapshots.len(), 1);
        assert_eq!(snapshots[0].0, "vm.boxd.sh");
        assert_eq!(snapshots[0].1.repos[0].worktrees[0].branch, "main");
    }

    /// `load_cached_snapshots_from` skips non-OrchardProxy remotes.
    #[test]
    fn load_cached_snapshots_skips_non_proxy_remotes() {
        use crate::global_config::{GlobalConfig, RemoteConfig, RepoConfig};
        use crate::remote_adapter::RemoteKind;

        let dir = TempDir::new().unwrap();

        // Write a snapshot for a Remmy remote — it must NOT be loaded.
        let snapshot = json_output_with_worktree(6, "/remote/main", "main");
        write_snapshot_to("remmy.host", &snapshot, dir.path()).unwrap();

        let config = GlobalConfig {
            repos: vec![RepoConfig {
                slug: "owner/repo".to_string(),
                path: "/local/repo".to_string(),
                remotes: vec![RemoteConfig {
                    name: "remmy".to_string(),
                    host: "remmy.host".to_string(),
                    path: "/remote/repo".to_string(),
                    shell: "ssh".to_string(),
                    kind: RemoteKind::Remmy,
                    allow_transitive: false,
                }],
            }],
            ..GlobalConfig::default()
        };

        let snapshots = load_cached_snapshots_from(&config, dir.path());
        assert!(
            snapshots.is_empty(),
            "Remmy remotes must not have their snapshots loaded"
        );
    }

    /// `load_cached_snapshots_from` deduplicates by host.
    #[test]
    fn load_cached_snapshots_deduplicates_same_host() {
        use crate::global_config::{GlobalConfig, RemoteConfig, RepoConfig};
        use crate::remote_adapter::RemoteKind;

        let dir = TempDir::new().unwrap();
        let snapshot = minimal_json_output(6);
        write_snapshot_to("vm.boxd.sh", &snapshot, dir.path()).unwrap();

        // Two repos both reference the same OrchardProxy host.
        let config = GlobalConfig {
            repos: vec![
                RepoConfig {
                    slug: "owner/repo1".to_string(),
                    path: "/local/repo1".to_string(),
                    remotes: vec![RemoteConfig {
                        name: "vm".to_string(),
                        host: "vm.boxd.sh".to_string(),
                        path: "/remote/repo1".to_string(),
                        shell: "ssh".to_string(),
                        kind: RemoteKind::OrchardProxy,
                        allow_transitive: false,
                    }],
                },
                RepoConfig {
                    slug: "owner/repo2".to_string(),
                    path: "/local/repo2".to_string(),
                    remotes: vec![RemoteConfig {
                        name: "vm".to_string(),
                        host: "vm.boxd.sh".to_string(),
                        path: "/remote/repo2".to_string(),
                        shell: "ssh".to_string(),
                        kind: RemoteKind::OrchardProxy,
                        allow_transitive: false,
                    }],
                },
            ],
            ..GlobalConfig::default()
        };

        let snapshots = load_cached_snapshots_from(&config, dir.path());
        assert_eq!(
            snapshots.len(),
            1,
            "same host across two repos must produce exactly one snapshot entry"
        );
    }

    // ---- path helper -------------------------------------------------------

    #[test]
    fn orchard_snapshot_path_in_safe_hostname() {
        let p = orchard_snapshot_path_in("boxd@vm.boxd.sh", Path::new("/tmp/cache"));
        assert_eq!(
            p,
            Path::new("/tmp/cache/boxd_vm_boxd_sh_orchard_snapshot.json")
        );
    }

    #[test]
    fn orchard_snapshot_path_in_no_special_chars() {
        let p = orchard_snapshot_path_in("myhost", Path::new("/tmp/cache"));
        assert_eq!(p, Path::new("/tmp/cache/myhost_orchard_snapshot.json"));
    }

    // ---- load_cached_snapshots: topology extension (Phase 3 / feature:351) ---

    /// feature:351 — load_cached_snapshots consults federation_topology.json
    ///
    /// When the topology lists a transitive host that is not in config.repos.remotes
    /// but has a snapshot file on disk, that snapshot must be loaded.
    #[test]
    fn load_cached_snapshots_includes_transitive_hosts_from_topology() {
        use crate::federation_topology::{
            FederationTopology, TOPOLOGY_CURRENT_VERSION, TopologyEntry, write_topology_to,
        };
        use crate::global_config::{GlobalConfig, RemoteConfig, RepoConfig};

        let dir = TempDir::new().unwrap();

        // Write a snapshot for a directly-configured host.
        let direct_snap = json_output_with_worktree(6, "/remote/direct", "direct-branch");
        write_snapshot_to("direct-host", &direct_snap, dir.path()).unwrap();

        // Write a snapshot for a transitively-discovered host.
        let transitive_snap =
            json_output_with_worktree(6, "/remote/transitive", "transitive-branch");
        write_snapshot_to("transitive-child", &transitive_snap, dir.path()).unwrap();

        // Config only knows about direct-host.
        let config = GlobalConfig {
            repos: vec![RepoConfig {
                slug: "owner/repo".to_string(),
                path: "/local".to_string(),
                remotes: vec![RemoteConfig {
                    name: "direct".to_string(),
                    host: "direct-host".to_string(),
                    path: "/remote".to_string(),
                    shell: "ssh".to_string(),
                    kind: RemoteKind::OrchardProxy,
                    allow_transitive: false,
                }],
            }],
            ..GlobalConfig::default()
        };

        // Write a topology that lists transitive-child.
        let topo = FederationTopology {
            version: TOPOLOGY_CURRENT_VERSION,
            written_at: "2026-01-01T00:00:00+00:00".to_string(),
            entries: vec![TopologyEntry {
                dedup_key: "transitive-child".to_string(),
                discovery_path: vec![
                    "local".to_string(),
                    "direct-host".to_string(),
                    "transitive-child".to_string(),
                ],
                root: "direct-host".to_string(),
                last_seen_at: "2026-01-01T00:00:00+00:00".to_string(),
            }],
        };
        write_topology_to(&topo, dir.path()).unwrap();

        let snapshots = load_cached_snapshots_from(&config, dir.path());

        assert_eq!(
            snapshots.len(),
            2,
            "both direct and transitive must be loaded"
        );
        assert!(
            snapshots.iter().any(|(h, _)| h == "direct-host"),
            "direct host must be present"
        );
        assert!(
            snapshots.iter().any(|(h, _)| h == "transitive-child"),
            "transitive host must be present"
        );
        // Verify the transitive snapshot's content is intact.
        let trans = snapshots
            .iter()
            .find(|(h, _)| h == "transitive-child")
            .unwrap();
        assert_eq!(trans.1.repos[0].worktrees[0].branch, "transitive-branch");
    }
}
