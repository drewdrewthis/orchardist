//! Worktree prune — bulk remove based on a filter predicate.
//!
//! `prune` is a thin wrapper that lists worktrees, applies a caller-supplied
//! predicate, and calls [`crate::destroy::remove_worktree`] for each match.
//! It does not enrich worktrees with PR/issue data — callers that need to
//! prune by "PR merged" or "issue closed" must filter their *own* enriched
//! data and pass the matching paths in. This library is git-only.

use anyhow::Result;

use crate::destroy::remove_worktree;

/// Removes every worktree path in `paths`. Reports per-path success.
///
/// Returns `Vec<(path, Result)>` — one entry per input path. The caller can
/// inspect failures without losing the rest of the work. `force` is forwarded
/// to each `git worktree remove`.
pub fn prune(paths: &[&str], force: bool) -> Vec<(String, Result<()>)> {
    paths
        .iter()
        .map(|p| ((*p).to_string(), remove_worktree(p, force)))
        .collect()
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn prune_empty_returns_empty() {
        let outcomes = prune(&[], false);
        assert!(outcomes.is_empty());
    }
}
