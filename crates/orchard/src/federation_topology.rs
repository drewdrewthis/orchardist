//! Topology persistence for transitive-federation discovery (issue #363, Phase 3).
//!
//! After each successful walker run, the discovered topology is persisted at
//! `~/.cache/orchard/federation_topology.json`.  On cold start,
//! [`load_cached_snapshots_from`](crate::orchard_snapshot::load_cached_snapshots_from)
//! reads this file so transitively-discovered hosts are available before any SSH
//! occurs.
//!
//! # File schema
//!
//! ```json
//! {
//!   "version": 1,
//!   "written_at": "2026-01-02T03:04:05Z",
//!   "entries": [
//!     {
//!       "dedup_key": "boxd@vm.boxd.sh",
//!       "discovery_path": ["local", "boxd", "scenario-voice-agents.boxd.sh"],
//!       "root": "boxd",
//!       "last_seen_at": "2026-01-02T03:04:05Z"
//!     }
//!   ]
//! }
//! ```
//!
//! The `version` field is versioned **independently** of `JsonOutput`. The check
//! is a lower-bound (`version >= TOPOLOGY_MIN_VERSION`), not an exact whitelist.
//!
//! # GC
//!
//! [`gc_orphan_snapshots`] deletes `{safe_host}_orchard_snapshot.json` files
//! whose `dedup_key` is not present in the current topology AND is not a
//! directly-configured remote (per `GlobalConfig`). It is called at the end of
//! each `orchard refresh` run.
//!
//! # Soft 7-day TTL
//!
//! Entries older than 7 days are logged via `remote_snapshot.stale` but kept
//! on disk. They are deleted on the next GC pass if they have become orphans
//! (the host is no longer discovered at all).

use std::path::{Path, PathBuf};

use serde::{Deserialize, Serialize};

use crate::cache::cache_dir;
use crate::events::log_event;

// ---------------------------------------------------------------------------
// Constants
// ---------------------------------------------------------------------------

/// Minimum acceptable version in a `federation_topology.json` file.
///
/// Lower-bound check (`version >= TOPOLOGY_MIN_VERSION`) — not exact-whitelist.
pub const TOPOLOGY_MIN_VERSION: u32 = 1;

/// Current version written by this build.
pub const TOPOLOGY_CURRENT_VERSION: u32 = 1;

/// Soft TTL for topology entries: 7 days.
pub const TOPOLOGY_SOFT_TTL_DAYS: u64 = 7;

// ---------------------------------------------------------------------------
// Schema types
// ---------------------------------------------------------------------------

/// A single entry in the topology file — one per transitively-discovered host.
///
/// Serde silently ignores unknown fields (no `deny_unknown_fields`), so
/// existing `federation_topology.json` files that contain a `"root"` field
/// will still deserialize correctly after the field was removed from this
/// struct.
#[derive(Debug, Clone, Serialize, Deserialize, PartialEq, Eq)]
#[serde(rename_all = "camelCase")]
pub struct TopologyEntry {
    /// `host_dedup_key()` of the discovered host.
    pub dedup_key: String,
    /// Full discovery path from `"local"` to this host.
    ///
    /// Example: `["local", "boxd", "scenario-voice-agents.boxd.sh"]`
    pub discovery_path: Vec<String>,
    /// ISO 8601 UTC timestamp of the last time this host was seen during a
    /// successful walk.
    pub last_seen_at: String,
}

impl TopologyEntry {
    /// Returns the directly-configured root host from which this node was
    /// reached: `discovery_path[1]` (the first element after `"local"`).
    ///
    /// Returns `None` when `discovery_path` has fewer than 2 elements.
    pub fn root(&self) -> Option<&str> {
        self.discovery_path.get(1).map(String::as_str)
    }
}

/// Top-level structure of `federation_topology.json`.
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct FederationTopology {
    /// Schema version. Lower-bound check on read.
    pub version: u32,
    /// ISO 8601 UTC timestamp when this file was written.
    pub written_at: String,
    /// All transitively-discovered topology entries.
    pub entries: Vec<TopologyEntry>,
}

