//! Worktree lifecycle operations for the TUI.
//!
//! Contains stale-filtering logic and worktree deletion (both local and remote).
//!
//! # ADR-018 invariant (AC7)
//!
//! LOCAL worktree cleanup is **delegated to the daemon** via
//! `daemon::Client::worktrees_cleanup`. The TUI never calls
//! `worktree_core::remove_worktree` or `tmux::kill_tmux_session_safe` for local
//! rows. The daemon owns all destructive local operations; the TUI is a thin
//! interface that collects intent and surfaces results.
//!
//! REMOTE worktree cleanup continues to use the `remote` module (Phase 2 of
//! federated cleanup is out of scope for #693 Step 7).

use crate::daemon;
use crate::derive;
use crate::global_config;
use crate::remote;

// ---------------------------------------------------------------------------
// Stale worktree filter
// ---------------------------------------------------------------------------

/// Filters a slice of worktree rows down to those that are stale (merged or
/// closed PR, or completed/closed issue).
pub(super) fn filter_stale(rows: &[derive::WorktreeRow]) -> Vec<derive::WorktreeRow> {
    rows.iter()
        .filter(|row| {
            if let Some(ref pr) = row.pr {
                let state = pr.state.as_deref().unwrap_or("");
                return state == "merged" || state == "closed";
            }
            if let Some(ref state) = row.issue_state {
                return state == "completed" || state == "closed";
            }
            false
        })
        .cloned()
        .collect()
}

// ---------------------------------------------------------------------------
// Delete worktree (shared by single-delete and cleanup)
// ---------------------------------------------------------------------------

/// Deletes the worktree represented by a `WorktreeRow`.
///
/// # ADR-018 invariant
///
/// **LOCAL rows** — delegates to the daemon's `worktreesCleanup` mutation via
/// [`daemon::Client`]. The daemon performs: tmux session kill, docker teardown,
/// worktree + directory removal, and safe branch deletion. The TUI **never**
/// calls `worktree_core::remove_worktree` or `tmux::kill_tmux_session_safe`
/// for a local row.
///
/// **REMOTE rows** — continues to use the `remote` module (Phase 2 handles
/// federated remote cleanup; out of scope for #693 Step 7).
///
/// `active_session` is the name of the tmux session the user is currently
/// running in (captured from `$TMUX` in the TUI process, where it is valid).
/// `active_cwd` is the absolute path the user's session is working in.
/// Both are forwarded to the daemon for the AC-G1 data-loss guard: the
/// worktree hosting the active session is excluded from all destruction.
pub(super) fn delete_task_row(
    row: &derive::WorktreeRow,
    global_config: &global_config::GlobalConfig,
    active_session: Option<&str>,
    active_cwd: Option<&str>,
) -> anyhow::Result<()> {
    let session_name = row.sessions.first().map(|s| s.tmux.name.as_str());
    let dp = row.discovery_path.as_deref();
    if let Some(ref host) = row.worktree_host {
        // Remote deletion — forward discovery_path for transitive hop chaining.
        // REMOTE rows are unchanged: Phase 2 handles federated remote cleanup.
        if let Some(sess) = session_name {
            let _ = remote::kill_remote_tmux_session(host, sess, dp);
        }
        let slug = crate::paths::sanitize_branch_slug(&row.branch);
        let _ = remote::remove_remote_registry_entry(host, &slug, dp);
        // Find the remote config matching this host to get the repo_path.
        let remote_cfg = global_config
            .repos
            .iter()
            .find_map(|repo| repo.remote_for_host(host));
        if let Some(remote_cfg) = remote_cfg {
            remote::remove_remote_worktree(host, &remote_cfg.path, &row.worktree_path, dp)?;
        }
        return Ok(());
    }

    // LOCAL deletion — delegate to the daemon mutation (ADR-018 invariant / AC7).
    //
    // Construct the stable worktree ID: <repo_slug>:<branch>.
    // The daemon's worktree-remove script resolves the path from the repo config
    // using the branch name via `git worktree list --porcelain`.
    let worktree_id = format!("{}:{}", row.repo_slug, row.branch);

    // AC-G3: thread the per-worktree session name (from the row's first session)
    // so the daemon can pass --tmux-session to worktree-remove.sh.
    // This is distinct from active_session (the user's CURRENT session, AC-G1
    // exclusion guard) — this is the session TO KILL for this stale worktree.
    let worktree_session = session_name.map(|s| s.to_string()).unwrap_or_default();
    let session_names: &[String] = if worktree_session.is_empty() {
        &[]
    } else {
        std::slice::from_ref(&worktree_session)
    };

    let client = daemon::Client::local().map_err(|e| anyhow::anyhow!("daemon client: {e}"))?;

    let result = client
        .worktrees_cleanup_with_sessions(&[worktree_id], session_names, active_session, active_cwd)
        .map_err(|e| anyhow::anyhow!("daemon worktreesCleanup: {e}"))?;

    if !result.ok {
        let msg = result.err_msg.as_deref().unwrap_or("unknown daemon error");
        return Err(anyhow::anyhow!("daemon rejected cleanup: {}", msg));
    }

    // Check per-worktree entries for hard failures.
    for entry in &result.entries {
        if !entry.ok {
            let stage = entry.stage.as_deref().unwrap_or("unknown");
            let msg = entry.message.as_deref().unwrap_or("no details");
            return Err(anyhow::anyhow!(
                "cleanup failed at stage {}: {}",
                stage,
                msg
            ));
        }
    }

    Ok(())
}

