use std::path::PathBuf;

use serde::{Deserialize, Serialize};

use crate::logger::LOG;

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

/// Top-level global configuration for Orchard.
#[derive(Debug, Clone, Serialize, Deserialize, Default)]
pub struct GlobalConfig {
    pub repos: Vec<RepoConfig>,
}

// ---------------------------------------------------------------------------
// Config location
// ---------------------------------------------------------------------------

fn global_config_path() -> Option<PathBuf> {
    dirs::config_dir().map(|d| d.join("orchard").join("config.json"))
}

// ---------------------------------------------------------------------------
// Loading
// ---------------------------------------------------------------------------

/// Loads the global Orchard configuration.
///
/// Resolution order:
/// 1. `~/.config/orchard/config.json` — explicit multi-repo config.
/// 2. CWD-based single-repo fallback: calls `gh repo view` to detect the
///    current repo slug, uses CWD as the path, and reads `.git/orchard.json`
///    for optional remote config.
/// 3. Empty `GlobalConfig` if neither succeeds.
pub fn load_global_config() -> GlobalConfig {
    if let Some(path) = global_config_path()
        && path.exists() {
            return load_from_path(&path);
        }

    fallback_single_repo()
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
                })
                .collect();

            // If `remote` (singular) is present and there are no `remotes`,
            // promote it as the sole entry.
            if remotes.is_empty() {
                if let Some(r) = raw_repo.remote {
                    remotes.push(RemoteConfig {
                        name: r.name,
                        host: r.host,
                        path: r.path.or(r.repo_path).unwrap_or_default(),
                        shell: r.shell,
                    });
                }
            }

            RepoConfig {
                slug: raw_repo.slug,
                path: raw_repo.path,
                remotes,
            }
        })
        .collect();

    let cfg = GlobalConfig { repos };
    LOG.info(&format!(
        "global_config: loaded {} repo(s) from {}",
        cfg.repos.len(),
        path.display()
    ));
    cfg
}

/// Builds a single-repo `GlobalConfig` from the current working directory.
fn fallback_single_repo() -> GlobalConfig {
    let (owner, name) = match crate::github::get_repo() {
        Ok(pair) => pair,
        Err(e) => {
            LOG.info(&format!("global_config: could not detect repo from CWD: {e}"));
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

    GlobalConfig { repos: vec![repo] }
}

/// Reads `.git/orchard.json` from `repo_root` and extracts all `RemoteConfig`
/// entries. Supports:
/// - `{ "remotes": [{ "name": "...", "host": "...", "repoPath": "..." }] }` (per-repo array)
/// - `{ "remote": { "host": "...", "path": "..." } }` (singular, wrapped as "default")
///
/// Returns an empty vec if the file does not exist or contains no valid remotes.
fn load_orchard_json_remotes(repo_root: &PathBuf) -> Vec<RemoteConfig> {
    let out = match std::process::Command::new("git")
        .args(["rev-parse", "--absolute-git-dir"])
        .current_dir(repo_root)
        .output()
    {
        Ok(o) => o,
        Err(_) => return Vec::new(),
    };

    let git_dir = String::from_utf8_lossy(&out.stdout).trim().to_string();
    let orchard_json = PathBuf::from(&git_dir).join("orchard.json");

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
    if let Some(entries) = raw.remotes {
        for r in entries {
            if let Some(host) = r.host.filter(|s| !s.is_empty()) {
                if let Some(path) = r.path.or(r.repo_path).filter(|s| !s.is_empty()) {
                    results.push(RemoteConfig {
                        name: r.name.unwrap_or_else(|| "default".to_string()),
                        host,
                        path,
                        shell: r.shell.unwrap_or_else(|| "ssh".to_string()),
                    });
                }
            }
        }
    }

    // Fall back to singular `remote` only when `remotes` produced nothing.
    if results.is_empty() {
        if let Some(r) = raw.remote {
            if let Some(host) = r.host.filter(|s| !s.is_empty()) {
                if let Some(path) = r.path.or(r.repo_path).filter(|s| !s.is_empty()) {
                    results.push(RemoteConfig {
                        name: r.name.unwrap_or_else(|| "default".to_string()),
                        host,
                        path,
                        shell: r.shell.unwrap_or_else(|| "ssh".to_string()),
                    });
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

        assert_eq!(repo.owner(), "webapp");
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
        assert_eq!(cfg.repos[0].remotes[0].path, "~/webapp-workspace/webapp-bare");
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
                },
                RemoteConfig {
                    name: "cpu".to_string(),
                    host: "ubuntu@10.0.0.2".to_string(),
                    path: "/home/ubuntu/repo".to_string(),
                    shell: "ssh".to_string(),
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
                },
                RemoteConfig {
                    name: "cpu".to_string(),
                    host: "ubuntu@10.0.0.2".to_string(),
                    path: "/home/ubuntu/repo".to_string(),
                    shell: "ssh".to_string(),
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
        assert_eq!(repo.repo_name(), "git-orchard-rs");
    }

    #[test]
    fn invalid_json_returns_empty_config() {
        let dir = tempdir().unwrap();
        let path = write_config(dir.path(), "not valid json");
        let cfg = load_from_path(&path);

        assert_eq!(cfg.repos.len(), 0);
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
}
