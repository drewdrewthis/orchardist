//! SSH host reachability probes.
//!
//! Simple SSH connectivity checks for remote hosts. Used to determine if a host is
//! reachable before attempting worktree or tmux operations on it. Probes run with a
//! 5-second connect timeout (set in `remote::ssh_flags()`), so dead hosts fail fast.
//!
//! The concurrent entry point is [`probe_reachability_all_for_remotes`], which
//! accepts full `RemoteConfig` entries so each host is probed with the correct
//! command for its kind. Each probe runs on its own thread so a stopped VM
//! can't block probes for healthy hosts behind it.

use std::collections::{HashMap, HashSet};
use std::thread;

use crate::global_config::{GlobalConfig, RemoteConfig, RepoConfig};

/// Returns every `RemoteConfig` configured across all repos in `config`.
///
/// Order follows config iteration order; duplicates across repos are preserved
/// in the returned `Vec` (callers that need uniqueness should pass through
/// [`probe_reachability_all_for_remotes`], which dedupes internally by host).
pub fn remotes_from_config(config: &GlobalConfig) -> Vec<RemoteConfig> {
    config
        .repos
        .iter()
        .flat_map(|r| r.remotes.iter().cloned())
        .collect()
}

/// Probes whether a remote is reachable, using a probe command appropriate
/// for the remote's kind.
///
/// `Remmy` and `BoxdShared` reach a general-purpose shell on the remote host,
/// so `true` is a valid probe. `BoxdFork` targets the Boxd controller
/// (e.g. `boxd.sh`), which is a restricted CLI that rejects `true` —
/// `list --json` is the canonical health check there. Using the wrong probe
/// would mark a perfectly healthy golden host as unreachable and silence
/// every fork behind it.
pub(crate) fn probe_reachability_for_remote(remote: &RemoteConfig) -> bool {
    let cmd = match remote.kind {
        crate::remote_adapter::RemoteKind::BoxdFork => "list --json",
        crate::remote_adapter::RemoteKind::Remmy
        | crate::remote_adapter::RemoteKind::BoxdShared => "true",
    };
    crate::remote::ssh_exec(&remote.host, cmd).is_ok()
}

/// Probes many (host, kind-aware) remotes concurrently.
///
/// Deduplicates by host, spawns one thread per unique remote, and returns
/// a `host -> reachable` map. Each remote is probed with the command
/// appropriate for its kind (see `probe_reachability_for_remote`).
pub fn probe_reachability_all_for_remotes(remotes: &[RemoteConfig]) -> HashMap<String, bool> {
    probe_with(remotes.iter().cloned(), probe_reachability_for_remote)
}

fn probe_with<I, F>(remotes: I, probe: F) -> HashMap<String, bool>
where
    I: IntoIterator<Item = RemoteConfig>,
    F: Fn(&RemoteConfig) -> bool + Clone + Send + 'static,
{
    let mut seen = HashSet::new();
    let unique: Vec<RemoteConfig> = remotes
        .into_iter()
        .filter(|r| seen.insert(r.host.clone()))
        .collect();

    let handles: Vec<(String, _)> = unique
        .into_iter()
        .map(|remote| {
            let probe = probe.clone();
            let host = remote.host.clone();
            let handle = thread::spawn(move || probe(&remote));
            (host, handle)
        })
        .collect();

    handles
        .into_iter()
        .filter_map(|(host, handle)| match handle.join() {
            Ok(reachable) => Some((host, reachable)),
            Err(_) => {
                crate::logger::LOG.warn(&format!(
                    "hosts: probe thread for {host} panicked; treating as unreachable"
                ));
                None
            }
        })
        .collect()
}

