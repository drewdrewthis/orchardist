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

use std::collections::HashMap;
use std::thread;

/// Probes whether a remote is reachable, using a probe command appropriate
/// for the remote's kind.
///
/// `Remmy` and `BoxdShared` reach a general-purpose shell on the remote host,
/// so `true` is a valid probe. `BoxdFork` targets the Boxd controller
/// (e.g. `boxd.sh`), which is a restricted CLI that rejects `true` —
/// `list --json` is the canonical health check there. Using the wrong probe
/// would mark a perfectly healthy golden host as unreachable and silence
/// every fork behind it.
pub fn probe_reachability_for_remote(remote: &crate::global_config::RemoteConfig) -> bool {
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
pub fn probe_reachability_all_for_remotes(
    remotes: &[crate::global_config::RemoteConfig],
) -> HashMap<String, bool> {
    let mut seen = std::collections::HashSet::new();
    let unique: Vec<crate::global_config::RemoteConfig> = remotes
        .iter()
        .filter(|r| seen.insert(r.host.clone()))
        .cloned()
        .collect();

    let handles: Vec<_> = unique
        .into_iter()
        .map(|remote| {
            thread::spawn(move || {
                let reachable = probe_reachability_for_remote(&remote);
                (remote.host, reachable)
            })
        })
        .collect();

    handles.into_iter().filter_map(|h| h.join().ok()).collect()
}

#[cfg(test)]
fn probe_with<I, S, F>(hosts: I, probe: F) -> HashMap<String, bool>
where
    I: IntoIterator<Item = S>,
    S: Into<String>,
    F: Fn(&str) -> bool + Clone + Send + 'static,
{
    let mut seen = std::collections::HashSet::new();
    let unique: Vec<String> = hosts
        .into_iter()
        .map(Into::into)
        .filter(|h| seen.insert(h.clone()))
        .collect();

    let handles: Vec<_> = unique
        .into_iter()
        .map(|host| {
            let probe = probe.clone();
            thread::spawn(move || {
                let reachable = probe(&host);
                (host, reachable)
            })
        })
        .collect();

    handles.into_iter().filter_map(|h| h.join().ok()).collect()
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::sync::Arc;
    use std::sync::atomic::{AtomicUsize, Ordering};
    use std::time::{Duration, Instant};

    /// Regression test for issue #263: serial probing lets one dead host block
    /// every probe behind it. Three probes × 200ms = 600ms if serial, but
    /// parallel dispatch finishes near the slowest (~200ms). Budget of 500ms
    /// catches serialization while tolerating CI scheduler jitter.
    #[test]
    fn probes_run_concurrently() {
        let hosts = vec!["alpha", "bravo", "charlie"];
        let delay = Duration::from_millis(200);

        let start = Instant::now();
        let result = probe_with(hosts, move |_host| {
            thread::sleep(delay);
            true
        });
        let elapsed = start.elapsed();

        assert_eq!(result.len(), 3);
        assert!(
            elapsed < Duration::from_millis(500),
            "expected concurrent dispatch (<500ms, serial would be ~600ms), got {:?}",
            elapsed
        );
    }

    #[test]
    fn probes_dedupe_hosts() {
        let probe_calls = Arc::new(AtomicUsize::new(0));
        let probe_calls_clone = probe_calls.clone();

        let hosts = vec!["alpha", "alpha", "bravo", "alpha"];
        let result = probe_with(hosts, move |_host| {
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
    /// probe cannot start until the first finishes, so start times are ≥500ms
    /// apart. In a parallel implementation both start within a few ms of each
    /// other. This distinction catches serial execution even when the total
    /// wall time is similar.
    #[test]
    fn dead_host_does_not_delay_live_host_probe() {
        use std::sync::Mutex;
        let start_times: Arc<Mutex<Vec<(String, Duration)>>> = Arc::new(Mutex::new(Vec::new()));
        let start_times_clone = start_times.clone();
        let t0 = Instant::now();

        let hosts = vec!["dead", "live"];
        let result = probe_with(hosts, move |host| {
            start_times_clone
                .lock()
                .unwrap()
                .push((host.to_string(), t0.elapsed()));
            if host == "dead" {
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
        let live_start = times
            .iter()
            .find(|(h, _)| h == "live")
            .map(|(_, t)| *t)
            .expect("live probe should have started");
        assert!(
            live_start < Duration::from_millis(100),
            "live probe must start within 100ms of dispatch (serial would delay it ~500ms), \
             started at {:?}",
            live_start
        );
    }
}