// ---------------------------------------------------------------------------
// Path helpers
// ---------------------------------------------------------------------------

/// Path to `federation_topology.json` under the default cache dir.
pub fn topology_path() -> PathBuf {
    topology_path_in(&cache_dir())
}

/// Path to `federation_topology.json` under `dir` (testing variant).
pub fn topology_path_in(dir: &Path) -> PathBuf {
    dir.join("federation_topology.json")
}

// ---------------------------------------------------------------------------
// Write
// ---------------------------------------------------------------------------

/// Writes `topology` atomically to `~/.cache/orchard/federation_topology.json`.
///
/// Uses write-to-tmp-then-rename for crash safety.
pub fn write_topology(topology: &FederationTopology) -> anyhow::Result<()> {
    write_topology_to(topology, &cache_dir())
}

/// Like [`write_topology`] but writes under `dir` (testing variant).
pub fn write_topology_to(topology: &FederationTopology, dir: &Path) -> anyhow::Result<()> {
    use anyhow::Context as _;

    let path = topology_path_in(dir);
    let parent = path.parent().context("topology path has no parent")?;
    std::fs::create_dir_all(parent).context("create topology cache directory")?;

    let json = serde_json::to_string_pretty(topology).context("serialize topology")?;
    let tmp = path.with_extension("json.tmp");
    std::fs::write(&tmp, &json).context("write topology .tmp file")?;
    std::fs::rename(&tmp, &path).context("rename topology .tmp to final")?;

    Ok(())
}

// ---------------------------------------------------------------------------
// Read
// ---------------------------------------------------------------------------

/// Reads `federation_topology.json` from the default cache dir.
///
/// Returns `None` if the file is missing or the version is below
/// [`TOPOLOGY_MIN_VERSION`].
pub fn read_topology() -> Option<FederationTopology> {
    read_topology_from(&cache_dir())
}

/// Like [`read_topology`] but reads from `dir` (testing variant).
pub fn read_topology_from(dir: &Path) -> Option<FederationTopology> {
    let path = topology_path_in(dir);
    let contents = std::fs::read_to_string(&path).ok()?;
    let topo: FederationTopology = serde_json::from_str(&contents).ok()?;

    if topo.version < TOPOLOGY_MIN_VERSION {
        log_event(
            "federation_topology.version_skew",
            &[
                ("version", serde_json::Value::Number(topo.version.into())),
                (
                    "min_version",
                    serde_json::Value::Number(TOPOLOGY_MIN_VERSION.into()),
                ),
            ],
        );
        return None;
    }

    Some(topo)
}

// ---------------------------------------------------------------------------
// Build topology from walker results
// ---------------------------------------------------------------------------

/// Constructs a [`FederationTopology`] from a slice of
/// `(discovery_path, dedup_key)` pairs produced by the walker.
///
/// Each pair maps to a [`TopologyEntry`] with `last_seen_at` set to now.
pub fn build_topology(entries: &[(Vec<String>, String)]) -> FederationTopology {
    let now = now_iso8601();

    let topology_entries: Vec<TopologyEntry> = entries
        .iter()
        .map(|(path, key)| TopologyEntry {
            dedup_key: key.clone(),
            discovery_path: path.clone(),
            last_seen_at: now.clone(),
        })
        .collect();

    FederationTopology {
        version: TOPOLOGY_CURRENT_VERSION,
        written_at: now,
        entries: topology_entries,
    }
}

// ---------------------------------------------------------------------------
// GC
// ---------------------------------------------------------------------------

