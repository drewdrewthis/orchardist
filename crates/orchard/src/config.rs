//! Per-repository configuration loader for Orchard.
//!
//! Reads two config layers and merges them, with the local layer taking precedence:
//! - `.orchard.json` in the repo root (committable, team-shared)
//! - `.git/orchard.json` in the git directory (local, machine-specific overrides)
//!
//! Supports the current `{ "remote": {...} }` format and a legacy multi-remote
//! array format. Used by the imperative shell at startup to discover remote hosts.
use std::path::{Path, PathBuf};
use std::process::Command;

use crate::logger::LOG;
use crate::types::{OrchardConfig, RemoteConfig};

/// Loads the merged Orchard config for the current repository.
///
/// Reads `.orchard.json` (committable) and `.git/orchard.json` (local).
/// Fields present in the local layer override the committable layer.
/// Returns an empty `OrchardConfig` on any error.
pub fn load_config() -> OrchardConfig {
    match git_absolute_dir() {
        Ok(git_dir) => {
            let repo_root = PathBuf::from(&git_dir)
                .parent()
                .map(|p| p.to_string_lossy().into_owned())
                .unwrap_or_default();
            let committable =
                load_config_from_file(&PathBuf::from(&repo_root).join(".orchard.json"));
            let local = load_config_from_file(&PathBuf::from(&git_dir).join("orchard.json"));
            merge_configs(committable, local)
        }
        Err(_) => OrchardConfig::default(),
    }
}

/// Merges two `OrchardConfig` layers. Fields in `local` take precedence over `base`.
fn merge_configs(base: OrchardConfig, local: OrchardConfig) -> OrchardConfig {
    OrchardConfig {
        remote: local.remote.or(base.remote),
        setup_script: local.setup_script.or(base.setup_script),
    }
}

// Reads and parses an OrchardConfig from a file path. Returns default on any error.
fn load_config_from_file(path: &Path) -> OrchardConfig {
    let data = match std::fs::read(path) {
        Ok(d) => d,
        Err(_) => return OrchardConfig::default(),
    };
    parse_config(&data, &path.to_string_lossy())
}

// Unmarshals raw JSON bytes into an OrchardConfig.
fn parse_config(data: &[u8], path: &str) -> OrchardConfig {
    #[derive(serde::Deserialize)]
    struct LegacyEntry {
        host: String,
        #[serde(rename = "repoPath")]
        repo_path: String,
        #[serde(default)]
        shell: String,
    }

    #[derive(serde::Deserialize)]
    struct RawConfig {
        remote: Option<RemoteConfig>,
        #[serde(default)]
        remotes: Vec<LegacyEntry>,
        setup_script: Option<String>,
    }

    let raw: RawConfig = match serde_json::from_slice(data) {
        Ok(r) => r,
        Err(error) => {
            LOG.warn(&format!("config: failed to parse orchard.json: {}", error));
            return OrchardConfig::default();
        }
    };

    // New format takes precedence.
    if let Some(remote) = raw.remote {
        LOG.info(&format!(
            "config: loaded remote {} from {}",
            remote.host, path
        ));
        return OrchardConfig {
            remote: Some(remote),
            setup_script: raw.setup_script,
        };
    }

    // Legacy format: use the first entry that has both host and repoPath.
    for entry in raw.remotes {
        if entry.host.is_empty() || entry.repo_path.is_empty() {
            continue;
        }
        let shell = if entry.shell.is_empty() {
            "ssh".to_string()
        } else {
            entry.shell
        };
        LOG.info(&format!(
            "config: migrated remote {} from legacy format",
            entry.host
        ));
        return OrchardConfig {
            remote: Some(RemoteConfig {
                host: entry.host,
                repo_path: entry.repo_path,
                shell,
            }),
            setup_script: raw.setup_script,
        };
    }

    OrchardConfig {
        remote: None,
        setup_script: raw.setup_script,
    }
}

