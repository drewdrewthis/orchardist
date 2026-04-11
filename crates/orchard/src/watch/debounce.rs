//! Claude status transition debouncer.
//!
//! Suppresses single-poll status flicker (e.g. working → input → working within
//! one cycle) by requiring a new status to persist across two consecutive diff
//! cycles before it is treated as the "effective" status for transition events.
//!
//! The TUI displays the raw status from each poll — only the watch event stream
//! is debounced.

use std::collections::HashMap;

use crate::claude_state::ClaudeState;

/// Per-worktree state for claude-status debouncing.
///
/// For each worktree, we track:
/// - `confirmed`: the last status that has persisted for at least one full cycle
///   and is safe to use for transition events
/// - `pending`: a new status seen in the most recent cycle that has not yet been
///   confirmed by a second cycle
#[derive(Debug, Clone, Default)]
pub struct ClaudeDebounceState {
    entries: HashMap<String, DebounceEntry>,
}

#[derive(Debug, Clone)]
struct DebounceEntry {
    confirmed: ClaudeState,
    pending: Option<ClaudeState>,
}

impl ClaudeDebounceState {
    /// Creates a new empty debounce state.
    pub fn new() -> Self {
        Self::default()
    }

    /// Looks up the confirmed (debounced) status for a worktree path.
    /// Returns `ClaudeState::None` if unknown.
    pub fn confirmed(&self, path: &str) -> ClaudeState {
        self.entries
            .get(path)
            .map(|e| e.confirmed)
            .unwrap_or(ClaudeState::None)
    }

    /// Advances debounce state based on the observed raw status for a worktree.
    /// Returns the effective (confirmed) status after this observation.
    ///
    /// Rules:
    /// - First observation: the status becomes immediately confirmed (no prior
    ///   baseline to debounce against).
    /// - Observed == confirmed: clear any pending, return confirmed.
    /// - Observed != confirmed and pending is None: record pending, return confirmed
    ///   (suppress transition — this is the first sighting).
    /// - Observed != confirmed and pending == observed: promote pending → confirmed,
    ///   return the new confirmed (transition now visible).
    /// - Observed != confirmed and pending != observed: replace pending with observed,
    ///   return confirmed (a different new value resets the debounce window).
    pub fn observe(&mut self, path: &str, observed: ClaudeState) -> ClaudeState {
        match self.entries.get_mut(path) {
            None => {
                self.entries.insert(
                    path.to_string(),
                    DebounceEntry {
                        confirmed: observed,
                        pending: None,
                    },
                );
                observed
            }
            Some(entry) => {
                if observed == entry.confirmed {
                    entry.pending = None;
                    entry.confirmed
                } else if entry.pending == Some(observed) {
                    entry.confirmed = observed;
                    entry.pending = None;
                    entry.confirmed
                } else {
                    entry.pending = Some(observed);
                    entry.confirmed
                }
            }
        }
    }

    /// Drops debounce state for worktrees that no longer exist.
    pub fn retain_paths<F: Fn(&str) -> bool>(&mut self, keep: F) {
        self.entries.retain(|k, _| keep(k));
    }
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn observe_first_time_confirms_immediately() {
        let mut d = ClaudeDebounceState::new();
        let result = d.observe("/workspace/repo/feat-1", ClaudeState::Working);
        assert_eq!(result, ClaudeState::Working);
    }

    #[test]
    fn observe_unchanged_returns_same() {
        let mut d = ClaudeDebounceState::new();
        let path = "/workspace/repo/feat-1";
        d.observe(path, ClaudeState::Working);
        let result = d.observe(path, ClaudeState::Working);
        assert_eq!(result, ClaudeState::Working);
    }

    #[test]
    fn single_blip_is_suppressed() {
        let mut d = ClaudeDebounceState::new();
        let path = "/workspace/repo/feat-1";

        // Cycle 1: Working confirmed immediately (first observation)
        let r1 = d.observe(path, ClaudeState::Working);
        assert_eq!(r1, ClaudeState::Working, "first observation confirms Working");

        // Cycle 2: Input observed — first sighting, returns old confirmed
        let r2 = d.observe(path, ClaudeState::Input);
        assert_eq!(r2, ClaudeState::Working, "single Input blip: still Working");

        // Cycle 3: Working again — pending (Input) doesn't match observed (Working),
        // so pending is replaced; confirmed stays Working
        let r3 = d.observe(path, ClaudeState::Working);
        assert_eq!(r3, ClaudeState::Working, "return to Working: still Working");
    }

    #[test]
    fn two_poll_transition_is_confirmed() {
        let mut d = ClaudeDebounceState::new();
        let path = "/workspace/repo/feat-1";

        // Cycle 1: Working confirmed
        let r1 = d.observe(path, ClaudeState::Working);
        assert_eq!(r1, ClaudeState::Working);

        // Cycle 2: Input first sighting — returns old confirmed
        let r2 = d.observe(path, ClaudeState::Input);
        assert_eq!(r2, ClaudeState::Working, "first sighting of Input: still Working");

        // Cycle 3: Input again — pending matches observed, promote to confirmed
        let r3 = d.observe(path, ClaudeState::Input);
        assert_eq!(r3, ClaudeState::Input, "Input confirmed after two cycles");
    }

    #[test]
    fn changed_pending_resets_window() {
        let mut d = ClaudeDebounceState::new();
        let path = "/workspace/repo/feat-1";

        // Cycle 1: Working confirmed
        d.observe(path, ClaudeState::Working);

        // Cycle 2: Input pending
        let r2 = d.observe(path, ClaudeState::Input);
        assert_eq!(r2, ClaudeState::Working);

        // Cycle 3: Idle (different from pending Input) — resets window
        let r3 = d.observe(path, ClaudeState::Idle);
        assert_eq!(r3, ClaudeState::Working, "different pending resets window");
    }

    #[test]
    fn different_worktrees_are_independent() {
        let mut d = ClaudeDebounceState::new();
        let path_a = "/workspace/repo/feat-1";
        let path_b = "/workspace/repo/feat-2";

        // Establish Working for both
        d.observe(path_a, ClaudeState::Working);
        d.observe(path_b, ClaudeState::Working);

        // Transition path_a to Input (first sighting)
        let a = d.observe(path_a, ClaudeState::Input);
        // path_b stays unchanged
        let b = d.observe(path_b, ClaudeState::Working);

        assert_eq!(a, ClaudeState::Working, "path_a blip suppressed");
        assert_eq!(b, ClaudeState::Working, "path_b unaffected");

        // Confirm Input for path_a
        let a2 = d.observe(path_a, ClaudeState::Input);
        assert_eq!(a2, ClaudeState::Input, "path_a transitions after second cycle");

        // path_b still Working
        let b2 = d.observe(path_b, ClaudeState::Working);
        assert_eq!(b2, ClaudeState::Working, "path_b still Working");
    }

    #[test]
    fn retain_paths_drops_removed_worktrees() {
        let mut d = ClaudeDebounceState::new();
        let keep = "/workspace/repo/feat-1";
        let drop = "/workspace/repo/feat-2";

        d.observe(keep, ClaudeState::Working);
        d.observe(drop, ClaudeState::Idle);

        d.retain_paths(|p| p == keep);

        // keep path still has confirmed state
        assert_eq!(d.confirmed(keep), ClaudeState::Working);
        // dropped path returns None
        assert_eq!(d.confirmed(drop), ClaudeState::None);
    }
}
