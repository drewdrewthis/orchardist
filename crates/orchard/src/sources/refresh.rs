//! Drives remote refresh given reachability — extracts the per-(repo, remote) loop
//! previously duplicated across `build_state::refresh_and_build` and
//! `tui::start_full_refresh`.
//!
//! Behavior contract (matches Site B today):
//! - Fork-host snapshot is taken BEFORE `refresh_remote_worktrees` per
//!   (repo, remote) — honors the documented contract at
//!   `cache_sources::snapshot_fork_hosts_for_remote`.
//! - Tmux refresh is deduped by `format!("{kind_str}:{host}")` (same as
//!   `tui::mod::dedup_key`), NOT by raw host.
//! - Worktree refresh is NOT deduped — it fires once per (repo, remote)
//!   regardless of host overlap.
//! - Parallelism is `std::thread::scope` flat per-(repo, remote) for worktrees
//!   + per first-seen `{kind}:{host}` for tmux. No inner threading inside
//!   `refresh_remote_*` (item 6 of #272 is out of scope).
//!
//! No `AppMsg` / `mpsc::Sender` coupling. Callers that need to emit messages
//! (Site B) do so BEFORE calling this helper.

use crate::global_config::GlobalConfig;
use std::collections::HashSet;

/// Drives remote-refresh work for every reachable (repo, remote) pair in `config`.
///
/// `reachable` is the set of host strings (e.g. `"user@host"`) that probed as
/// reachable. Unreachable hosts are skipped silently — the caller owns probe
/// emission (Site B emits `AppMsg::HostReachability` BEFORE calling this).
///
/// Side effects:
/// - For each reachable (repo, remote), takes a fork-host snapshot via
///   `cache_sources::snapshot_fork_hosts_for_remote` BEFORE worktree refresh.
/// - Spawns `cache_sources::refresh_remote_worktrees(repo, remote)` per
///   reachable pair, in a `std::thread::scope`.
/// - For each first-seen `"{kind}:{host}"` among the reachable pairs, spawns
///   `cache_sources::refresh_remote_tmux_sessions(repo, remote, &snapshot)`
///   with the snapshot captured before worktree refresh.
///
/// Returns when all spawned threads have joined.
pub fn refresh_remotes_for_reachable_hosts(config: &GlobalConfig, reachable: &HashSet<String>) {
    drive(config, reachable, &RealDriver);
}

/// Indirection trait so unit tests can record call ordering without touching
/// real caches. `RealDriver` is the only production impl.
trait RemoteRefreshDriver: Sync {
    fn snapshot_fork_hosts(
        &self,
        repo: &crate::global_config::RepoConfig,
        remote: &crate::global_config::RemoteConfig,
    ) -> HashSet<String>;

    fn refresh_worktrees(
        &self,
        repo: &crate::global_config::RepoConfig,
        remote: &crate::global_config::RemoteConfig,
    );

    fn refresh_tmux(
        &self,
        repo: &crate::global_config::RepoConfig,
        remote: &crate::global_config::RemoteConfig,
        old_hosts: &HashSet<String>,
    );
}

struct RealDriver;

impl RemoteRefreshDriver for RealDriver {
    fn snapshot_fork_hosts(
        &self,
        repo: &crate::global_config::RepoConfig,
        remote: &crate::global_config::RemoteConfig,
    ) -> HashSet<String> {
        crate::cache_sources::snapshot_fork_hosts_for_remote(repo, remote)
    }

    fn refresh_worktrees(
        &self,
        repo: &crate::global_config::RepoConfig,
        remote: &crate::global_config::RemoteConfig,
    ) {
        let _ = crate::cache_sources::refresh_remote_worktrees(repo, remote);
    }

    fn refresh_tmux(
        &self,
        repo: &crate::global_config::RepoConfig,
        remote: &crate::global_config::RemoteConfig,
        old_hosts: &HashSet<String>,
    ) {
        let _ = crate::cache_sources::refresh_remote_tmux_sessions(repo, remote, old_hosts);
    }
}