/// Deletes orphan snapshot files: those whose `dedup_key` is not in `topology`
/// AND whose host is not a directly-configured remote in `config`.
///
/// A file is considered an orphan when the host it represents is no longer
/// discovered by the walker and is not a directly-configured remote — it was
/// likely from a VM that has since been destroyed.
///
/// Entries that are in the topology but have a `last_seen_at` older than
/// [`TOPOLOGY_SOFT_TTL_DAYS`] days emit a `remote_snapshot.stale` event
/// (logged but not deleted — they remain available for observability).
///
/// Returns the list of deleted file paths.
pub fn gc_orphan_snapshots(
    topology: Option<&FederationTopology>,
    config: &crate::global_config::GlobalConfig,
) -> Vec<PathBuf> {
    gc_orphan_snapshots_in(topology, config, &cache_dir())
}

/// Like [`gc_orphan_snapshots`] but operates under `dir` (testing variant).
pub fn gc_orphan_snapshots_in(
    topology: Option<&FederationTopology>,
    config: &crate::global_config::GlobalConfig,
    dir: &Path,
) -> Vec<PathBuf> {
    use crate::orchard_snapshot::orchard_snapshot_path_in;
    use crate::remote_adapter::RemoteKind;

    // Build the set of "known" dedup keys: topology entries + config remotes.
    let mut known_keys: std::collections::HashSet<String> = std::collections::HashSet::new();

    // Add all dedup keys from the topology.
    if let Some(topo) = topology {
        let now = now_unix_secs();
        for entry in &topo.entries {
            known_keys.insert(entry.dedup_key.clone());

            // Soft TTL: log stale entries but keep them.
            if let Ok(age_days) = age_days_from_iso8601(&entry.last_seen_at, now)
                && age_days > TOPOLOGY_SOFT_TTL_DAYS
            {
                log_event(
                    "remote_snapshot.stale",
                    &[
                        (
                            "dedup_key",
                            serde_json::Value::String(entry.dedup_key.clone()),
                        ),
                        (
                            "last_seen_at",
                            serde_json::Value::String(entry.last_seen_at.clone()),
                        ),
                        ("age_days", serde_json::Value::Number(age_days.into())),
                    ],
                );
            }
        }
    }

    // Add dedup keys for directly-configured OrchardProxy remotes.
    for repo in &config.repos {
        for remote in &repo.remotes {
            if remote.kind == RemoteKind::OrchardProxy {
                if let Ok(key) = crate::federation::host_dedup_key(&remote.host) {
                    known_keys.insert(key);
                }
                // Also add the raw host in case host_dedup_key fails.
                known_keys.insert(remote.host.clone());
            }
        }
    }

    // Scan the cache dir for _orchard_snapshot.json files and delete orphans.
    let mut deleted: Vec<PathBuf> = Vec::new();

    let read_dir = match std::fs::read_dir(dir) {
        Ok(rd) => rd,
        Err(_) => return deleted,
    };

    for entry in read_dir.flatten() {
        let path = entry.path();
        let Some(name) = path.file_name().and_then(|n| n.to_str()) else {
            continue;
        };

        // Only consider files matching the snapshot filename pattern.
        if !name.ends_with("_orchard_snapshot.json") {
            continue;
        }

        // Reverse-map the filename back to a dedup key to check if known.
        // We scan all known keys: for each key, check if it would produce this filename.
        let is_known = known_keys.iter().any(|key| {
            orchard_snapshot_path_in(key, dir)
                .file_name()
                .is_some_and(|n| n == entry.file_name())
        });

        if !is_known && std::fs::remove_file(&path).is_ok() {
            log_event(
                "remote_snapshot.gc_deleted",
                &[(
                    "path",
                    serde_json::Value::String(path.display().to_string()),
                )],
            );
            deleted.push(path);
        }
    }

    deleted
}

// ---------------------------------------------------------------------------
// Host list extraction (for cold-start loader)
// ---------------------------------------------------------------------------

