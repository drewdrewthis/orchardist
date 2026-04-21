//! SSH host reachability probes.
//!
//! Simple SSH connectivity checks for remote hosts. Used to determine if a host is
//! reachable before attempting worktree or tmux operations on it. Probes enforce a
//! hard wall-clock deadline (see [`PROBE_TIMEOUT`]) via
//! [`crate::remote::ssh_exec_with_timeout`], so a frozen handshake or post-auth
//! hang can't exceed the budget.
//!
//! The concurrent entry point is [`probe_reachability_all_for_remotes`], which
//! accepts full `RemoteConfig` entries so each host is probed with the correct
//! command for its kind. Each probe runs on its own thread so a stopped VM
//! can't block probes for healthy hosts behind it.

use crate::global_config::{GlobalConfig, RemoteConfig};
use std::collections::HashMap;
use std::thread;
use std::time::Duration;

/// Hard wall-clock deadline for a single host reachability probe.
///
/// Tighter than SSH's own `ConnectTimeout=5` so that `orchard --json`
/// can complete in under 5s even when every configured host is dead
/// (see #246 ACs #4/#5). A silently-dropping VM or hung remote sshd
/// can otherwise let the probe run well past the intended budget;
/// wrapping the probe in [`crate::remote::ssh_exec_with_timeout`]
/// forces a kill after this deadline regardless of SSH's internal state.
pub const PROBE_TIMEOUT: Duration = Duration::from_secs(3);

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

/// Returns the probe command appropriate for a given remote kind.
///
/// Each remote kind requires a different health-check command:
/// - `Remmy` / `BoxdShared`: general-purpose shell accepts `true` as a no-op probe.
/// - `BoxdFork`: restricted Boxd CLI rejects `true`; `list --json` is the canonical check.
/// - `OrchardProxy`: must be probed with an orchard-specific command (`orchard --version`)
///   so that the probe exercises the orchard binary, not just SSH connectivity (AC7).
pub fn probe_command_for(kind: crate::remote_adapter::RemoteKind) -> &'static str {
    match kind {
        crate::remote_adapter::RemoteKind::BoxdFork => "list --json",
        crate::remote_adapter::RemoteKind::Remmy
        | crate::remote_adapter::RemoteKind::BoxdShared => "true",
        // AC7: OrchardProxy requires an orchard-specific probe — verifies the orchard
        // binary is present and responsive, not just that SSH itself works.
        crate::remote_adapter::RemoteKind::OrchardProxy => "orchard --version",
    }
}

