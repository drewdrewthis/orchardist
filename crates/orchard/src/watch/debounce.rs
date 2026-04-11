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

    /// Advances debounce state and returns `(old_effective, new_effective)` — the
    /// confirmed status before and after this observation. This is the only way
    /// to safely read the pre-observation state: a separate `confirmed()` call
    /// followed by `observe()` creates a temporal-coupling trap.
    ///
    /// Rules:
    /// - First observation: prior = `None`, new = observed. Insert entry with
    ///   `confirmed = observed`, `pending = None`. Return `(None, observed)`.
    /// - Observed == confirmed: prior = confirmed, new = confirmed. Clear pending.
    ///   Return `(confirmed, confirmed)`.
    /// - Observed != confirmed and pending == observed: prior = confirmed, new =
    ///   observed (promoted). Update confirmed, clear pending.
    ///   Return `(old_confirmed, observed)`.
    /// - Observed != confirmed and pending != observed: prior = confirmed, new =
    ///   confirmed (suppressed). Record pending.
    ///   Return `(confirmed, confirmed)`.
    pub fn observe(&mut self, path: &str, observed: ClaudeState) -> (ClaudeState, ClaudeState) {
        match self.entries.get_mut(path) {
            None => {
                self.entries.insert(
                    path.to_string(),
                    DebounceEntry {
                        confirmed: observed,
                        pending: None,
                    },
                );
                (ClaudeState::None, observed)
            }
            Some(entry) => {
                let prior = entry.confirmed;
                if observed == entry.confirmed {
                    entry.pending = None;
                    (prior, prior)
                } else if entry.pending == Some(observed) {
                    entry.confirmed = observed;
                    entry.pending = None;
                    (prior, observed)
                } else {
                    entry.pending = Some(observed);
                    (prior, prior)
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
        let (prior, new) = d.observe("/workspace/repo/feat-1", ClaudeState::Working);
        assert_eq!(prior, ClaudeState::None, "no prior on first observation");
        assert_eq!(new, ClaudeState::Working, "first observation confirms Working");
    }

    #[test]
    fn observe_unchanged_returns_same() {
        let mut d = ClaudeDebounceState::new();
        let path = "/workspace/repo/feat-1";
        d.observe(path, ClaudeState::Working);
        let (_, new) = d.observe(path, ClaudeState::Working);
        assert_eq!(new, ClaudeState::Working);
    }

    #[test]
    fn single_blip_is_suppressed() {
        let mut d = ClaudeDebounceState::new();
        let path = "/workspace/repo/feat-1";

        // Cycle 1: Working confirmed immediately (first observation)
        let (_, r1) = d.observe(path, ClaudeState::Working);
        assert_eq!(r1, ClaudeState::Working, "first observation confirms Working");

        // Cycle 2: Input observed — first sighting, returns old confirmed
        let (_, r2) = d.observe(path, ClaudeState::Input);
        assert_eq!(r2, ClaudeState::Working, "single Input blip: still Working");

        // Cycle 3: Working again — pending (Input) doesn't match observed (Working),
        // so pending is replaced; confirmed stays Working
        let (_, r3) = d.observe(path, ClaudeState::Working);
        assert_eq!(r3, ClaudeState::Working, "return to Working: still Working");
    }

    #[test]
    fn two_poll_transition_is_confirmed() {
        let mut d = ClaudeDebounceState::new();
        let path = "/workspace/repo/feat-1";

        // Cycle 1: Working confirmed
        let (_, r1) = d.observe(path, ClaudeState::Working);
        assert_eq!(r1, ClaudeState::Working);

        // Cycle 2: Input first sighting — returns old confirmed
        let (_, r2) = d.observe(path, ClaudeState::Input);
        assert_eq!(r2, ClaudeState::Working, "first sighting of Input: still Working");

        // Cycle 3: Input again — pending matches observed, promote to confirmed
        let (_, r3) = d.observe(path, ClaudeState::Input);
        assert_eq!(r3, ClaudeState::Input, "Input confirmed after two cycles");
    }

    #[test]
    fn changed_pending_resets_window() {
        let mut d = ClaudeDebounceState::new();
        let path = "/workspace/repo/feat-1";

        // Cycle 1: Working confirmed
        d.observe(path, ClaudeState::Working);

        // Cycle 2: Input pending
        let (_, r2) = d.observe(path, ClaudeState::Input);
        assert_eq!(r2, ClaudeState::Working);

        // Cycle 3: Idle (different from pending Input) — resets window
        let (_, r3) = d.observe(path, ClaudeState::Idle);
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
        let (_, a) = d.observe(path_a, ClaudeState::Input);
        // path_b stays unchanged
        let (_, b) = d.observe(path_b, ClaudeState::Working);

        assert_eq!(a, ClaudeState::Working, "path_a blip suppressed");
        assert_eq!(b, ClaudeState::Working, "path_b unaffected");

        // Confirm Input for path_a
        let (_, a2) = d.observe(path_a, ClaudeState::Input);
        assert_eq!(a2, ClaudeState::Input, "path_a transitions after second cycle");

        // path_b still Working
        let (_, b2) = d.observe(path_b, ClaudeState::Working);
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

    #[test]
    fn retain_paths_fully_removes_entry_including_pending() {
        let mut d = ClaudeDebounceState::new();
        let drop_path = "/workspace/repo/feat-gone";

        // Set up an entry with both confirmed and pending populated.
        d.observe(drop_path, ClaudeState::Working); // confirmed = Working
        d.observe(drop_path, ClaudeState::Input); // pending = Some(Input)

        // Drop.
        d.retain_paths(|p| p != drop_path);

        // After drop, the entry is gone: confirmed reports None.
        assert_eq!(d.confirmed(drop_path), ClaudeState::None);

        // And observe behaves as if the path is brand-new (first-observation rule).
        let (prior, new) = d.observe(drop_path, ClaudeState::Idle);
        assert_eq!(prior, ClaudeState::None, "dropped entry should return None as prior");
        assert_eq!(new, ClaudeState::Idle, "dropped entry should first-observe fresh");
    }
}