/// Returns the list of unique hosts from a topology that are not already in
/// the direct-config host set.
///
/// These are the transitively-discovered hosts whose snapshots should be loaded
/// at cold start (in addition to the directly-configured OrchardProxy hosts
/// already loaded by [`crate::orchard_snapshot::load_cached_snapshots_from`]).
pub fn transitive_hosts_from_topology(topology: &FederationTopology) -> Vec<String> {
    let mut seen: std::collections::HashSet<String> = std::collections::HashSet::new();
    let mut hosts: Vec<String> = Vec::new();

    for entry in &topology.entries {
        if seen.insert(entry.dedup_key.clone()) {
            hosts.push(entry.dedup_key.clone());
        }
    }

    hosts
}

// ---------------------------------------------------------------------------
// Time helpers
// ---------------------------------------------------------------------------

fn now_iso8601() -> String {
    use std::time::SystemTime;
    let secs = SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .map(|d| d.as_secs())
        .unwrap_or(0);
    unix_secs_to_iso8601(secs)
}

fn now_unix_secs() -> u64 {
    std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .map(|d| d.as_secs())
        .unwrap_or(0)
}

fn unix_secs_to_iso8601(secs: u64) -> String {
    // Produce a simple ISO 8601 UTC string without external crate dependencies.
    // Using chrono is fine since it's already in the dependency tree.
    use chrono::{DateTime, Utc};
    let dt = DateTime::<Utc>::from_timestamp(secs as i64, 0)
        .unwrap_or_else(|| DateTime::<Utc>::from_timestamp(0, 0).unwrap());
    dt.to_rfc3339()
}

