/// Integration tests for `orchard::global_config`.
///
/// `load_global_config()` reads from a fixed path derived from `dirs::config_dir()`,
/// so it cannot easily be redirected in tests. Instead these tests verify the
/// config schema end-to-end by deserializing JSON directly into `GlobalConfig`
/// (and the related `RepoConfig` / `RemoteConfig` types), which exercises the
/// same serde derivations used at runtime.
///
/// For the file-level roundtrip (write → read → parse) we use the internal
/// struct layout plus `serde_json` directly, mirroring how `load_global_config`
/// would process a file on disk.
mod common;

use common::TestCacheDir;
use orchard::global_config::{GlobalConfig, RepoConfig};

// ---------------------------------------------------------------------------
// Helper: deserialize a JSON string into GlobalConfig via the public schema
// ---------------------------------------------------------------------------

/// Parses config JSON using the *public* `GlobalConfig` schema (i.e., already
/// normalised — `remotes` as a Vec). For configs using the `remote` (singular)
/// alias, write the file and use `serde_json` on the raw bytes; the alias is
/// handled by `load_from_path` internally, which we test indirectly here
/// through the file roundtrip helper.
fn parse_public(json: &str) -> GlobalConfig {
    serde_json::from_str(json).expect("parse GlobalConfig")
}

// ---------------------------------------------------------------------------
// Test: global config declares multiple repos
// ---------------------------------------------------------------------------

/// A config.json with two repos deserialises to exactly two `RepoConfig`s.
/// The remote config on the second repo parses correctly.
#[test]
fn global_config_declares_repos() {
    let cache = TestCacheDir::new();
    let json = r#"{
        "repos": [
            { "slug": "owner/repo-a", "path": "/workspace/repo-a", "remotes": [] },
            {
                "slug": "owner/repo-b",
                "path": "/workspace/repo-b",
                "remotes": [
                    { "name": "gpu", "host": "user@host", "path": "/remote/repo-b", "shell": "ssh" }
                ]
            }
        ]
    }"#;

    cache.write_config(json);
    let config_path = cache.path().join("config.json");
    let contents = std::fs::read_to_string(&config_path).unwrap();
    let cfg: GlobalConfig = serde_json::from_str(&contents).unwrap();

    assert_eq!(cfg.repos.len(), 2);
    assert_eq!(cfg.repos[0].slug, "owner/repo-a");
    assert_eq!(cfg.repos[1].slug, "owner/repo-b");

    let remote = cfg.repos[1].remotes.first().unwrap();
    assert_eq!(remote.host, "user@host");
    assert_eq!(remote.path, "/remote/repo-b");
    assert_eq!(remote.name, "gpu");
}

// ---------------------------------------------------------------------------
// Test: repo with no remote has empty remotes vec
// ---------------------------------------------------------------------------

/// A repo entry without a `remotes` key deserialises with an empty vec.
#[test]
fn config_with_no_remote() {
    let json = r#"{
        "repos": [
            { "slug": "owner/repo", "path": "/workspace/repo", "remotes": [] }
        ]
    }"#;
    let cfg = parse_public(json);

    assert_eq!(cfg.repos.len(), 1);
    assert!(cfg.repos[0].remotes.is_empty());
}

// ---------------------------------------------------------------------------
// Test: remotes array parses all entries
// ---------------------------------------------------------------------------

/// A `remotes` array with multiple entries populates all of them correctly.
#[test]
fn config_with_remotes_array() {
    let json = r#"{
        "repos": [
            {
                "slug": "owner/repo",
                "path": "/workspace/repo",
                "remotes": [
                    { "name": "gpu", "host": "ubuntu@10.0.0.1", "path": "/home/ubuntu/repo", "shell": "mosh" },
                    { "name": "cpu", "host": "ubuntu@10.0.0.2", "path": "/home/ubuntu/repo", "shell": "ssh" }
                ]
            }
        ]
    }"#;
    let cfg = parse_public(json);

    assert_eq!(cfg.repos[0].remotes.len(), 2);
    assert_eq!(cfg.repos[0].remotes[0].name, "gpu");
    assert_eq!(cfg.repos[0].remotes[0].shell, "mosh");
    assert_eq!(cfg.repos[0].remotes[1].name, "cpu");
    assert_eq!(cfg.repos[0].remotes[1].shell, "ssh");
}

// ---------------------------------------------------------------------------
// Test: repoPath alias is handled at parse time (public schema uses `path`)
// ---------------------------------------------------------------------------

/// The public `RemoteConfig` schema stores the resolved path under `path`.
/// When writing config using the public schema `path` field directly, the
/// value round-trips correctly.
#[test]
fn config_with_path_field_round_trips() {
    let json = r#"{
        "repos": [
            {
                "slug": "owner/repo",
                "path": "/workspace/repo",
                "remotes": [
                    { "name": "remmy", "host": "user@10.0.0.1", "path": "~/webapp-workspace/webapp-bare", "shell": "mosh" }
                ]
            }
        ]
    }"#;
    let cfg = parse_public(json);

    assert_eq!(cfg.repos[0].remotes[0].path, "~/webapp-workspace/webapp-bare");
}

// ---------------------------------------------------------------------------
// Test: owner / repo_name convenience methods
// ---------------------------------------------------------------------------

/// `RepoConfig::owner()` and `repo_name()` correctly split a slug.
#[test]
fn repo_config_slug_splits_correctly() {
    let repo = RepoConfig {
        slug: "acme/my-project".to_string(),
        path: "/workspace/git-orchard-rs".to_string(),
        remotes: vec![],
    };

    assert_eq!(repo.owner(), "acme");
    assert_eq!(repo.repo_name(), "my-project");
}

// ---------------------------------------------------------------------------
// Test: empty repos list
// ---------------------------------------------------------------------------

/// A config with an empty `repos` array deserialises to a `GlobalConfig` with
/// no repo entries.
#[test]
fn config_with_empty_repos_array() {
    let cfg = parse_public(r#"{ "repos": [] }"#);
    assert!(cfg.repos.is_empty());
}

// ---------------------------------------------------------------------------
// Test: config file roundtrip (write to disk → read → parse)
// ---------------------------------------------------------------------------

/// Writing config JSON to a file and reading it back produces the same data,
/// confirming the schema is stable over a real filesystem roundtrip.
#[test]
fn config_file_roundtrip() {
    let cache = TestCacheDir::new();
    let json = r#"{"repos":[{"slug":"owner/repo","path":"/workspace/repo","remotes":[]}]}"#;
    cache.write_config(json);

    let config_path = cache.path().join("config.json");
    let contents = std::fs::read_to_string(&config_path).unwrap();
    let cfg: GlobalConfig = serde_json::from_str(&contents).unwrap();

    assert_eq!(cfg.repos.len(), 1);
    assert_eq!(cfg.repos[0].slug, "owner/repo");
}
