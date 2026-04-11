//! Pure state diffing for the watch system.
//!
//! Compares two `OrchardState` snapshots and returns the `WatchEvent`s
//! that represent transitions between them. No I/O occurs here.

use std::collections::{HashMap, HashSet};

use crate::claude_state::ClaudeState;
use crate::orchard_state::{OrchardState, WorktreeState};
use crate::watch::debounce::ClaudeDebounceState;
use crate::watch::event::{EventKind, WatchEvent};

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

/// Builds a map of worktree path → `WorktreeState` reference.
fn worktree_map(state: &OrchardState) -> HashMap<&str, &WorktreeState> {
    state
        .repos
        .iter()
        .flat_map(|r| r.worktrees.iter())
        .map(|wt| (wt.path.as_str(), wt))
        .collect()
}

/// Returns the dominant Claude status across all sessions in a worktree.
///
/// Priority: Working > Input > Idle > None.
fn claude_status(wt: &WorktreeState) -> ClaudeState {
    let mut best = ClaudeState::None;
    for session in &wt.sessions {
        let status = session
            .claude
            .as_ref()
            .map(|c| c.status)
            .unwrap_or(ClaudeState::None);
        match status {
            ClaudeState::Working => return ClaudeState::Working,
            ClaudeState::Input if best != ClaudeState::Working => best = ClaudeState::Input,
            ClaudeState::Idle if best != ClaudeState::Working && best != ClaudeState::Input => {
                best = ClaudeState::Idle;
            }
            _ => {}
        }
    }
    best
}

/// Returns the first session name in a worktree, or an empty string.
fn first_session(wt: &WorktreeState) -> String {
    wt.sessions
        .first()
        .map(|s| s.name.clone())
        .unwrap_or_default()
}

/// Returns `true` when a PR is approved, CI is passing, no conflicts, and no unresolved threads.
fn is_ready_to_merge(pr: &crate::orchard_state::PrState) -> bool {
    pr.review_decision.as_deref() == Some("approved")
        && pr.checks_state.as_deref() == Some("passing")
        && !pr.has_conflicts
        && pr.unresolved_threads == 0
}

/// Returns a human-readable label for a worktree: issue title if available, else branch.
fn label_for(wt: &WorktreeState) -> String {
    wt.issue
        .as_ref()
        .map(|i| i.title.clone())
        .unwrap_or_else(|| wt.branch.clone())
}

// ---------------------------------------------------------------------------
// Public diff function
// ---------------------------------------------------------------------------

