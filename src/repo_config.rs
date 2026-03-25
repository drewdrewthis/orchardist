use std::path::{Path, PathBuf};
use std::process::Command;

use serde::{Deserialize, Serialize};

use crate::logger::LOG;

#[derive(Debug, Clone, Default, Serialize, Deserialize)]
pub struct RepoLocalConfig {
    #[serde(default)]
    pub ci: CiConfig,
}

#[derive(Debug, Clone, Default, Serialize, Deserialize)]
pub struct CiConfig {
    #[serde(default)]
    pub ignore: Vec<String>,
    #[serde(default)]
    pub required: Vec<String>,
}

impl CiConfig {
    /// Filters checks according to `required` or `ignore` patterns.
    ///
    /// - If `required` is non-empty: retain only checks whose name contains any required pattern
    ///   (case-insensitive substring match).
    /// - Else if `ignore` is non-empty: remove checks whose name contains any ignore pattern
    ///   (case-insensitive substring match).
    /// - Otherwise: return all checks unchanged.
    pub fn filter_checks(&self, checks: &[(String, String)]) -> Vec<(String, String)> {
        if !self.required.is_empty() {
            let patterns: Vec<String> = self.required.iter().map(|p| p.to_lowercase()).collect();
            return checks
                .iter()
                .filter(|(name, _)| {
                    let lower = name.to_lowercase();
                    patterns.iter().any(|p| lower.contains(p.as_str()))
                })
                .cloned()
                .collect();
        }

        if !self.ignore.is_empty() {
            let patterns: Vec<String> = self.ignore.iter().map(|p| p.to_lowercase()).collect();
            return checks
                .iter()
                .filter(|(name, _)| {
                    let lower = name.to_lowercase();
                    !patterns.iter().any(|p| lower.contains(p.as_str()))
                })
                .cloned()
                .collect();
        }

        checks.to_vec()
    }

    /// Derives an overall CI state from `checks` after applying `filter_checks`.
    ///
    /// Returns `None` if the filtered list is empty, otherwise the worst state:
    /// `"failing"` > `"pending"` > `"passing"`.
    pub fn derive_checks_state(&self, checks: &[(String, String)]) -> Option<String> {
        let filtered = self.filter_checks(checks);

        if filtered.is_empty() {
            return None;
        }

        let mut has_pending = false;

        for (_, state) in &filtered {
            match state.as_str() {
                "failing" | "error" => return Some("failing".to_string()),
                "pending" => has_pending = true,
                _ => {}
            }
        }

        if has_pending {
            return Some("pending".to_string());
        }

        Some("passing".to_string())
    }
}

/// Loads `.orchard.json` from `repo_path` and `.git/orchard.json` from the git dir,
/// then merges them with array-union semantics (git dir overlays on top of repo root).
pub fn load_repo_config(repo_path: &str) -> RepoLocalConfig {
    let root = load_from_path(&PathBuf::from(repo_path).join(".orchard.json"));
    let git_dir = resolve_git_dir(repo_path)
        .map(|dir| load_from_path(&PathBuf::from(dir).join("orchard.json")))
        .unwrap_or_default();
    merge(root, git_dir)
}

// Reads and parses a RepoLocalConfig from the given path.
// Returns defaults if the file is missing or contains invalid JSON.
fn load_from_path(path: &Path) -> RepoLocalConfig {
    let data = match std::fs::read(path) {
        Ok(d) => d,
        Err(_) => return RepoLocalConfig::default(),
    };
    match serde_json::from_slice(&data) {
        Ok(cfg) => cfg,
        Err(err) => {
            LOG.warn(&format!(
                "repo_config: failed to parse {}: {}",
                path.display(),
                err
            ));
            RepoLocalConfig::default()
        }
    }
}

// Runs `git rev-parse --absolute-git-dir` from `repo_path`.
fn resolve_git_dir(repo_path: &str) -> Option<String> {
    let out = Command::new("git")
        .args(["rev-parse", "--absolute-git-dir"])
        .current_dir(repo_path)
        .output()
        .ok()?;
    if !out.status.success() {
        return None;
    }
    let path = String::from_utf8_lossy(&out.stdout).trim().to_string();
    if path.is_empty() { None } else { Some(path) }
}