/// Runs per-reachable-host refresh work across all repos and their remotes.
///
/// For each `(repo, remote)` pair whose host appears in `reachable`, calls
/// `refresh_worktree(repo, remote)`. The first time each unique host is
/// encountered, also calls `refresh_tmux(repo, remote)`. Tmux is deduped per
/// host across all repos so it runs exactly once regardless of how many repos
/// share that host. The `(repo, remote)` pair handed to `refresh_tmux` is the
/// first one that introduced the host, which is what adapter-routed tmux
/// refresh needs to pick the right per-host cache file.
///
/// Callers supply the refresh strategy via closures, allowing both the
/// `--json` path (direct `sources::*` calls) and the TUI SWR path
/// (`cache_sources::*` calls) to share this loop without duplication.
///
/// # Arguments
///
/// * `config` - Global config supplying the repo × remote matrix to iterate.
/// * `reachable` - Set of host strings confirmed reachable by a prior
///   [`probe_reachability_all_for_remotes`] call.
/// * `refresh_worktree` - Called once per `(repo, remote)` pair whose host is
///   reachable.
/// * `refresh_tmux` - Called once per unique reachable host (deduplicated),
///   with the first `(repo, remote)` pair that introduced the host.
pub fn refresh_remotes_for_reachable_hosts<W, T>(
    config: &GlobalConfig,
    reachable: &HashSet<String>,
    mut refresh_worktree: W,
    mut refresh_tmux: T,
) where
    W: FnMut(&RepoConfig, &RemoteConfig),
    T: FnMut(&RepoConfig, &RemoteConfig),
{
    let mut tmux_refreshed: HashSet<String> = HashSet::new();
    for repo in &config.repos {
        for remote in &repo.remotes {
            if reachable.contains(&remote.host) {
                refresh_worktree(repo, remote);
                if tmux_refreshed.insert(remote.host.clone()) {
                    refresh_tmux(repo, remote);
                }
            }
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::global_config::{RemoteConfig, RepoConfig};
    use crate::remote_adapter::RemoteKind;
    use std::sync::Arc;
    use std::sync::atomic::{AtomicUsize, Ordering};
    use std::time::{Duration, Instant};

    fn remote(host: &str) -> RemoteConfig {
        RemoteConfig {
            name: "default".to_string(),
            host: host.to_string(),
            path: "/tmp/repo".to_string(),
            shell: "ssh".to_string(),
            kind: RemoteKind::Remmy,
        }
    }

    fn repo(slug: &str, hosts: &[&str]) -> RepoConfig {
        RepoConfig {
            slug: slug.to_string(),
            path: format!("/tmp/{slug}"),
            remotes: hosts.iter().copied().map(remote).collect(),
        }
    }

    /// Locks in the documented contract: `remotes_from_config` returns remotes
    /// in config-iteration order and *preserves duplicates across repos*. Dedup
    /// is the responsibility of `probe_reachability_all_for_remotes`, not this
    /// collector.
    #[test]
    fn remotes_from_config_preserves_order_and_cross_repo_duplicates() {
        let mut cfg = GlobalConfig::default();
        cfg.repos.push(repo("a/one", &["alpha", "bravo"]));
        cfg.repos.push(repo("b/two", &["charlie", "alpha"]));

        let hosts: Vec<String> = remotes_from_config(&cfg)
            .into_iter()
            .map(|r| r.host)
            .collect();

        assert_eq!(hosts, vec!["alpha", "bravo", "charlie", "alpha"]);
    }

    #[test]
    fn remotes_from_config_returns_empty_when_no_remotes() {
        let mut cfg = GlobalConfig::default();
        cfg.repos.push(repo("a/one", &[]));

        assert!(remotes_from_config(&cfg).is_empty());
    }

    /// Regression test for issue #263: serial probing lets one dead host block
    /// every probe behind it. Three probes × 200ms ≈ 200ms in parallel and
    /// ≈ 600ms strictly serial. Budget is `delay * 2` so the test still fails
    /// on *partial* serialization (e.g. 2-of-3 parallel + 1 serial ≈ 400ms),
    /// not just on fully-serial regressions, while leaving ~200ms of headroom
    /// for CI scheduler jitter.
    #[test]
    fn probes_run_concurrently() {
        let remotes = vec![remote("alpha"), remote("bravo"), remote("charlie")];
        let delay = Duration::from_millis(200);
        let budget = delay * 2;

        let start = Instant::now();
        let result = probe_with(remotes, move |_remote| {
            thread::sleep(delay);
            true
        });
        let elapsed = start.elapsed();

        assert_eq!(result.len(), 3);
        assert!(
            elapsed < budget,
            "expected concurrent dispatch (<{:?}); partial serialization would push past this, got {:?}",
            budget,
            elapsed
        );
    }

    #[test]
    fn probes_dedupe_hosts() {
        let probe_calls = Arc::new(AtomicUsize::new(0));
        let probe_calls_clone = probe_calls.clone();

        let remotes = vec![
            remote("alpha"),
            remote("alpha"),
            remote("bravo"),
            remote("alpha"),
        ];
        let result = probe_with(remotes, move |_remote| {
            probe_calls_clone.fetch_add(1, Ordering::SeqCst);
            true
        });

        assert_eq!(result.len(), 2, "expected 2 unique hosts, got {:?}", result);
        assert_eq!(
            probe_calls.load(Ordering::SeqCst),
            2,
            "probe should be called once per unique host"
        );
    }

    /// Records when each probe *starts*. In a serial implementation the second
    /// probe cannot start until the first finishes, so the live probe would
    /// start ~500ms after the dead one. In a parallel implementation both start
    /// within a few ms of each other. Asserting on the *relative* delay between
    /// the earliest start and the live start catches serial execution without
    /// depending on absolute dispatch latency (which varies on loaded CI).
    #[test]
    fn dead_host_does_not_delay_live_host_probe() {
        use std::sync::Mutex;
        let start_times: Arc<Mutex<Vec<(String, Duration)>>> = Arc::new(Mutex::new(Vec::new()));
        let start_times_clone = start_times.clone();
        let t0 = Instant::now();

        let remotes = vec![remote("dead"), remote("live")];
        let result = probe_with(remotes, move |r| {
            start_times_clone
                .lock()
                .unwrap()
                .push((r.host.clone(), t0.elapsed()));
            if r.host == "dead" {
                thread::sleep(Duration::from_millis(500));
                false
            } else {
                thread::sleep(Duration::from_millis(10));
                true
            }
        });

        assert_eq!(result.get("live"), Some(&true));
        assert_eq!(result.get("dead"), Some(&false));

        let times = start_times.lock().unwrap();
        let earliest_start = times
            .iter()
            .map(|(_, t)| *t)
            .min()
            .expect("at least one probe should have started");
        let live_start = times
            .iter()
            .find(|(h, _)| h == "live")
            .map(|(_, t)| *t)
            .expect("live probe should have started");
        let dispatch_gap = live_start.saturating_sub(earliest_start);
        assert!(
            dispatch_gap < Duration::from_millis(100),
            "live probe must dispatch within 100ms of the earliest probe \
             (serial would delay it ~500ms), gap was {:?}",
            dispatch_gap
        );
    }

    // -----------------------------------------------------------------------
    // refresh_remotes_for_reachable_hosts tests
    // -----------------------------------------------------------------------

    use std::cell::RefCell;
    use std::rc::Rc;

    fn config(repos: Vec<RepoConfig>) -> GlobalConfig {
        GlobalConfig {
            repos,
            ..Default::default()
        }
    }

    #[test]
    fn refresh_worktree_called_once_per_reachable_repo_remote_pair() {
        let cfg = config(vec![
            repo("owner/a", &["host1"]),
            repo("owner/b", &["host1", "host2"]),
        ]);
        let reachable: HashSet<String> = ["host1".to_string(), "host2".to_string()]
            .into_iter()
            .collect();

        let wt_calls: Rc<RefCell<Vec<(String, String)>>> = Rc::new(RefCell::new(Vec::new()));
        let wt_calls_ref = wt_calls.clone();

        refresh_remotes_for_reachable_hosts(
            &cfg,
            &reachable,
            |repo, remote| {
                wt_calls_ref
                    .borrow_mut()
                    .push((repo.slug.clone(), remote.host.clone()));
            },
            |_repo, _remote| {},
        );

        let calls = wt_calls.borrow();
        // 3 (repo, remote) pairs total: (a, host1), (b, host1), (b, host2)
        assert_eq!(calls.len(), 3);
        assert!(calls.contains(&("owner/a".to_string(), "host1".to_string())));
        assert!(calls.contains(&("owner/b".to_string(), "host1".to_string())));
        assert!(calls.contains(&("owner/b".to_string(), "host2".to_string())));
    }

    #[test]
    fn refresh_tmux_called_once_per_unique_reachable_host() {
        // host1 appears in two repos — tmux should only refresh it once.
        let cfg = config(vec![
            repo("owner/a", &["host1"]),
            repo("owner/b", &["host1", "host2"]),
        ]);
        let reachable: HashSet<String> = ["host1".to_string(), "host2".to_string()]
            .into_iter()
            .collect();

        let tmux_calls: Rc<RefCell<Vec<String>>> = Rc::new(RefCell::new(Vec::new()));
        let tmux_calls_ref = tmux_calls.clone();

        refresh_remotes_for_reachable_hosts(
            &cfg,
            &reachable,
            |_repo, _remote| {},
            |_repo, remote| {
                tmux_calls_ref.borrow_mut().push(remote.host.clone());
            },
        );

        let mut calls = tmux_calls.borrow().clone();
        calls.sort();
        assert_eq!(calls, vec!["host1".to_string(), "host2".to_string()]);
    }

    #[test]
    fn unreachable_hosts_skipped_entirely() {
        let cfg = config(vec![repo("owner/a", &["dead-host"])]);
        // Empty reachable set — nothing is reachable.
        let reachable: HashSet<String> = HashSet::new();

        let wt_calls: Rc<RefCell<u32>> = Rc::new(RefCell::new(0));
        let tmux_calls: Rc<RefCell<u32>> = Rc::new(RefCell::new(0));
        let wt_ref = wt_calls.clone();
        let tmux_ref = tmux_calls.clone();

        refresh_remotes_for_reachable_hosts(
            &cfg,
            &reachable,
            |_repo, _remote| *wt_ref.borrow_mut() += 1,
            |_repo, _remote| *tmux_ref.borrow_mut() += 1,
        );

        assert_eq!(
            *wt_calls.borrow(),
            0,
            "refresh_worktree must not be called for unreachable hosts"
        );
        assert_eq!(
            *tmux_calls.borrow(),
            0,
            "refresh_tmux must not be called for unreachable hosts"
        );
    }
}