/// Diffs two `OrchardState` snapshots and returns all detected `WatchEvent`s.
///
/// The `debounce` parameter is mutated to suppress single-poll Claude status
/// flicker: a new status must be observed in two consecutive diff cycles before
/// a transition event is emitted. The TUI displays raw status; only the event
/// stream is debounced.
pub fn diff(
    old: &OrchardState,
    new: &OrchardState,
    debounce: &mut ClaudeDebounceState,
) -> Vec<WatchEvent> {
    let mut events = Vec::new();

    let old_map = worktree_map(old);
    let new_map = worktree_map(new);

    // --- Worktrees added ---
    for (&path, &wt) in &new_map {
        if !old_map.contains_key(path) {
            events.push(WatchEvent::now(EventKind::WorktreeAdded {
                worktree: path.to_string(),
                branch: wt.branch.clone(),
            }));
        }
    }

    // --- Worktrees removed ---
    for (&path, &wt) in &old_map {
        if !new_map.contains_key(path) {
            events.push(WatchEvent::now(EventKind::WorktreeRemoved {
                worktree: path.to_string(),
                branch: wt.branch.clone(),
            }));
        }
    }

    // --- Per-worktree transitions ---
    for (&path, &new_wt) in &new_map {
        let Some(&old_wt) = old_map.get(path) else {
            continue;
        };

        let new_raw = claude_status(new_wt);
        let (old_effective, new_effective) = debounce.observe(path, new_raw);
        let label = label_for(new_wt);
        let session = first_session(new_wt);

        // Claude state transitions (debounced: new status must persist two cycles)
        match (old_effective, new_effective) {
            (ClaudeState::Working, ClaudeState::Input) => {
                events.push(WatchEvent::now(EventKind::ClaudeNeedsInput {
                    worktree: path.to_string(),
                    session,
                    label,
                }));
            }
            (ClaudeState::Working, ClaudeState::Idle | ClaudeState::None) => {
                events.push(WatchEvent::now(EventKind::ClaudeFinished {
                    worktree: path.to_string(),
                    session,
                    label,
                }));
            }
            (ClaudeState::Idle | ClaudeState::Input | ClaudeState::None, ClaudeState::Working) => {
                events.push(WatchEvent::now(EventKind::ClaudeStarted {
                    worktree: path.to_string(),
                    session,
                    label,
                }));
            }
            _ => {}
        }

        // PR-level transitions
        match (&old_wt.pr, &new_wt.pr) {
            (Some(old_pr), Some(new_pr)) => {
                let label = label_for(new_wt);
                let pr_number = new_pr.number;

                // CI failing transition
                if new_pr.checks_state.as_deref() == Some("failing")
                    && old_pr.checks_state.as_deref() != Some("failing")
                {
                    events.push(WatchEvent::now(EventKind::CiFailed {
                        worktree: path.to_string(),
                        pr_number,
                        label: label.clone(),
                    }));
                }

                // CI passing transition
                if new_pr.checks_state.as_deref() == Some("passing")
                    && old_pr.checks_state.as_deref() != Some("passing")
                {
                    events.push(WatchEvent::now(EventKind::CiPassed {
                        worktree: path.to_string(),
                        pr_number,
                        label: label.clone(),
                    }));
                }

                // New unresolved review threads
                if new_pr.unresolved_threads > 0 && old_pr.unresolved_threads == 0 {
                    events.push(WatchEvent::now(EventKind::ReviewComments {
                        worktree: path.to_string(),
                        pr_number,
                        thread_count: new_pr.unresolved_threads,
                        label: label.clone(),
                    }));
                }

                // PR merged
                if new_pr.state.as_deref() == Some("MERGED")
                    && old_pr.state.as_deref() != Some("MERGED")
                {
                    events.push(WatchEvent::now(EventKind::PrMerged {
                        worktree: path.to_string(),
                        pr_number,
                        label: label.clone(),
                    }));
                }

                // PR ready to merge: approved + passing + wasn't already in that state
                let new_ready = is_ready_to_merge(new_pr);
                let old_ready = is_ready_to_merge(old_pr);
                if new_ready && !old_ready {
                    events.push(WatchEvent::now(EventKind::PrReadyToMerge {
                        worktree: path.to_string(),
                        pr_number,
                        label,
                    }));
                }
            }
            (None, Some(new_pr)) => {
                // PR just appeared — check if it's already ready
                let label = label_for(new_wt);
                if is_ready_to_merge(new_pr) {
                    events.push(WatchEvent::now(EventKind::PrReadyToMerge {
                        worktree: path.to_string(),
                        pr_number: new_pr.number,
                        label,
                    }));
                }
            }
            _ => {}
        }
    }

    // Drop debounce state for worktrees that no longer exist.
    debounce.retain_paths(|p| new_map.contains_key(p));

    // --- Standalone session transitions ---
    let old_sessions: HashSet<&str> = old
        .standalone_sessions
        .iter()
        .map(|ss| ss.session.tmux.name.as_str())
        .collect();
    let new_sessions: HashSet<&str> = new
        .standalone_sessions
        .iter()
        .map(|ss| ss.session.tmux.name.as_str())
        .collect();

    for name in &new_sessions {
        if !old_sessions.contains(name) {
            events.push(WatchEvent::now(EventKind::SessionStarted {
                session: name.to_string(),
                worktree: None,
            }));
        }
    }

    for name in &old_sessions {
        if !new_sessions.contains(name) {
            events.push(WatchEvent::now(EventKind::SessionDied {
                session: name.to_string(),
                worktree: None,
            }));
        }
    }

    events
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

#[cfg(test)]
mod tests {
    use super::*;
    use crate::derive::DisplayGroup;
    use crate::orchard_state::{ClaudeEnrichment, PrState, RepoState, SessionState, WorktreeState};
    use crate::watch::debounce::ClaudeDebounceState;

    fn empty_state() -> OrchardState {
        OrchardState {
            repos: vec![],
            standalone_sessions: vec![],
            hosts: HashMap::new(),
        }
    }

    fn make_worktree(path: &str, branch: &str) -> WorktreeState {
        WorktreeState {
            path: path.to_string(),
            branch: branch.to_string(),
            is_bare: false,
            host: None,
            issue: None,
            pr: None,
            sessions: vec![],
            display_group: DisplayGroup::Other,
            is_main_worktree: false,
        }
    }

    fn with_claude(mut wt: WorktreeState, status: ClaudeState) -> WorktreeState {
        wt.sessions = vec![SessionState {
            name: "test_session".to_string(),
            host: None,
            claude: Some(ClaudeEnrichment {
                status,
                model: None,
                last_tool: None,
                current_task: None,
                session_start_ts: None,
                input_tokens: None,
                output_tokens: None,
                cache_creation_input_tokens: None,
                cache_read_input_tokens: None,
            }),
            windows: vec![],
        }];
        wt
    }

    fn with_pr(mut wt: WorktreeState, pr: PrState) -> WorktreeState {
        wt.pr = Some(pr);
        wt
    }

    fn make_state(worktrees: Vec<WorktreeState>) -> OrchardState {
        OrchardState {
            repos: vec![RepoState {
                slug: "test/repo".to_string(),
                worktrees,
            }],
            standalone_sessions: vec![],
            hosts: HashMap::new(),
        }
    }

    fn make_pr(number: u32) -> PrState {
        PrState {
            number,
            branch: "feat/branch".to_string(),
            state: Some("OPEN".to_string()),
            review_decision: None,
            checks_state: None,
            has_conflicts: false,
            unresolved_threads: 0,
            labels: vec![],
        }
    }

    /// Creates a debounce state where each worktree's current claude status is
    /// already confirmed. Used by tests that want to start from a known baseline
    /// so the next `diff` call exercises a single transition.
    fn seeded_debounce(state: &OrchardState) -> ClaudeDebounceState {
        let mut d = ClaudeDebounceState::new();
        for repo in &state.repos {
            for wt in &repo.worktrees {
                d.observe(&wt.path, claude_status(wt));
            }
        }
        d
    }

    #[test]
    fn diff_empty_states_returns_no_events() {
        let mut d = ClaudeDebounceState::new();
        let events = diff(&empty_state(), &empty_state(), &mut d);
        assert!(events.is_empty());
    }

    #[test]
    fn diff_detects_worktree_added() {
        let old = empty_state();
        let wt = make_worktree("/workspace/repo/feat-1", "feat/issue-1");
        let new = make_state(vec![wt]);

        let mut d = seeded_debounce(&old);
        let events = diff(&old, &new, &mut d);
        assert_eq!(events.len(), 1);
        assert!(matches!(
            &events[0].kind,
            EventKind::WorktreeAdded { worktree, .. } if worktree == "/workspace/repo/feat-1"
        ));
    }

    #[test]
    fn diff_detects_worktree_removed() {
        let wt = make_worktree("/workspace/repo/feat-1", "feat/issue-1");
        let old = make_state(vec![wt]);
        let new = empty_state();

        let mut d = seeded_debounce(&old);
        let events = diff(&old, &new, &mut d);
        assert_eq!(events.len(), 1);
        assert!(matches!(
            &events[0].kind,
            EventKind::WorktreeRemoved { worktree, .. } if worktree == "/workspace/repo/feat-1"
        ));
    }

    #[test]
    fn diff_detects_claude_working_to_input() {
        let path = "/workspace/repo/feat-1";
        let old_wt = with_claude(make_worktree(path, "feat/issue-1"), ClaudeState::Working);
        let new_wt = with_claude(make_worktree(path, "feat/issue-1"), ClaudeState::Input);
        let old_state = make_state(vec![old_wt]);
        let new_state = make_state(vec![new_wt]);

        // Prime debouncer with Working, then observe Input twice (required by 2-cycle rule).
        let mut d = seeded_debounce(&old_state);
        let _ = diff(&old_state, &new_state, &mut d); // first sighting of Input — suppressed
        let events = diff(&old_state, &new_state, &mut d); // second sighting — confirmed

        let input_events: Vec<_> = events
            .iter()
            .filter(|e| matches!(&e.kind, EventKind::ClaudeNeedsInput { .. }))
            .collect();
        assert_eq!(input_events.len(), 1);
    }

    #[test]
    fn diff_detects_claude_working_to_idle_as_finished() {
        let path = "/workspace/repo/feat-1";
        let old_wt = with_claude(make_worktree(path, "feat/issue-1"), ClaudeState::Working);
        let new_wt = with_claude(make_worktree(path, "feat/issue-1"), ClaudeState::Idle);
        let old_state = make_state(vec![old_wt]);
        let new_state = make_state(vec![new_wt]);

        let mut d = seeded_debounce(&old_state);
        let _ = diff(&old_state, &new_state, &mut d); // first sighting
        let events = diff(&old_state, &new_state, &mut d); // second sighting — confirmed

        let finished_events: Vec<_> = events
            .iter()
            .filter(|e| matches!(&e.kind, EventKind::ClaudeFinished { .. }))
            .collect();
        assert_eq!(finished_events.len(), 1);
    }

    #[test]
    fn diff_detects_claude_idle_to_working_as_started() {
        let path = "/workspace/repo/feat-1";
        let old_wt = with_claude(make_worktree(path, "feat/issue-1"), ClaudeState::Idle);
        let new_wt = with_claude(make_worktree(path, "feat/issue-1"), ClaudeState::Working);
        let old_state = make_state(vec![old_wt]);
        let new_state = make_state(vec![new_wt]);

        let mut d = seeded_debounce(&old_state);
        let _ = diff(&old_state, &new_state, &mut d); // first sighting
        let events = diff(&old_state, &new_state, &mut d); // second sighting — confirmed

        let started_events: Vec<_> = events
            .iter()
            .filter(|e| matches!(&e.kind, EventKind::ClaudeStarted { .. }))
            .collect();
        assert_eq!(started_events.len(), 1);
    }

    #[test]
    fn diff_detects_ci_transition_to_failing() {
        let path = "/workspace/repo/feat-1";
        let old_pr = make_pr(10);
        let new_pr = PrState {
            checks_state: Some("failing".to_string()),
            ..make_pr(10)
        };

        let old_wt = with_pr(make_worktree(path, "feat/issue-1"), old_pr);
        let new_wt = with_pr(make_worktree(path, "feat/issue-1"), new_pr);
        let old_state = make_state(vec![old_wt]);
        let new_state = make_state(vec![new_wt]);

        let mut d = seeded_debounce(&old_state);
        let events = diff(&old_state, &new_state, &mut d);

        let ci_events: Vec<_> = events
            .iter()
            .filter(|e| matches!(&e.kind, EventKind::CiFailed { .. }))
            .collect();
        assert_eq!(ci_events.len(), 1);
    }

    #[test]
    fn diff_ignores_ci_staying_at_failing() {
        let path = "/workspace/repo/feat-1";
        let old_pr = PrState {
            checks_state: Some("failing".to_string()),
            ..make_pr(10)
        };
        let new_pr = PrState {
            checks_state: Some("failing".to_string()),
            ..make_pr(10)
        };

        let old_wt = with_pr(make_worktree(path, "feat/issue-1"), old_pr);
        let new_wt = with_pr(make_worktree(path, "feat/issue-1"), new_pr);
        let old_state = make_state(vec![old_wt]);
        let new_state = make_state(vec![new_wt]);

        let mut d = seeded_debounce(&old_state);
        let events = diff(&old_state, &new_state, &mut d);

        let ci_events: Vec<_> = events
            .iter()
            .filter(|e| matches!(&e.kind, EventKind::CiFailed { .. }))
            .collect();
        assert!(ci_events.is_empty());
    }

    #[test]
    fn diff_detects_new_review_comments() {
        let path = "/workspace/repo/feat-1";
        let old_pr = make_pr(10);
        let new_pr = PrState {
            unresolved_threads: 2,
            ..make_pr(10)
        };

        let old_wt = with_pr(make_worktree(path, "feat/issue-1"), old_pr);
        let new_wt = with_pr(make_worktree(path, "feat/issue-1"), new_pr);
        let old_state = make_state(vec![old_wt]);
        let new_state = make_state(vec![new_wt]);

        let mut d = seeded_debounce(&old_state);
        let events = diff(&old_state, &new_state, &mut d);

        let review_events: Vec<_> = events
            .iter()
            .filter(|e| matches!(&e.kind, EventKind::ReviewComments { .. }))
            .collect();
        assert_eq!(review_events.len(), 1);
        assert!(matches!(
            &review_events[0].kind,
            EventKind::ReviewComments {
                thread_count: 2,
                ..
            }
        ));
    }

    #[test]
    fn diff_detects_pr_merged() {
        let path = "/workspace/repo/feat-1";
        let old_pr = make_pr(10);
        let new_pr = PrState {
            state: Some("MERGED".to_string()),
            ..make_pr(10)
        };

        let old_wt = with_pr(make_worktree(path, "feat/issue-1"), old_pr);
        let new_wt = with_pr(make_worktree(path, "feat/issue-1"), new_pr);
        let old_state = make_state(vec![old_wt]);
        let new_state = make_state(vec![new_wt]);

        let mut d = seeded_debounce(&old_state);
        let events = diff(&old_state, &new_state, &mut d);

        let merged_events: Vec<_> = events
            .iter()
            .filter(|e| matches!(&e.kind, EventKind::PrMerged { .. }))
            .collect();
        assert_eq!(merged_events.len(), 1);
    }

    #[test]
    fn diff_detects_ci_transition_to_passing() {
        let path = "/workspace/repo/feat-1";
        let old_pr = PrState {
            checks_state: Some("failing".to_string()),
            ..make_pr(10)
        };
        let new_pr = PrState {
            checks_state: Some("passing".to_string()),
            ..make_pr(10)
        };

        let old_wt = with_pr(make_worktree(path, "feat/issue-1"), old_pr);
        let new_wt = with_pr(make_worktree(path, "feat/issue-1"), new_pr);
        let old_state = make_state(vec![old_wt]);
        let new_state = make_state(vec![new_wt]);

        let mut d = seeded_debounce(&old_state);
        let events = diff(&old_state, &new_state, &mut d);
        let ci_events: Vec<_> = events
            .iter()
            .filter(|e| matches!(&e.kind, EventKind::CiPassed { .. }))
            .collect();
        assert_eq!(ci_events.len(), 1);
    }

    #[test]
    fn diff_detects_pr_ready_to_merge() {
        let path = "/workspace/repo/feat-1";
        let old_pr = make_pr(10);
        let new_pr = PrState {
            review_decision: Some("approved".to_string()),
            checks_state: Some("passing".to_string()),
            has_conflicts: false,
            unresolved_threads: 0,
            ..make_pr(10)
        };

        let old_wt = with_pr(make_worktree(path, "feat/issue-1"), old_pr);
        let new_wt = with_pr(make_worktree(path, "feat/issue-1"), new_pr);
        let old_state = make_state(vec![old_wt]);
        let new_state = make_state(vec![new_wt]);

        let mut d = seeded_debounce(&old_state);
        let events = diff(&old_state, &new_state, &mut d);
        let ready_events: Vec<_> = events
            .iter()
            .filter(|e| matches!(&e.kind, EventKind::PrReadyToMerge { .. }))
            .collect();
        assert_eq!(ready_events.len(), 1);
    }

    #[test]
    fn diff_detects_standalone_session_started() {
        use crate::session::{
            EnrichedSession, Host, SessionStatus, StandaloneConfig, StandaloneSessionRow,
            TmuxSessionInfo,
        };

        let old = empty_state();
        let new = OrchardState {
            repos: vec![],
            standalone_sessions: vec![StandaloneSessionRow {
                session: EnrichedSession {
                    tmux: TmuxSessionInfo {
                        host: Host::Local,
                        name: "orchardist".to_string(),
                        status: SessionStatus::Running { attached: false },
                    },
                    claude: None,
                    windows: vec![],
                    panes: vec![],
                },
                config: StandaloneConfig {
                    name: "orchardist".to_string(),
                    command: "claude".to_string(),
                    cwd: "/workspace".to_string(),
                    start_on_launch: true,
                },
            }],
            hosts: HashMap::new(),
        };

        let mut d = seeded_debounce(&old);
        let events = diff(&old, &new, &mut d);
        let started: Vec<_> = events
            .iter()
            .filter(|e| matches!(&e.kind, EventKind::SessionStarted { .. }))
            .collect();
        assert_eq!(started.len(), 1);
    }

    #[test]
    fn diff_detects_standalone_session_died() {
        use crate::session::{
            EnrichedSession, Host, SessionStatus, StandaloneConfig, StandaloneSessionRow,
            TmuxSessionInfo,
        };

        let old = OrchardState {
            repos: vec![],
            standalone_sessions: vec![StandaloneSessionRow {
                session: EnrichedSession {
                    tmux: TmuxSessionInfo {
                        host: Host::Local,
                        name: "orchardist".to_string(),
                        status: SessionStatus::Running { attached: false },
                    },
                    claude: None,
                    windows: vec![],
                    panes: vec![],
                },
                config: StandaloneConfig {
                    name: "orchardist".to_string(),
                    command: "claude".to_string(),
                    cwd: "/workspace".to_string(),
                    start_on_launch: true,
                },
            }],
            hosts: HashMap::new(),
        };
        let new = empty_state();

        let mut d = seeded_debounce(&old);
        let events = diff(&old, &new, &mut d);
        let died: Vec<_> = events
            .iter()
            .filter(|e| matches!(&e.kind, EventKind::SessionDied { .. }))
            .collect();
        assert_eq!(died.len(), 1);
    }

    #[test]
    fn diff_no_events_when_state_unchanged() {
        let path = "/workspace/repo/feat-1";
        let wt = with_claude(
            with_pr(make_worktree(path, "feat/issue-1"), make_pr(10)),
            ClaudeState::Working,
        );

        let state = make_state(vec![wt]);
        let mut d = seeded_debounce(&state);
        let events = diff(&state, &state, &mut d);
        assert!(events.is_empty());
    }

    // -----------------------------------------------------------------------
    // Debounce regression tests
    // -----------------------------------------------------------------------

    #[test]
    fn diff_debounces_single_poll_claude_flicker() {
        // Sequence: Working → Input → Working across three diff cycles.
        // Expect: zero ClaudeNeedsInput events emitted.
        let path = "/workspace/repo/feat-1";
        let mut debounce = ClaudeDebounceState::new();

        let s1 = make_state(vec![with_claude(
            make_worktree(path, "b"),
            ClaudeState::Working,
        )]);
        let s2 = make_state(vec![with_claude(
            make_worktree(path, "b"),
            ClaudeState::Input,
        )]);
        let s3 = make_state(vec![with_claude(
            make_worktree(path, "b"),
            ClaudeState::Working,
        )]);

        // Prime the debouncer: first diff call observes Working and confirms it.
        let _ = diff(&s1, &s1, &mut debounce);

        let events_1_to_2 = diff(&s1, &s2, &mut debounce);
        let events_2_to_3 = diff(&s2, &s3, &mut debounce);

        let flicker_events: Vec<_> = events_1_to_2
            .iter()
            .chain(events_2_to_3.iter())
            .filter(|e| {
                matches!(
                    &e.kind,
                    EventKind::ClaudeNeedsInput { .. } | EventKind::ClaudeFinished { .. }
                )
            })
            .collect();
        assert!(
            flicker_events.is_empty(),
            "single-poll flicker should not emit transition events, got {flicker_events:?}"
        );
    }

    #[test]
    fn diff_emits_transition_when_status_persists_two_cycles() {
        // Sequence: Working → Input → Input. Expect: one ClaudeNeedsInput after the second Input.
        let path = "/workspace/repo/feat-1";
        let mut debounce = ClaudeDebounceState::new();

        let s1 = make_state(vec![with_claude(
            make_worktree(path, "b"),
            ClaudeState::Working,
        )]);
        let s2 = make_state(vec![with_claude(
            make_worktree(path, "b"),
            ClaudeState::Input,
        )]);
        let s3 = make_state(vec![with_claude(
            make_worktree(path, "b"),
            ClaudeState::Input,
        )]);

        let _ = diff(&s1, &s1, &mut debounce);
        let events_1_to_2 = diff(&s1, &s2, &mut debounce);
        let events_2_to_3 = diff(&s2, &s3, &mut debounce);

        let input_events_1: Vec<_> = events_1_to_2
            .iter()
            .filter(|e| matches!(&e.kind, EventKind::ClaudeNeedsInput { .. }))
            .collect();
        let input_events_2: Vec<_> = events_2_to_3
            .iter()
            .filter(|e| matches!(&e.kind, EventKind::ClaudeNeedsInput { .. }))
            .collect();

        assert!(
            input_events_1.is_empty(),
            "no event on first sighting of Input"
        );
        assert_eq!(
            input_events_2.len(),
            1,
            "event fires when Input persists for a second cycle"
        );
    }
}