// Runs `git rev-parse --absolute-git-dir` and returns the path.
fn git_absolute_dir() -> anyhow::Result<String> {
    let out = Command::new("git")
        .args(["rev-parse", "--absolute-git-dir"])
        .output()?;
    Ok(String::from_utf8_lossy(&out.stdout).trim().to_string())
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::io::Write;
    use tempfile::NamedTempFile;

    fn write_temp(json: &str) -> NamedTempFile {
        let mut f = NamedTempFile::new().unwrap();
        f.write_all(json.as_bytes()).unwrap();
        f
    }

    fn load_from_file(path: &str) -> OrchardConfig {
        let data = std::fs::read(path).unwrap();
        parse_config(&data, path)
    }

    #[test]
    fn new_format_remote() {
        let f = write_temp(r#"{"remote":{"host":"myhost","repoPath":"/srv/repo","shell":"ssh"}}"#);
        let cfg = load_from_file(f.path().to_str().unwrap());
        let remote = cfg.remote.unwrap();
        assert_eq!(remote.host, "myhost");
        assert_eq!(remote.repo_path, "/srv/repo");
        assert_eq!(remote.shell, "ssh");
    }

    #[test]
    fn legacy_format_remotes_first_valid_entry() {
        let f = write_temp(
            r#"{"remotes":[{"host":"h1","repoPath":"/p1"},{"host":"h2","repoPath":"/p2"}]}"#,
        );
        let cfg = load_from_file(f.path().to_str().unwrap());
        let remote = cfg.remote.unwrap();
        assert_eq!(remote.host, "h1");
        assert_eq!(remote.repo_path, "/p1");
        assert_eq!(remote.shell, "ssh"); // default
    }

    #[test]
    fn legacy_format_skips_incomplete_entries() {
        let f = write_temp(
            r#"{"remotes":[{"host":"","repoPath":"/p"},{"host":"h2","repoPath":"/p2"}]}"#,
        );
        let cfg = load_from_file(f.path().to_str().unwrap());
        let remote = cfg.remote.unwrap();
        assert_eq!(remote.host, "h2");
    }

    #[test]
    fn new_format_takes_precedence_over_legacy() {
        let f = write_temp(
            r#"{"remote":{"host":"new","repoPath":"/new","shell":"mosh"},"remotes":[{"host":"old","repoPath":"/old"}]}"#,
        );
        let cfg = load_from_file(f.path().to_str().unwrap());
        assert_eq!(cfg.remote.unwrap().host, "new");
    }

    #[test]
    fn empty_json_returns_default() {
        let f = write_temp("{}");
        let cfg = load_from_file(f.path().to_str().unwrap());
        assert!(cfg.remote.is_none());
    }

    #[test]
    fn invalid_json_returns_default() {
        let f = write_temp("not json");
        let cfg = load_from_file(f.path().to_str().unwrap());
        assert!(cfg.remote.is_none());
    }

    #[test]
    fn setup_script_is_parsed_from_json() {
        let f = write_temp(r#"{"setup_script":"./scripts/setup-worktree.sh"}"#);
        let cfg = load_from_file(f.path().to_str().unwrap());
        assert_eq!(
            cfg.setup_script,
            Some("./scripts/setup-worktree.sh".to_string())
        );
    }

    #[test]
    fn setup_script_defaults_to_none_when_omitted() {
        let f = write_temp("{}");
        let cfg = load_from_file(f.path().to_str().unwrap());
        assert!(cfg.setup_script.is_none());
    }

    #[test]
    fn merge_configs_local_setup_script_wins() {
        let base = OrchardConfig {
            remote: None,
            setup_script: Some("./team-setup.sh".to_string()),
        };
        let local = OrchardConfig {
            remote: None,
            setup_script: Some("./my-setup.sh".to_string()),
        };
        let merged = merge_configs(base, local);
        assert_eq!(merged.setup_script, Some("./my-setup.sh".to_string()));
    }

    #[test]
    fn merge_configs_base_setup_script_used_when_local_absent() {
        let base = OrchardConfig {
            remote: None,
            setup_script: Some("./team-setup.sh".to_string()),
        };
        let local = OrchardConfig::default();
        let merged = merge_configs(base, local);
        assert_eq!(merged.setup_script, Some("./team-setup.sh".to_string()));
    }

    #[test]
    fn setup_script_round_trips_through_serde() {
        let cfg = OrchardConfig {
            remote: None,
            setup_script: Some("./scripts/setup.sh".to_string()),
        };
        let json = serde_json::to_string(&cfg).unwrap();
        let deserialized: OrchardConfig = serde_json::from_str(&json).unwrap();
        assert_eq!(
            deserialized.setup_script,
            Some("./scripts/setup.sh".to_string())
        );
    }

    #[test]
    fn setup_script_omitted_from_json_when_none() {
        let cfg = OrchardConfig {
            remote: None,
            setup_script: None,
        };
        let json = serde_json::to_string(&cfg).unwrap();
        assert!(!json.contains("setup_script"));
    }
}