// Merges `base` and `overlay` with array-union semantics.
// The overlay is applied on top of the base; arrays are concatenated and deduped.
fn merge(base: RepoLocalConfig, overlay: RepoLocalConfig) -> RepoLocalConfig {
    RepoLocalConfig {
        ci: CiConfig {
            ignore: union(base.ci.ignore, overlay.ci.ignore),
            required: union(base.ci.required, overlay.ci.required),
        },
    }
}

// Returns a deduped union of two string vecs, preserving order (base first).
fn union(mut base: Vec<String>, overlay: Vec<String>) -> Vec<String> {
    for item in overlay {
        if !base.contains(&item) {
            base.push(item);
        }
    }
    base
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::fs;
    use tempfile::tempdir;

    // ---------- helpers ----------

    fn checks(pairs: &[(&str, &str)]) -> Vec<(String, String)> {
        pairs.iter().map(|(n, s)| (n.to_string(), s.to_string())).collect()
    }

    fn write_json(dir: &std::path::Path, filename: &str, json: &str) {
        fs::write(dir.join(filename), json).unwrap();
    }

    // ---------- struct defaults ----------

    #[test]
    fn empty_config_returns_defaults() {
        let cfg = RepoLocalConfig::default();
        assert!(cfg.ci.ignore.is_empty());
        assert!(cfg.ci.required.is_empty());
    }

    // ---------- load_repo_config: file sources ----------

    #[test]
    fn load_from_repo_root_only() {
        let dir = tempdir().unwrap();
        write_json(dir.path(), ".orchard.json", r#"{"ci":{"ignore":["flaky"]}}"#);

        // No .git dir, so git dir layer is empty.
        let cfg = load_repo_config(dir.path().to_str().unwrap());
        assert_eq!(cfg.ci.ignore, vec!["flaky"]);
        assert!(cfg.ci.required.is_empty());
    }

    #[test]
    fn load_from_git_dir_only() {
        let dir = tempdir().unwrap();
        let git_dir = dir.path().join(".git");
        fs::create_dir_all(&git_dir).unwrap();
        write_json(&git_dir, "orchard.json", r#"{"ci":{"required":["build"]}}"#);

        // Simulate a real git repo so resolve_git_dir succeeds.
        // Initialise a bare-ish layout: HEAD is the minimum git needs.
        fs::write(git_dir.join("HEAD"), "ref: refs/heads/main\n").unwrap();

        // We cannot call git inside a non-git temp dir easily, so test via
        // load_from_path directly for the git-dir layer.
        let git_layer = load_from_path(&git_dir.join("orchard.json"));
        assert_eq!(git_layer.ci.required, vec!["build"]);
        assert!(git_layer.ci.ignore.is_empty());
    }

    #[test]
    fn merge_union_semantics_deduplicates() {
        let base = RepoLocalConfig {
            ci: CiConfig {
                ignore: vec!["flaky".to_string(), "lint".to_string()],
                required: vec!["build".to_string()],
            },
        };
        let overlay = RepoLocalConfig {
            ci: CiConfig {
                ignore: vec!["lint".to_string(), "slow".to_string()],
                required: vec!["build".to_string(), "test".to_string()],
            },
        };
        let merged = merge(base, overlay);
        assert_eq!(merged.ci.ignore, vec!["flaky", "lint", "slow"]);
        assert_eq!(merged.ci.required, vec!["build", "test"]);
    }

    #[test]
    fn missing_files_return_defaults() {
        let dir = tempdir().unwrap();
        // Neither .orchard.json nor .git/orchard.json exist.
        let cfg = load_repo_config(dir.path().to_str().unwrap());
        assert!(cfg.ci.ignore.is_empty());
        assert!(cfg.ci.required.is_empty());
    }

    #[test]
    fn invalid_json_returns_defaults_gracefully() {
        let dir = tempdir().unwrap();
        write_json(dir.path(), ".orchard.json", "not valid json {{");
        let cfg = load_repo_config(dir.path().to_str().unwrap());
        assert!(cfg.ci.ignore.is_empty());
        assert!(cfg.ci.required.is_empty());
    }

    // ---------- filter_checks ----------

    #[test]
    fn filter_checks_passthrough_when_no_patterns() {
        let ci = CiConfig::default();
        let input = checks(&[("build", "passing"), ("test", "failing")]);
        assert_eq!(ci.filter_checks(&input), input);
    }

    #[test]
    fn filter_checks_removes_ignored_patterns_case_insensitive() {
        let ci = CiConfig {
            ignore: vec!["Flaky".to_string()],
            required: vec![],
        };
        let input = checks(&[("flaky-test", "failing"), ("build", "passing")]);
        let result = ci.filter_checks(&input);
        assert_eq!(result.len(), 1);
        assert_eq!(result[0].0, "build");
    }

    #[test]
    fn filter_checks_keeps_required_patterns_case_insensitive() {
        let ci = CiConfig {
            ignore: vec![],
            required: vec!["BUILD".to_string()],
        };
        let input = checks(&[("build-release", "passing"), ("test", "failing")]);
        let result = ci.filter_checks(&input);
        assert_eq!(result.len(), 1);
        assert_eq!(result[0].0, "build-release");
    }

    #[test]
    fn filter_checks_required_takes_precedence_over_ignore() {
        let ci = CiConfig {
            ignore: vec!["test".to_string()],
            required: vec!["build".to_string()],
        };
        let input = checks(&[("build", "passing"), ("test", "failing"), ("lint", "passing")]);
        // required is non-empty, so ignore is not consulted.
        let result = ci.filter_checks(&input);
        assert_eq!(result.len(), 1);
        assert_eq!(result[0].0, "build");
    }

    // ---------- derive_checks_state ----------

    #[test]
    fn derive_checks_state_all_passing() {
        let ci = CiConfig::default();
        let input = checks(&[("build", "passing"), ("test", "passing")]);
        assert_eq!(ci.derive_checks_state(&input), Some("passing".to_string()));
    }

    #[test]
    fn derive_checks_state_any_failing_wins() {
        let ci = CiConfig::default();
        let input = checks(&[("build", "passing"), ("test", "failing"), ("lint", "pending")]);
        assert_eq!(ci.derive_checks_state(&input), Some("failing".to_string()));
    }

    #[test]
    fn derive_checks_state_error_treated_as_failing() {
        let ci = CiConfig::default();
        let input = checks(&[("build", "error")]);
        assert_eq!(ci.derive_checks_state(&input), Some("failing".to_string()));
    }

    #[test]
    fn derive_checks_state_pending_without_failing() {
        let ci = CiConfig::default();
        let input = checks(&[("build", "passing"), ("test", "pending")]);
        assert_eq!(ci.derive_checks_state(&input), Some("pending".to_string()));
    }

    #[test]
    fn derive_checks_state_mixed_states() {
        let ci = CiConfig::default();
        let input = checks(&[("a", "passing"), ("b", "pending"), ("c", "passing")]);
        assert_eq!(ci.derive_checks_state(&input), Some("pending".to_string()));
    }

    #[test]
    fn derive_checks_state_empty_after_filtering_returns_none() {
        let ci = CiConfig {
            ignore: vec!["flaky".to_string()],
            required: vec![],
        };
        // All checks match the ignore pattern, leaving nothing.
        let input = checks(&[("flaky-build", "failing"), ("flaky-test", "error")]);
        assert_eq!(ci.derive_checks_state(&input), None);
    }

    #[test]
    fn derive_checks_state_failing_filtered_out_returns_passing() {
        let ci = CiConfig {
            ignore: vec!["flaky".to_string()],
            required: vec![],
        };
        let input = checks(&[("flaky-test", "failing"), ("build", "passing")]);
        assert_eq!(ci.derive_checks_state(&input), Some("passing".to_string()));
    }
}
