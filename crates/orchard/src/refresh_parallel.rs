//! Parallel fan-out helpers for refresh pipelines.
//!
//! Every caller that refreshes per-repo caches, per-(repo, remote) caches, or
//! per-unique-host caches fans the work out with [`std::thread::scope`] so that
//! one slow GitHub API call or SSH round-trip can't block another. The bodies
//! live here so the same ergonomics and invariants (scoped borrows, caller
//! logs failures, closures must not panic) apply everywhere.
//!
//! Invariants callers must uphold:
//! * Each refresh writes to its own cache file, so no shared mutable state
//!   crosses thread boundaries.
//! * The closure must not panic — `std::thread::scope` propagates panics from
//!   any thread when the scope is dropped, which would abort the refresh for
//!   every other repo/host. In practice all `refresh_*` functions return
//!   `anyhow::Result` and never panic.

use std::collections::HashSet;

use crate::global_config::{GlobalConfig, RepoConfig};

/// Runs `f` once per configured repo, in parallel.
///
/// Each closure invocation borrows its `RepoConfig` for the lifetime of the
/// scope. All threads join before the function returns.
pub fn for_each_repo_parallel<F>(config: &GlobalConfig, f: F)
where
    F: Fn(&RepoConfig) + Sync,
{
    std::thread::scope(|s| {
        for repo in &config.repos {
            let f_ref = &f;
            s.spawn(move || f_ref(repo));
        }
    });
}

/// Returns the set of unique reachable hosts across all repo remotes, in
/// declaration order. Used to dedupe per-host work (like a single tmux
/// refresh) when multiple repos reference the same remote.
pub fn unique_reachable_hosts(
    config: &GlobalConfig,
    reachable: impl Fn(&str) -> bool,
) -> Vec<String> {
    let mut seen = HashSet::new();
    config
        .repos
        .iter()
        .flat_map(|r| r.remotes.iter().map(|rm| rm.host.clone()))
        .filter(|h| reachable(h) && seen.insert(h.clone()))
        .collect()
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::global_config::RemoteConfig;
    use crate::remote_adapter::RemoteKind;
    use std::sync::atomic::{AtomicUsize, Ordering};
    use std::sync::{Arc, Mutex};
    use std::thread;
    use std::time::{Duration, Instant};

    fn repo(slug: &str, hosts: &[&str]) -> RepoConfig {
        RepoConfig {
            slug: slug.to_string(),
            path: format!("/tmp/{slug}"),
            remotes: hosts
                .iter()
                .map(|h| RemoteConfig {
                    name: h.to_string(),
                    host: h.to_string(),
                    path: "~/src".to_string(),
                    shell: "ssh".to_string(),
                    kind: RemoteKind::Remmy,
                    fallback_kind: None,
                })
                .collect(),
        }
    }

    /// Regression: refreshes must dispatch concurrently, not serially. With
    /// three repos that each sleep 200ms, serial execution would take ~600ms;
    /// the fan-out should finish near 200ms. 500ms budget tolerates scheduler
    /// jitter on CI.
    #[test]
    fn for_each_repo_parallel_runs_concurrently() {
        let config = GlobalConfig {
            repos: vec![repo("a/a", &[]), repo("b/b", &[]), repo("c/c", &[])],
            ..GlobalConfig::default()
        };
        let count = Arc::new(AtomicUsize::new(0));
        let count_c = count.clone();

        let start = Instant::now();
        for_each_repo_parallel(&config, move |_r| {
            thread::sleep(Duration::from_millis(200));
            count_c.fetch_add(1, Ordering::SeqCst);
        });
        let elapsed = start.elapsed();

        assert_eq!(count.load(Ordering::SeqCst), 3);
        assert!(
            elapsed < Duration::from_millis(500),
            "expected parallel dispatch (<500ms, serial would be ~600ms), got {:?}",
            elapsed
        );
    }

    #[test]
    fn unique_reachable_hosts_dedupes_and_filters() {
        let config = GlobalConfig {
            repos: vec![
                repo("a/a", &["host-alive", "host-dead"]),
                repo("b/b", &["host-alive", "host-other"]),
            ],
            ..GlobalConfig::default()
        };
        let alive: HashSet<&str> = ["host-alive", "host-other"].into_iter().collect();

        let hosts = unique_reachable_hosts(&config, |h| alive.contains(h));
        assert_eq!(
            hosts,
            vec!["host-alive".to_string(), "host-other".to_string()]
        );
    }

    #[test]
    fn for_each_repo_parallel_passes_each_repo_exactly_once() {
        let config = GlobalConfig {
            repos: vec![repo("a/a", &[]), repo("b/b", &[]), repo("c/c", &[])],
            ..GlobalConfig::default()
        };
        let seen: Arc<Mutex<Vec<String>>> = Arc::new(Mutex::new(Vec::new()));
        let seen_c = seen.clone();

        for_each_repo_parallel(&config, move |r| {
            seen_c.lock().unwrap().push(r.slug.clone());
        });

        let mut slugs = seen.lock().unwrap().clone();
        slugs.sort();
        assert_eq!(
            slugs,
            vec!["a/a".to_string(), "b/b".to_string(), "c/c".to_string()]
        );
    }
}