fn age_days_from_iso8601(iso: &str, now_secs: u64) -> Result<u64, ()> {
    let ts = chrono::DateTime::parse_from_rfc3339(iso).map_err(|_| ())?;
    let ts_secs = ts.timestamp() as u64;
    let age_secs = now_secs.saturating_sub(ts_secs);
    Ok(age_secs / 86400)
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

#[cfg(test)]
mod tests {
    use super::*;
    use tempfile::TempDir;

    fn make_entry(key: &str, path: &[&str], _root: &str) -> TopologyEntry {
        // `root` is no longer stored as a field; it is derived from discovery_path[1].
        TopologyEntry {
            dedup_key: key.to_string(),
            discovery_path: path.iter().map(|s| s.to_string()).collect(),
            last_seen_at: "2026-01-01T00:00:00+00:00".to_string(),
        }
    }

    fn make_topology(entries: Vec<TopologyEntry>) -> FederationTopology {
        FederationTopology {
            version: TOPOLOGY_CURRENT_VERSION,
            written_at: "2026-01-01T00:00:00+00:00".to_string(),
            entries,
        }
    }

    // -----------------------------------------------------------------------
    // Scenario 343: topology round-trip
    // -----------------------------------------------------------------------

    /// feature:343 — topology file round-trip (write then read back)
    #[test]
    fn topology_round_trip() {
        let dir = TempDir::new().unwrap();
        let topo = make_topology(vec![
            make_entry(
                "boxd@vm.boxd.sh",
                &["local", "boxd", "scenario-voice-agents.boxd.sh"],
                "boxd",
            ),
            make_entry(
                "evals-v3-debug",
                &["local", "boxd", "evals-v3-debug"],
                "boxd",
            ),
        ]);

        write_topology_to(&topo, dir.path()).unwrap();

        let loaded = read_topology_from(dir.path()).expect("must load back");
        assert_eq!(loaded.version, TOPOLOGY_CURRENT_VERSION);
        assert_eq!(loaded.entries.len(), 2);
        assert_eq!(loaded.entries[0].dedup_key, "boxd@vm.boxd.sh");
        assert_eq!(loaded.entries[0].root(), Some("boxd"));
    }

    #[test]
    fn topology_write_is_atomic_no_tmp_left() {
        let dir = TempDir::new().unwrap();
        let topo = make_topology(vec![]);

        write_topology_to(&topo, dir.path()).unwrap();

        let tmp = topology_path_in(dir.path()).with_extension("json.tmp");
        assert!(!tmp.exists(), ".tmp must be renamed away");
    }

    #[test]
    fn topology_missing_file_returns_none() {
        let dir = TempDir::new().unwrap();
        let result = read_topology_from(dir.path());
        assert!(result.is_none(), "missing file must return None");
    }

    #[test]
    fn topology_version_below_min_returns_none() {
        let dir = TempDir::new().unwrap();
        let bad_json = r#"{"version":0,"writtenAt":"2026-01-01T00:00:00Z","entries":[]}"#;
        std::fs::write(topology_path_in(dir.path()), bad_json).unwrap();

        let result = read_topology_from(dir.path());
        // version 0 < TOPOLOGY_MIN_VERSION (1) → None
        assert!(result.is_none());
    }

    #[test]
    fn topology_future_version_accepted() {
        let dir = TempDir::new().unwrap();
        // version 99 > TOPOLOGY_MIN_VERSION → accepted (lower-bound check)
        let json = r#"{"version":99,"writtenAt":"2026-01-01T00:00:00Z","entries":[]}"#;
        std::fs::write(topology_path_in(dir.path()), json).unwrap();

        let result = read_topology_from(dir.path());
        assert!(
            result.is_some(),
            "future version must be accepted by lower-bound check"
        );
    }

    // -----------------------------------------------------------------------
    // Scenario 351: load_cached_snapshots returns union of config + topology
    // -----------------------------------------------------------------------

    /// feature:351 — transitive_hosts_from_topology returns unique dedup keys
    #[test]
    fn transitive_hosts_returns_unique_keys() {
        let topo = make_topology(vec![
            make_entry(
                "scenario-voice-agents",
                &["local", "boxd", "scenario-voice-agents"],
                "boxd",
            ),
            make_entry("evals", &["local", "boxd", "evals"], "boxd"),
            // Duplicate key — must be deduped.
            make_entry(
                "scenario-voice-agents",
                &["local", "other", "scenario-voice-agents"],
                "other",
            ),
        ]);

        let hosts = transitive_hosts_from_topology(&topo);
        assert_eq!(hosts.len(), 2, "duplicates must be collapsed");
        assert!(hosts.contains(&"scenario-voice-agents".to_string()));
        assert!(hosts.contains(&"evals".to_string()));
    }

    // -----------------------------------------------------------------------
    // Scenario 360: orphan snapshot GC
    // -----------------------------------------------------------------------

    /// feature:360 — orphan snapshots are deleted; known snapshots are kept
    #[test]
    fn gc_orphan_snapshots_deletes_orphans_keeps_known() {
        use crate::global_config::{GlobalConfig, RemoteConfig, RepoConfig};
        use crate::json_output::JsonOutput;
        use crate::orchard_snapshot::write_snapshot_to;
        use std::collections::HashMap;

        let dir = TempDir::new().unwrap();

        let snapshot = JsonOutput {
            version: 6,
            tmux_sessions: vec![],
            repos: vec![],
            hosts: HashMap::new(),
            errors: vec![],
        };

        // Write a known snapshot (in config).
        write_snapshot_to("known-host", &snapshot, dir.path()).unwrap();
        // Write an orphan snapshot (not in config, not in topology).
        write_snapshot_to("abandoned-host", &snapshot, dir.path()).unwrap();

        let config = GlobalConfig {
            repos: vec![RepoConfig {
                slug: "owner/repo".to_string(),
                path: "/local".to_string(),
                remotes: vec![RemoteConfig {
                    name: "known".to_string(),
                    host: "known-host".to_string(),
                    path: "/remote".to_string(),
                    shell: "ssh".to_string(),
                    kind: crate::remote_adapter::RemoteKind::OrchardProxy,
                    allow_transitive: false,
                }],
            }],
            ..GlobalConfig::default()
        };

        let topo = make_topology(vec![]); // empty topology — no transitive hosts

        let deleted = gc_orphan_snapshots_in(Some(&topo), &config, dir.path());

        // abandoned-host should be deleted, known-host should be kept.
        assert_eq!(deleted.len(), 1, "exactly one file should be deleted");
        assert!(
            deleted[0].to_string_lossy().contains("abandoned"),
            "deleted path must contain 'abandoned'"
        );

        // known-host snapshot must still exist.
        use crate::orchard_snapshot::orchard_snapshot_path_in;
        assert!(orchard_snapshot_path_in("known-host", dir.path()).exists());
    }

    /// feature:360 — GC keeps topology-listed hosts (not direct config)
    #[test]
    fn gc_keeps_topology_listed_transitive_hosts() {
        use crate::global_config::GlobalConfig;
        use crate::json_output::JsonOutput;
        use crate::orchard_snapshot::write_snapshot_to;
        use std::collections::HashMap;

        let dir = TempDir::new().unwrap();

        let snapshot = JsonOutput {
            version: 6,
            tmux_sessions: vec![],
            repos: vec![],
            hosts: HashMap::new(),
            errors: vec![],
        };

        // A transitive host — not in config but in topology.
        write_snapshot_to("transitive-child", &snapshot, dir.path()).unwrap();

        let config = GlobalConfig::default(); // no remotes

        let topo = make_topology(vec![make_entry(
            "transitive-child",
            &["local", "root", "transitive-child"],
            "root",
        )]);

        let deleted = gc_orphan_snapshots_in(Some(&topo), &config, dir.path());

        assert!(
            deleted.is_empty(),
            "topology-listed host must NOT be deleted by GC"
        );
    }

    // -----------------------------------------------------------------------
    // Scenario 368: 7-day TTL log event
    // -----------------------------------------------------------------------

    /// feature:368 — stale topology entry emits remote_snapshot.stale event (smoke test)
    ///
    /// We can't easily assert on the live events file, but we verify the code runs
    /// without panicking.  The TTL is checked against `last_seen_at`.
    #[test]
    fn gc_stale_entry_does_not_delete_but_logs() {
        use crate::global_config::GlobalConfig;
        use crate::json_output::JsonOutput;
        use crate::orchard_snapshot::write_snapshot_to;
        use std::collections::HashMap;

        let dir = TempDir::new().unwrap();

        let snapshot = JsonOutput {
            version: 6,
            tmux_sessions: vec![],
            repos: vec![],
            hosts: HashMap::new(),
            errors: vec![],
        };

        // A very old topology entry.
        write_snapshot_to("old-host", &snapshot, dir.path()).unwrap();

        let config = GlobalConfig::default();

        // Entry with an ancient last_seen_at (more than 7 days ago).
        let old_entry = TopologyEntry {
            dedup_key: "old-host".to_string(),
            discovery_path: vec![
                "local".to_string(),
                "root".to_string(),
                "old-host".to_string(),
            ],
            last_seen_at: "2020-01-01T00:00:00+00:00".to_string(), // way old
        };
        let topo = make_topology(vec![old_entry]);

        // Must not panic; GC should keep the file (it's in topology).
        let deleted = gc_orphan_snapshots_in(Some(&topo), &config, dir.path());

        assert!(
            deleted.is_empty(),
            "stale but topology-listed file must NOT be deleted"
        );
    }

    // -----------------------------------------------------------------------
    // build_topology helper
    // -----------------------------------------------------------------------

    #[test]
    fn build_topology_sets_correct_fields() {
        let entries = vec![(
            vec!["local".to_string(), "boxd".to_string(), "child".to_string()],
            "child-key".to_string(),
        )];
        let topo = build_topology(&entries);
        assert_eq!(topo.version, TOPOLOGY_CURRENT_VERSION);
        assert_eq!(topo.entries.len(), 1);
        assert_eq!(topo.entries[0].dedup_key, "child-key");
        assert_eq!(topo.entries[0].root(), Some("boxd"));
        assert_eq!(
            topo.entries[0].discovery_path,
            vec!["local", "boxd", "child"]
        );
    }
}