/// Probes whether a remote is reachable, using a probe command appropriate
/// for the remote's kind.
///
/// `Remmy` and `BoxdShared` reach a general-purpose shell on the remote host,
/// so `true` is a valid probe. `BoxdFork` targets the Boxd controller
/// (e.g. `boxd.sh`), which is a restricted CLI that rejects `true` —
/// `list --json` is the canonical health check there. `OrchardProxy` uses
/// `orchard --version` to verify the orchard binary is present and responsive
/// (AC7 mandate), not merely that SSH connectivity exists.
///
/// Returns `true` if the host responds within [`PROBE_TIMEOUT`], `false` if
/// the host is unreachable, SSH fails, or the wall-clock deadline expires.
pub fn probe_reachability_for_remote(remote: &RemoteConfig) -> bool {
    let cmd = probe_command_for(remote.kind);
    crate::remote::ssh_exec_with_timeout(&remote.host, cmd, PROBE_TIMEOUT).is_ok()
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
    let mut seen = std::collections::HashSet::new();
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
            fallback_kind: None,
        }
    }

    fn repo(slug: &str, hosts: &[&str]) -> RepoConfig {
        RepoConfig {
            slug: slug.to_string(),
            path: format!("/tmp/{slug}"),
            remotes: hosts.iter().copied().map(remote).collect(),
        }
    }

    fn ssh_binary_present() -> bool {
        std::process::Command::new("ssh").arg("-V").output().is_ok()
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

    /// Regression test for issue #246: `probe_reachability_for_remote` must
    /// enforce a hard wall-clock deadline, not trust SSH's `ConnectTimeout=5`
    /// alone. A frozen handshake or a post-auth hang previously let a single
    /// probe exceed 15s. With `ssh_exec_with_timeout`, the probe returns
    /// within `PROBE_TIMEOUT` + small jitter regardless of the underlying SSH
    /// state. 192.0.2.1 (TEST-NET-1, RFC 5737) is guaranteed unroutable.
    #[test]
    fn probe_reachability_enforces_hard_deadline() {
        if !ssh_binary_present() {
            eprintln!("SKIP: ssh binary not available");
            return;
        }

        let remote = crate::global_config::RemoteConfig {
            name: "test".to_string(),
            host: "192.0.2.1".to_string(),
            path: "/tmp".to_string(),
            shell: "ssh".to_string(),
            kind: crate::remote_adapter::RemoteKind::Remmy,
            fallback_kind: None,
        };

        let start = Instant::now();
        let reachable = probe_reachability_for_remote(&remote);
        let elapsed = start.elapsed();

        assert!(!reachable, "unroutable host must probe as unreachable");
        assert!(
            elapsed < PROBE_TIMEOUT + Duration::from_millis(1500),
            "probe must respect PROBE_TIMEOUT ({:?}); got {:?}",
            PROBE_TIMEOUT,
            elapsed
        );
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

    // -----------------------------------------------------------------------
    // AC7 — OrchardProxy probe command + timeout tests
    // -----------------------------------------------------------------------

    /// AC7 scenario 1: the command selected for OrchardProxy must be an
    /// orchard-specific probe, not the generic `true` used for Remmy/BoxdShared.
    ///
    /// This is a pure unit test — no SSH is exercised. `probe_command_for` is a
    /// pure mapping function so it can be asserted without spinning up SSH.
    #[test]
    fn probe_command_for_orchard_proxy_is_not_true() {
        let cmd = probe_command_for(RemoteKind::OrchardProxy);
        assert_ne!(
            cmd, "true",
            "OrchardProxy must NOT use the generic 'true' probe (AC7)"
        );
        // Must contain "orchard" so it exercises the remote orchard binary.
        assert!(
            cmd.contains("orchard"),
            "OrchardProxy probe must be an orchard-specific command; got {:?}",
            cmd
        );
    }

    /// Sanity check: Remmy still uses the generic `true` probe (ensures the
    /// AC7 change is targeted and does not regress other kinds).
    #[test]
    fn probe_command_for_remmy_is_still_true() {
        assert_eq!(probe_command_for(RemoteKind::Remmy), "true");
        assert_eq!(probe_command_for(RemoteKind::BoxdShared), "true");
    }

    /// AC7 scenario 2: an OrchardProxy probe against an unroutable host must
    /// complete within PROBE_TIMEOUT. Mirrors `probe_reachability_enforces_hard_deadline`
    /// but explicitly uses RemoteKind::OrchardProxy so the orchard-specific command
    /// path is tested end-to-end.
    #[test]
    fn orchard_proxy_probe_is_bounded_by_probe_timeout() {
        if !ssh_binary_present() {
            eprintln!("SKIP: ssh binary not available");
            return;
        }

        let remote = crate::global_config::RemoteConfig {
            name: "orchard-proxy-test".to_string(),
            host: "192.0.2.2".to_string(), // TEST-NET-1 (RFC 5737) — guaranteed unroutable
            path: "/tmp".to_string(),
            shell: "ssh".to_string(),
            kind: RemoteKind::OrchardProxy,
            fallback_kind: None,
        };

        let start = Instant::now();
        let reachable = probe_reachability_for_remote(&remote);
        let elapsed = start.elapsed();

        assert!(!reachable, "unroutable OrchardProxy host must probe as unreachable");
        assert!(
            elapsed < PROBE_TIMEOUT + Duration::from_millis(1500),
            "OrchardProxy probe must respect PROBE_TIMEOUT ({:?}); got {:?} (AC7)",
            PROBE_TIMEOUT,
            elapsed
        );
    }

    /// AC7 scenario 3 (e2e): three OrchardProxy remotes against unroutable hosts
    /// must all complete (returning unreachable) in under 5 seconds total when
    /// run via `probe_reachability_all_for_remotes`. Validates that concurrent
    /// dispatch + PROBE_TIMEOUT keeps the combined wall-clock within the budget.
    #[test]
    fn three_orchard_proxy_probes_complete_under_5s() {
        if !ssh_binary_present() {
            eprintln!("SKIP: ssh binary not available");
            return;
        }

        // Three distinct TEST-NET-1 addresses (RFC 5737) — unroutable, won't
        // accidentally hit a real host, guaranteed to time out rather than refuse.
        let remotes: Vec<RemoteConfig> = ["192.0.2.3", "192.0.2.4", "192.0.2.5"]
            .iter()
            .enumerate()
            .map(|(i, &host)| RemoteConfig {
                name: format!("orchard-proxy-{i}"),
                host: host.to_string(),
                path: "/tmp".to_string(),
                shell: "ssh".to_string(),
                kind: RemoteKind::OrchardProxy,
                fallback_kind: None,
            })
            .collect();

        let start = Instant::now();
        let result = probe_reachability_all_for_remotes(&remotes);
        let elapsed = start.elapsed();

        assert_eq!(result.len(), 3, "all 3 hosts must appear in the result map");
        for (host, reachable) in &result {
            assert!(!reachable, "unroutable host {host} must be unreachable");
        }
        assert!(
            elapsed < Duration::from_secs(5),
            "3 concurrent OrchardProxy probes must complete under 5s total; got {:?} (AC7)",
            elapsed
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
}