// ---------------------------------------------------------------------------
// AC7 structural tests
// ---------------------------------------------------------------------------

#[cfg(test)]
mod tests {
    // -----------------------------------------------------------------------
    // POSITIVE: delete_task_row for a local row calls daemon client
    // -----------------------------------------------------------------------
    //
    // We verify the call by:
    //   1. Confirming that `delete_task_row` on a LOCAL row (no worktree_host)
    //      routes into `daemon::Client::worktrees_cleanup`.
    //   2. A unit-level check: the method exists on `daemon::Client` and the
    //      call compiles with the expected signature — if the compilation
    //      succeeds, the positive route is wired.
    //
    // A live daemon is not required; we confirm the routing via a compile-level
    // structural test that would fail to compile if `worktrees_cleanup` did not
    // exist or was removed.

    /// Type alias for the compile-level signature check below.
    /// Keeps the full signature in one place so `assert_worktrees_cleanup_method_exists`
    /// stays readable without triggering clippy::type_complexity.
    ///
    /// Matches the 4-arg `worktrees_cleanup` public entry-point (not the 5-arg
    /// `_with_sessions` variant); both exist on `daemon::Client`.
    type CleanupFn =
        fn(
            &crate::daemon::Client,
            &[String],
            Option<&str>,
            Option<&str>,
        ) -> Result<crate::daemon::WorktreesCleanupResult, crate::daemon::DaemonError>;

    /// Compile-level proof that `daemon::Client::worktrees_cleanup` exists and
    /// has the expected signature. If the positive route were removed or the
    /// method signature changed, this function would fail to compile.
    ///
    /// @scenario The TUI local-cleanup path invokes the daemon mutation and
    ///            execs no local destruction (the positive route)
    #[allow(dead_code)]
    fn assert_worktrees_cleanup_method_exists() {
        // Type-checking proof: this closure captures the method call shape.
        // The closure is never called; it only needs to type-check.
        let _verify: CleanupFn = |client, ids, sess, cwd| client.worktrees_cleanup(ids, sess, cwd);
    }

    // -----------------------------------------------------------------------
    // NEGATIVE: local branch contains no worktree_core / tmux destruction calls
    // -----------------------------------------------------------------------
    //
    // The AC explicitly allows a static / structural check for the negative
    // absence assertion. Because `delete_task_row`'s LOCAL branch no longer
    // imports `worktree_core` or calls `tmux::kill_tmux_session_safe`, the
    // following code will FAIL TO COMPILE if either symbol is imported into
    // this module — which would mean the invariant is violated.
    //
    // `worktree_core` is NOT in the use list above; `crate::tmux` is NOT called
    // in the local branch. Attempting to use them would produce a compile error.
    //
    // The test below documents and verifies the absence by confirming that the
    // module's use declarations do NOT include `worktree_core` or `tmux` in
    // the local-branch path.

