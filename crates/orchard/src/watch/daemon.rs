//! Watch daemon loop.
//!
//! Polls local and full sources on configured intervals, diffs the resulting
//! `OrchardState`, fires threshold checks, delivers events to subscribers,
//! and optionally sends desktop notifications for key events.

use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::Arc;
use std::time::{Duration, Instant};

use crate::build_state;
use crate::cache_sources;
use crate::global_config::GlobalConfig;
use crate::orchard_state::OrchardState;
use crate::watch::diff;
use crate::watch::event::{EventKind, WatchEvent};
use crate::watch::subscription;
use crate::watch::threshold::{ThresholdTimestamps, check_thresholds};

// ---------------------------------------------------------------------------
// Public entry point
// ---------------------------------------------------------------------------

/// Runs the watch daemon until interrupted by Ctrl-C.
///
/// Polls local sources every `config.watch.local_poll_secs` seconds and full
/// sources every `config.watch.full_poll_secs` seconds. On each cycle it diffs
/// the state, fires threshold checks, delivers events to subscribers, and
/// optionally sends desktop notifications.
pub fn run(config: &GlobalConfig) -> anyhow::Result<()> {
    let running = Arc::new(AtomicBool::new(true));
    let r = running.clone();
    ctrlc::set_handler(move || {
        r.store(false, Ordering::SeqCst);
    })?;

    eprintln!(
        "orchard watch: starting daemon (local={}s, full={}s)",
        config.watch.local_poll_secs, config.watch.full_poll_secs
    );

    let mut threshold_ts = ThresholdTimestamps::new();
    let mut last_local = Instant::now();
    let mut last_full = Instant::now();

    // Force a full refresh on startup.
    refresh_all_sources(config);
    let initial = build_state::build_state(config);
    let mut previous_state: Option<OrchardState> = Some(initial);

    while running.load(Ordering::SeqCst) {
        std::thread::sleep(Duration::from_secs(1));

        let now = Instant::now();
        let do_full = now.duration_since(last_full).as_secs() >= config.watch.full_poll_secs;
        let do_local = now.duration_since(last_local).as_secs() >= config.watch.local_poll_secs;

        if do_full {
            refresh_all_sources(config);
            last_full = now;
            last_local = now;
        } else if do_local {
            refresh_local_sources(config);
            last_local = now;
        } else {
            continue;
        }

        let new_state = build_state::build_state(config);

        // Diff
        let mut events: Vec<WatchEvent> = Vec::new();
        if let Some(ref old) = previous_state {
            events.extend(diff::diff(old, &new_state));
        }

        // Thresholds
        let (threshold_events, new_ts) =
            check_thresholds(&new_state, &config.watch, &threshold_ts);
        events.extend(threshold_events);
        threshold_ts = new_ts;

        previous_state = Some(new_state.clone());

        // Emit a heartbeat on full refresh cycles when no other events fired.
        if do_full && events.is_empty() {
            let worktree_count = new_state.repos.iter().map(|r| r.worktrees.len()).sum();
            let session_count = new_state
                .repos
                .iter()
                .flat_map(|r| r.worktrees.iter())
                .map(|wt| wt.sessions.len())
                .sum::<usize>()
                + new_state.standalone_sessions.len();
            events.push(WatchEvent::now(EventKind::Heartbeat {
                worktree_count,
                session_count,
            }));
        }

        if events.is_empty() {
            continue;
        }

        // Log events
        for event in &events {
            log_watch_event(event);
        }

        // Desktop notifications
        if config.watch.notifications {
            fire_notifications(&events, config);
        }

        // Deliver to subscribers
        let subs_file = subscription::read_subscriptions();
        let pruned = subscription::prune_stale(&subs_file);
        // Persist the pruned list so dead sessions are removed on disk.
        let _ = subscription::write_subscriptions(&pruned);
        for sub in &pruned.subscriptions {
            if let Err(e) = subscription::deliver(sub, &events) {
                crate::logger::LOG.warn(&format!("watch: delivery to {} failed: {e}", sub.id));
            }
        }
    }

    eprintln!("orchard watch: stopped");
    Ok(())
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

/// Refreshes all sources: issues, PRs, worktrees, and tmux sessions for each repo.
fn refresh_all_sources(config: &GlobalConfig) {
    for repo in &config.repos {
        let _ = cache_sources::refresh_issues(repo);
        let _ = cache_sources::refresh_prs(repo);
        let _ = cache_sources::refresh_worktrees(repo);
    }
    let _ = cache_sources::refresh_tmux_sessions(None);
}

/// Refreshes only local (fast) sources: worktrees and tmux sessions.
fn refresh_local_sources(config: &GlobalConfig) {
    for repo in &config.repos {
        let _ = cache_sources::refresh_worktrees(repo);
    }
    let _ = cache_sources::refresh_tmux_sessions(None);
}

/// Sends desktop notifications for key event kinds.
pub fn fire_notifications(events: &[WatchEvent], config: &GlobalConfig) {
    let terminal_app = config.terminal_app.as_str();
    for event in events {
        match &event.kind {
            EventKind::ClaudeNeedsInput { session, label, .. } => {
                crate::notify::send_notification_with_session(
                    "Claude needs input",
                    &format!("{} is waiting for you", label),
                    Some(session.as_str()),
                    terminal_app,
                );
            }
            EventKind::ClaudeFinished { session, label, .. } => {
                crate::notify::send_notification_with_session(
                    "Claude finished",
                    label,
                    Some(session.as_str()),
                    terminal_app,
                );
            }
            EventKind::CiFailed {
                pr_number, label, ..
            } => {
                crate::notify::send_notification_with_session(
                    "CI Failed",
                    &format!("#{} {}", pr_number, label),
                    None,
                    terminal_app,
                );
            }
            EventKind::ReviewComments {
                pr_number,
                thread_count,
                ..
            } => {
                crate::notify::send_notification_with_session(
                    "Review comments",
                    &format!("#{} has {} unresolved thread(s)", pr_number, thread_count),
                    None,
                    terminal_app,
                );
            }
            _ => {}
        }
    }
}

/// Logs a watch event to the structured event log.
///
/// Extracts the `type` field from the serialized tagged enum instead of
/// maintaining a manual match table.
fn log_watch_event(event: &WatchEvent) {
    let details = serde_json::to_string(&event.kind).unwrap_or_default();
    let event_type = serde_json::from_str::<serde_json::Value>(&details)
        .ok()
        .and_then(|v| v.get("type").and_then(|t| t.as_str().map(String::from)))
        .unwrap_or_else(|| "unknown".to_string());
    crate::events::log_watch_event(&event_type, &details);
}