fn drive(config: &GlobalConfig, reachable: &HashSet<String>, driver: &dyn RemoteRefreshDriver) {
    // 1. Snapshot fork-hosts FIRST for every reachable (repo, remote) — before any
    //    refresh_remote_worktrees runs anywhere. This honors the documented
    //    contract at `cache_sources::snapshot_fork_hosts_for_remote` (must be
    //    called before `refresh_remote_worktrees` mutates the cache).
    let mut snapshots: Vec<(
        &crate::global_config::RepoConfig,
        &crate::global_config::RemoteConfig,
        HashSet<String>,
    )> = Vec::new();
    for repo in &config.repos {
        for remote in &repo.remotes {
            if reachable.contains(&remote.host) {
                let snap = driver.snapshot_fork_hosts(repo, remote);
                snapshots.push((repo, remote, snap));
            }
        }
    }

    // 2. Build the tmux dispatch list — first-seen "{kind}:{host}" only.
    let tmux_dispatch: Vec<(
        &crate::global_config::RepoConfig,
        &crate::global_config::RemoteConfig,
        &HashSet<String>,
    )> = {
        let mut seen = HashSet::<String>::new();
        snapshots
            .iter()
            .filter(|(_, remote, _)| {
                seen.insert(format!(
                    "{}:{}",
                    crate::cache_sources::kind_str(remote.kind),
                    remote.host
                ))
            })
            .map(|(repo, remote, snap)| (*repo, *remote, snap))
            .collect()
    };

    // 3. Fan out worktree + tmux refreshes concurrently.
    std::thread::scope(|s| {
        for (repo, remote, _snap) in &snapshots {
            s.spawn(move || driver.refresh_worktrees(repo, remote));
        }
        for (repo, remote, snap) in &tmux_dispatch {
            s.spawn(move || driver.refresh_tmux(repo, remote, snap));
        }
    });
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::global_config::{RemoteConfig, RepoConfig};
    use crate::remote_adapter::RemoteKind;
    use std::sync::Mutex;
    use std::time::Instant;

    fn make_remote(name: &str, host: &str, kind: RemoteKind) -> RemoteConfig {
        RemoteConfig {
            name: name.to_string(),
            host: host.to_string(),
            path: "/tmp/repo".to_string(),
            shell: "ssh".to_string(),
            kind,
            allow_transitive: false,
        }
    }

    fn make_repo(slug: &str, remotes: Vec<RemoteConfig>) -> RepoConfig {
        RepoConfig {
            slug: slug.to_string(),
            path: format!("/tmp/{slug}"),
            remotes,
        }
    }

    #[derive(Clone, Copy, Debug, PartialEq, Eq)]
    enum CallKind {
        Snapshot,
        Worktrees,
        Tmux,
    }

    /// Records the ordered sequence of calls and what tmux received as `old_hosts`.
    struct Recorder {
        calls: Mutex<Vec<(CallKind, String, String, Instant)>>, // (kind, repo_slug, remote_host, when)
        tmux_old_hosts: Mutex<Vec<(String, HashSet<String>)>>,  // (remote_host, snapshot)
        // What snapshot_fork_hosts returns for each host.
        snapshot_returns: std::collections::HashMap<String, HashSet<String>>,
    }

    impl Recorder {
        fn new(snapshot_returns: std::collections::HashMap<String, HashSet<String>>) -> Self {
            Self {
                calls: Mutex::new(Vec::new()),
                tmux_old_hosts: Mutex::new(Vec::new()),
                snapshot_returns,
            }
        }
    }

    impl RemoteRefreshDriver for Recorder {
        fn snapshot_fork_hosts(
            &self,
            repo: &RepoConfig,
            remote: &RemoteConfig,
        ) -> HashSet<String> {
            self.calls.lock().unwrap().push((
                CallKind::Snapshot,
                repo.slug.clone(),
                remote.host.clone(),
                Instant::now(),
            ));
            self.snapshot_returns
                .get(&remote.host)
                .cloned()
                .unwrap_or_default()
        }

        fn refresh_worktrees(&self, repo: &RepoConfig, remote: &RemoteConfig) {
            // Small sleep so any ordering bug surfaces deterministically — without
            // it, snapshot + worktree spawns can land at the same Instant.
            std::thread::sleep(std::time::Duration::from_millis(5));
            self.calls.lock().unwrap().push((
                CallKind::Worktrees,
                repo.slug.clone(),
                remote.host.clone(),
                Instant::now(),
            ));
        }

        fn refresh_tmux(
            &self,
            repo: &RepoConfig,
            remote: &RemoteConfig,
            old_hosts: &HashSet<String>,
        ) {
            self.calls.lock().unwrap().push((
                CallKind::Tmux,
                repo.slug.clone(),
                remote.host.clone(),
                Instant::now(),
            ));
            self.tmux_old_hosts
                .lock()
                .unwrap()
                .push((remote.host.clone(), old_hosts.clone()));
        }
    }

    /// AC4 / AC8: snapshot is taken BEFORE refresh_remote_worktrees per (repo, remote).
    #[test]
    fn snapshot_precedes_worktree_refresh_per_pair() {
        let cfg = {
            let mut c = crate::global_config::GlobalConfig::default();
            c.repos.push(make_repo(
                "a/one",
                vec![make_remote("r", "vm.boxd.sh", RemoteKind::BoxdFork)],
            ));
            c
        };
        let reachable: HashSet<String> = ["vm.boxd.sh".to_string()].into_iter().collect();
        let recorder = Recorder::new(
            [("vm.boxd.sh".to_string(), {
                let mut s = HashSet::new();
                s.insert("vm-stale.boxd.sh".to_string());
                s
            })]
            .into_iter()
            .collect(),
        );

        drive(&cfg, &reachable, &recorder);

        let calls = recorder.calls.lock().unwrap();
        // Find the earliest Worktrees call and the latest Snapshot call.
        let earliest_worktrees = calls
            .iter()
            .filter(|(k, _, _, _)| *k == CallKind::Worktrees)
            .map(|(_, _, _, t)| *t)
            .min()
            .expect("worktrees must have been called");
        let latest_snapshot = calls
            .iter()
            .filter(|(k, _, _, _)| *k == CallKind::Snapshot)
            .map(|(_, _, _, t)| *t)
            .max()
            .expect("snapshot must have been called");
        assert!(
            latest_snapshot <= earliest_worktrees,
            "every snapshot must precede every worktree refresh; got snapshot={:?}, worktrees={:?}",
            latest_snapshot,
            earliest_worktrees
        );

        // And the tmux call must have received the pre-mutation snapshot.
        let tmux_snaps = recorder.tmux_old_hosts.lock().unwrap();
        assert_eq!(tmux_snaps.len(), 1);
        assert_eq!(tmux_snaps[0].0, "vm.boxd.sh");
        assert!(tmux_snaps[0].1.contains("vm-stale.boxd.sh"));
    }

    /// AC3 / AC8: two remotes sharing a host but different kinds each get their own tmux refresh.
    #[test]
    fn tmux_dedup_by_kind_and_host_distinguishes_kinds() {
        let cfg = {
            let mut c = crate::global_config::GlobalConfig::default();
            c.repos.push(make_repo(
                "a/one",
                vec![
                    make_remote("r1", "vm.boxd.sh", RemoteKind::Remmy),
                    make_remote("r2", "vm.boxd.sh", RemoteKind::BoxdFork),
                ],
            ));
            c
        };
        let reachable: HashSet<String> = ["vm.boxd.sh".to_string()].into_iter().collect();
        let recorder = Recorder::new(Default::default());

        drive(&cfg, &reachable, &recorder);

        let calls = recorder.calls.lock().unwrap();
        let tmux_count = calls
            .iter()
            .filter(|(k, _, _, _)| *k == CallKind::Tmux)
            .count();
        assert_eq!(
            tmux_count, 2,
            "two remotes sharing host but different kinds must each refresh tmux; got {tmux_count}"
        );
        let worktree_count = calls
            .iter()
            .filter(|(k, _, _, _)| *k == CallKind::Worktrees)
            .count();
        assert_eq!(
            worktree_count, 2,
            "worktree refresh is not deduped by host; got {worktree_count}"
        );
    }

    /// AC3 / AC8: two repos with the same (kind, host) share one tmux refresh.
    #[test]
    fn tmux_dedup_by_kind_and_host_collapses_duplicates() {
        let cfg = {
            let mut c = crate::global_config::GlobalConfig::default();
            c.repos.push(make_repo(
                "a/one",
                vec![make_remote("r", "vm.boxd.sh", RemoteKind::BoxdFork)],
            ));
            c.repos.push(make_repo(
                "b/two",
                vec![make_remote("r", "vm.boxd.sh", RemoteKind::BoxdFork)],
            ));
            c
        };
        let reachable: HashSet<String> = ["vm.boxd.sh".to_string()].into_iter().collect();
        let recorder = Recorder::new(Default::default());

        drive(&cfg, &reachable, &recorder);

        let calls = recorder.calls.lock().unwrap();
        let tmux_count = calls
            .iter()
            .filter(|(k, _, _, _)| *k == CallKind::Tmux)
            .count();
        assert_eq!(
            tmux_count, 1,
            "two repos with same (kind, host) must share one tmux refresh; got {tmux_count}"
        );
        let worktree_count = calls
            .iter()
            .filter(|(k, _, _, _)| *k == CallKind::Worktrees)
            .count();
        assert_eq!(
            worktree_count, 2,
            "worktree refresh fires once per (repo, remote) regardless of host overlap; got {worktree_count}"
        );
    }

    /// AC1 / AC9: unreachable hosts are skipped silently — no calls at all.
    #[test]
    fn unreachable_hosts_are_skipped() {
        let cfg = {
            let mut c = crate::global_config::GlobalConfig::default();
            c.repos.push(make_repo(
                "a/one",
                vec![make_remote("r", "vm.boxd.sh", RemoteKind::Remmy)],
            ));
            c
        };
        let reachable: HashSet<String> = HashSet::new(); // empty
        let recorder = Recorder::new(Default::default());

        drive(&cfg, &reachable, &recorder);

        assert!(recorder.calls.lock().unwrap().is_empty());
    }
}