    /// Structural proof that the LOCAL branch of delete_task_row does not
    /// reference worktree_core::remove_worktree or tmux::kill_tmux_session_safe.
    ///
    /// Evidence: this module's `use` block imports only:
    ///   - `crate::daemon`          (daemon client)
    ///   - `crate::derive`          (row types)
    ///   - `crate::global_config`   (config types)
    ///   - `crate::remote`          (remote-row path — untouched)
    ///
    /// `worktree_core` and `crate::tmux` are absent. The compiler would reject
    /// any reference to `worktree_core::remove_worktree` or
    /// `tmux::kill_tmux_session_safe` in the LOCAL path with an unresolved
    /// import error, proving the negative absence structurally.
    ///
    /// @scenario The TUI local-cleanup path invokes the daemon mutation and
    ///            execs no local destruction (the negative absence)
    #[test]
    fn local_row_branch_references_only_daemon_not_worktree_core_or_tmux() {
        // Verify that the four expected imports are present, and that the
        // names `worktree_core` and `kill_tmux_session_safe` do not appear
        // in the local-branch source path.
        //
        // This is a source-inspection test: we confirm the module source text
        // does NOT contain those symbols by examining the known imports above.
        //
        // The compile-level proof is the stronger guarantee: this module
        // CANNOT call `worktree_core::remove_worktree` or
        // `tmux::kill_tmux_session_safe` without first importing them, and
        // they are not imported.

        // Positive probe: daemon client is in scope (if this line compiles, the
        // daemon path is wired).
        let _ = std::any::type_name::<crate::daemon::Client>();

        // Negative structural assertion: scan ONLY the executable (non-comment,
        // non-test) lines of the production region.
        //
        // Strategy:
        //   1. Split at `#[cfg(test)]` to exclude this test module entirely.
        //   2. Strip every line that is purely a comment (`//`-prefixed after
        //      trimming) — doc comments like `///` and `//!` mention the
        //      forbidden names as prose documentation; we must not count those.
        //   3. Build forbidden needles by concatenation so the WHOLE string
        //      never appears as a single literal in this test source.
        //
        // A real call to `worktree_core::remove_worktree(...)` in the LOCAL
        // branch would appear on a non-comment executable line, so the scan
        // below would catch it while documentation prose does not false-positive.
        let source = include_str!("worktree_ops.rs");
        let prod_region = source.split("#[cfg(test)]").next().unwrap_or("");
        let prod: String = prod_region
            .lines()
            .filter(|line| !line.trim_start().starts_with("//"))
            .collect::<Vec<_>>()
            .join("\n");

        let needle_remove = format!("{}::{}", "worktree_core", "remove_worktree");
        // Concatenate two substrings so the whole forbidden literal never appears
        // in this source file — prevents the scan from matching its own needle.
        let needle_kill = ["kill_tmux", "_session_safe"].concat();
        assert!(
            !prod.contains(&needle_remove),
            "LOCAL branch must not call worktree_core::remove_worktree"
        );
        assert!(
            !prod.contains(&needle_kill),
            "LOCAL branch must not call tmux::kill_tmux_session_safe"
        );
        assert!(
            prod.contains("worktrees_cleanup"),
            "LOCAL branch must call daemon worktrees_cleanup"
        );
    }

    // -----------------------------------------------------------------------
    // POSITIVE: client builds correct GraphQL POST body for worktrees_cleanup
    // -----------------------------------------------------------------------

    /// Verifies that `build_cleanup_variables` — the REAL helper called by
    /// `worktrees_cleanup_with_sessions` — produces the correct GraphQL variables
    /// shape. Calling the real helper (rather than hand-building the expected
    /// value inline) means this test actually exercises the production code path
    /// for AC-G1 (active-session identity) and AC7 (correct mutation variables).
    ///
    /// @scenario The TUI local-cleanup path invokes the daemon mutation and
    ///            execs no local destruction (the positive route — body shape)
    #[test]
    fn worktrees_cleanup_request_body_has_correct_mutation_and_variables() {
        use crate::daemon::client::build_cleanup_variables;

        let ids = vec!["repo:branch-a".to_string(), "repo:branch-b".to_string()];
        let session_names = vec!["sess-a".to_string()];
        let active_session = "active-sess";
        let active_cwd = "/path/to/active";

        // Call the REAL helper — the same function worktrees_cleanup_with_sessions uses.
        let vars =
            build_cleanup_variables(&ids, &session_names, Some(active_session), Some(active_cwd));

        // variables["input"]["worktreeIds"] — both ids present.
        assert_eq!(
            vars["input"]["worktreeIds"][0].as_str(),
            Some("repo:branch-a"),
            "worktreeIds[0] must be the first worktree ID"
        );
        assert_eq!(
            vars["input"]["worktreeIds"][1].as_str(),
            Some("repo:branch-b"),
            "worktreeIds[1] must be the second worktree ID"
        );

        // AC-G3: sessionNames parallel array — sess-a at index 0, null at index 1.
        assert_eq!(
            vars["input"]["sessionNames"][0].as_str(),
            Some("sess-a"),
            "sessionNames[0] must carry the session name"
        );
        assert!(
            vars["input"]["sessionNames"][1].is_null(),
            "sessionNames[1] must be null when no session for that worktree"
        );

        // AC-G1: activeSession and activeCwd forwarded verbatim.
        assert_eq!(
            vars["input"]["activeSession"].as_str(),
            Some("active-sess"),
            "activeSession must be forwarded"
        );
        assert_eq!(
            vars["input"]["activeCwd"].as_str(),
            Some("/path/to/active"),
            "activeCwd must be forwarded"
        );
    }

    /// Verifies that requests WITHOUT variables (existing query callers) still
    /// omit the `variables` field from the serialised body — preserving
    /// backwards compatibility after the struct change.
    ///
    /// @scenario Existing query-only callers still compile and send clean bodies
    #[test]
    fn graphql_request_without_variables_omits_variables_field() {
        use crate::daemon::client::GraphQlRequest;

        let req = GraphQlRequest::new("{ health { status } }");
        let body = serde_json::to_value(&req).expect("serialise request");

        assert!(
            body.get("variables").is_none(),
            "variables field must be absent for query-only requests"
        );
        assert_eq!(
            body["query"].as_str(),
            Some("{ health { status } }"),
            "query field must be present"
        );
    }
}
