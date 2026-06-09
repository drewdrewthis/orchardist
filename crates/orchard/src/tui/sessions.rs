//! Tmux session lifecycle management for the TUI.
//!
//! Handles creation of main repo sessions, standalone sessions, and collision
//! detection between standalone session names and worktree-derived names.

use std::collections::HashMap;

use crate::cache;
use crate::derive;
use crate::global_config;
use crate::session::StandaloneSessionRow;
use crate::tmux;

// ---------------------------------------------------------------------------
// Main session auto-creation
// ---------------------------------------------------------------------------

/// A session that needs to be created for a repo.
#[derive(Debug, PartialEq)]
pub(super) struct SessionToCreate {
    /// Derived tmux session name (e.g. "orchardist_main").
    pub name: String,
    /// Absolute path on disk for the session start directory.
    pub start_dir: String,
    /// Slug of the repo this session belongs to (for error messages).
    pub repo_slug: String,
}

/// Pure function: given worktrees and existing sessions per repo, returns the
/// list of sessions that need to be created.
///
/// A session is needed when:
/// - The repo has at least one non-bare worktree (the origin).
/// - No existing session has the derived name.
pub(super) fn compute_sessions_to_create(
    repos: &[(
        String,                        // repo slug
        Vec<cache::CachedWorktree>,    // worktrees cache entries
        Vec<cache::CachedTmuxSession>, // existing local tmux sessions
    )],
) -> Vec<SessionToCreate> {
    let mut result = Vec::new();

    for (slug, worktrees, sessions) in repos {
        let origin = match worktrees.iter().find(|wt| !wt.is_bare) {
            Some(wt) => wt,
            None => continue,
        };

        let session_name = tmux::derive_main_session_name(&origin.path, Some(&origin.branch));

        if sessions.iter().any(|s| s.name == session_name) {
            continue;
        }

        result.push(SessionToCreate {
            name: session_name,
            start_dir: origin.path.clone(),
            repo_slug: slug.clone(),
        });
    }

    result
}

/// Ensures a main tmux session exists for each configured repo.
///
/// For each repo, reads the worktrees cache to find the origin (first non-bare
/// entry), then checks the local tmux sessions cache. If no session with the
/// derived name exists, creates one with `tmux::new_detached_session`.
///
/// Idempotent: skips repos whose session already exists.
/// Errors from individual repos are logged but do not block others.
///
/// The `cache_sources::refresh_tmux_sessions` call that previously followed
/// session creation has been removed (Phase 4, issue #429). The TUI no longer
/// reads the local tmux cache for its own state assembly — it fetches from the
/// daemon WorkView instead. After this function returns, the calling code path
/// triggers an `AppMsg::CacheRefreshed` or `AppMsg::LocalCacheRefreshed` tick
/// which calls `work_view()` from the daemon; the daemon scans tmux directly
/// and will include the newly-created session automatically.
pub(super) fn ensure_main_sessions(config: &global_config::GlobalConfig) {
    let existing_sessions =
        cache::read_cache::<cache::CachedTmuxSession>(&cache::tmux_cache_path(None)).entries;

    let repo_data: Vec<_> = config
        .repos
        .iter()
        .map(|repo| {
            let worktrees = cache::read_cache::<cache::CachedWorktree>(&cache::cache_path(
                repo.owner(),
                repo.repo_name(),
                "worktrees",
            ))
            .entries;
            (repo.slug.clone(), worktrees, existing_sessions.clone())
        })
        .collect();

    let to_create = compute_sessions_to_create(&repo_data);

    for session in &to_create {
        match tmux::new_detached_session(&session.name, &session.start_dir) {
            Ok(()) => {}
            Err(e) => {
                crate::logger::LOG.warn(&format!(
                    "ensure_main_sessions: failed to create session '{}' for repo '{}': {}",
                    session.name, session.repo_slug, e
                ));
            }
        }
    }
    // The `cache_sources::refresh_tmux_sessions` call that previously followed
    // session creation has been intentionally removed (Phase 4, issue #429).
    // The TUI reads state from the daemon WorkView, not from disk. The next
    // refresh tick will pick up newly-created sessions via work_view().
}

/// Creates standalone tmux sessions with `start_on_launch: true` if they don't already exist.
///
/// Returns an error if any session command fails immediately — broken config is a hard failure.
pub(super) fn ensure_standalone_sessions(
    config: &global_config::GlobalConfig,
) -> anyhow::Result<()> {
    for session_cfg in &config.tmux_sessions {
        if !session_cfg.start_on_launch {
            continue;
        }
        if tmux::session_exists(&session_cfg.name) {
            continue;
        }
        tmux::new_session_with_command(&session_cfg.name, &session_cfg.cwd, &session_cfg.command)
            .map_err(|e| {
            anyhow::anyhow!(
                "Failed to start standalone session '{}': {}",
                session_cfg.name,
                e
            )
        })?;
    }
    Ok(())
}

// ---------------------------------------------------------------------------
// Collision detection
// ---------------------------------------------------------------------------

/// Checks that no standalone session name collides with a worktree-derived session name.
///
/// Returns `Err` with a descriptive message if a collision is found. The error
/// identifies the standalone config name and the worktree branch that owns the
/// conflicting session, so the user knows exactly what to fix.
pub(super) fn check_standalone_collisions(
    standalone: &[StandaloneSessionRow],
    task_rows: &[derive::WorktreeRow],
) -> anyhow::Result<()> {
    // Build a map from session name → owning worktree branch for fast lookup.
    let mut wt_sessions: HashMap<&str, &str> = HashMap::new();
    for row in task_rows {
        for s in &row.sessions {
            wt_sessions.insert(s.tmux.name.as_str(), row.branch.as_str());
        }
    }

    for row in standalone {
        let name = &row.config.name;
        if let Some(branch) = wt_sessions.get(name.as_str()) {
            return Err(anyhow::anyhow!(
                "Standalone session '{}' collides with worktree session on branch '{}'",
                name,
                branch,
            ));
        }
    }
    Ok(())
}
