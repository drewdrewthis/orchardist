//! Threshold checking for the watch system.
//!
//! Iterates all worktree sessions and fires `Threshold` events when a monitored
//! metric exceeds its configured limit. A cooldown period prevents repeated
//! notifications for the same metric on the same session.

use std::collections::HashMap;

use chrono::{DateTime, Utc};

use crate::global_config::WatchConfig;
use crate::orchard_state::OrchardState;
use crate::watch::event::{EventKind, WatchEvent};

/// Timestamps of the last threshold notification, keyed by `"path:metric"`.
pub type ThresholdTimestamps = HashMap<String, DateTime<Utc>>;

/// Checks all sessions in `state` against configured thresholds.
///
/// Returns the triggered `WatchEvent`s and an updated `ThresholdTimestamps`
/// map reflecting when each notification was last fired. This is a pure
/// function — no I/O occurs here.
pub fn check_thresholds(
    state: &OrchardState,
    config: &WatchConfig,
    prev_ts: &ThresholdTimestamps,
) -> (Vec<WatchEvent>, ThresholdTimestamps) {
    let now = Utc::now();
    let mut events = Vec::new();
    let mut new_ts = prev_ts.clone();

    for repo in &state.repos {
        for wt in &repo.worktrees {
            for session in &wt.sessions {
                let Some(ref claude) = session.claude else {
                    continue;
                };

                // Context window threshold
                if let Some(pct) = claude.context_window_pct
                    && pct >= config.context_window_threshold
                {
                    let key = format!("{}:context_window_pct", session.name);
                    if should_fire(prev_ts, &key, now, config.threshold_cooldown_secs) {
                        events.push(WatchEvent::now(EventKind::Threshold {
                            worktree: wt.path.clone(),
                            session: session.name.clone(),
                            metric: "context_window_pct".to_string(),
                            value: pct,
                            threshold: config.context_window_threshold,
                        }));
                        new_ts.insert(key, now);
                    }
                }

                // Cost threshold
                if let Some(cost) = claude.cost_usd
                    && cost >= config.cost_threshold
                {
                    let key = format!("{}:cost_usd", session.name);
                    if should_fire(prev_ts, &key, now, config.threshold_cooldown_secs) {
                        events.push(WatchEvent::now(EventKind::Threshold {
                            worktree: wt.path.clone(),
                            session: session.name.clone(),
                            metric: "cost_usd".to_string(),
                            value: cost,
                            threshold: config.cost_threshold,
                        }));
                        new_ts.insert(key, now);
                    }
                }
            }
        }
    }

    (events, new_ts)
}

/// Returns `true` if the threshold should fire — i.e., no prior timestamp or
/// enough time has elapsed since the last fire.
fn should_fire(
    prev_ts: &ThresholdTimestamps,
    key: &str,
    now: DateTime<Utc>,
    cooldown_secs: u64,
) -> bool {
    match prev_ts.get(key) {
        None => true,
        Some(&last) => {
            let elapsed = now.signed_duration_since(last).num_seconds();
            elapsed >= cooldown_secs as i64
        }
    }
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

#[cfg(test)]
mod tests {
    use super::*;
    use crate::claude_state::ClaudeState;
    use crate::derive::DisplayGroup;
    use crate::orchard_state::{ClaudeEnrichment, RepoState, SessionState, WorktreeState};

    fn make_config() -> WatchConfig {
        WatchConfig {
            local_poll_secs: 5,
            full_poll_secs: 60,
            context_window_threshold: 80.0,
            cost_threshold: 5.0,
            threshold_cooldown_secs: 300,
            notifications: true,
        }
    }

    fn make_state_with_session(
        path: &str,
        session_name: &str,
        context_pct: Option<f64>,
        cost_usd: Option<f64>,
    ) -> OrchardState {
        let session = SessionState {
            name: session_name.to_string(),
            host: None,
            claude: Some(ClaudeEnrichment {
                status: ClaudeState::Working,
                cost_usd,
                context_window_pct: context_pct,
                model: None,
            }),
            windows: vec![],
        };
        let wt = WorktreeState {
            path: path.to_string(),
            branch: "feat/branch".to_string(),
            is_bare: false,
            host: None,
            issue: None,
            pr: None,
            sessions: vec![session],
            display_group: DisplayGroup::Normal,
            is_main_worktree: false,
        };
        OrchardState {
            repos: vec![RepoState {
                slug: "test/repo".to_string(),
                worktrees: vec![wt],
            }],
            standalone_sessions: vec![],
            hosts: HashMap::new(),
        }
    }

    #[test]
    fn no_thresholds_exceeded_returns_no_events() {
        let state = make_state_with_session("/wt/a", "sess_a", Some(50.0), Some(1.0));
        let config = make_config();
        let (events, _) = check_thresholds(&state, &config, &ThresholdTimestamps::new());
        assert!(events.is_empty());
    }

    #[test]
    fn context_window_exceeds_threshold_fires_event() {
        let state = make_state_with_session("/wt/a", "sess_a", Some(85.0), None);
        let config = make_config();
        let (events, _) = check_thresholds(&state, &config, &ThresholdTimestamps::new());
        assert_eq!(events.len(), 1);
        assert!(matches!(
            &events[0].kind,
            EventKind::Threshold { metric, value, .. }
            if metric == "context_window_pct" && (*value - 85.0).abs() < f64::EPSILON
        ));
    }

    #[test]
    fn cost_exceeds_threshold_fires_event() {
        let state = make_state_with_session("/wt/a", "sess_a", None, Some(6.0));
        let config = make_config();
        let (events, _) = check_thresholds(&state, &config, &ThresholdTimestamps::new());
        assert_eq!(events.len(), 1);
        assert!(matches!(
            &events[0].kind,
            EventKind::Threshold { metric, .. } if metric == "cost_usd"
        ));
    }

    #[test]
    fn cooldown_suppresses_repeated_threshold() {
        let state = make_state_with_session("/wt/a", "sess_a", Some(90.0), None);
        let config = make_config();

        // First fire: no previous timestamp.
        let (events, new_ts) = check_thresholds(&state, &config, &ThresholdTimestamps::new());
        assert_eq!(events.len(), 1);

        // Second call with the just-updated timestamps — should be suppressed.
        let (events2, _) = check_thresholds(&state, &config, &new_ts);
        assert!(
            events2.is_empty(),
            "cooldown should suppress repeated event"
        );
    }

    #[test]
    fn cooldown_expired_fires_again() {
        let state = make_state_with_session("/wt/a", "sess_a", Some(90.0), None);
        let config = WatchConfig {
            threshold_cooldown_secs: 300,
            ..make_config()
        };

        // Simulate a "last fired" timestamp that is older than the cooldown.
        let mut old_ts = ThresholdTimestamps::new();
        let stale = Utc::now() - chrono::Duration::seconds(400);
        old_ts.insert("sess_a:context_window_pct".to_string(), stale);

        let (events, _) = check_thresholds(&state, &config, &old_ts);
        assert_eq!(events.len(), 1, "expired cooldown should allow re-firing");
    }
}
