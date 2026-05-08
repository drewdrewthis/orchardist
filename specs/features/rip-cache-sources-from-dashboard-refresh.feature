Feature: Rip cache_sources from TUI dashboard refresh path (#426 phase 5, local-only Phase 1)
  As an orchardist running the interactive TUI dashboard
  I want local data (issues, PRs, local worktrees, local tmux sessions, claude instances) to flow from the orchard daemon's WorkView, not from per-source `git`/`gh`/`tmux` shell-outs
  So that the dashboard shares the daemon's join pipeline, refreshes are a single round-trip, and `cache_sources` survives only as a documented thin shim

  # Scope: LOCAL DATA ONLY. Remote worktrees + remote tmux sessions continue to flow
  # through `cache_sources::refresh_remote_worktrees` + `refresh_remote_tmux_sessions`
  # (gated on daemon Workstream F populating per-peer worktrees in WorkView). Wholesale
  # rip + federation parity are tracked as Phase 2 / Phase 3 follow-up issues.

  Background:
    Given the daemon is reachable at "http://127.0.0.1:7777/graphql"
    And `daemon::Client::work_view` returns a `WorkViewSnapshot { projects, tmux_sessions, claude_instances }`
    And `cache_sources` retains `refresh_remote_worktrees`, `refresh_remote_tmux_sessions`, `TMUX_SESSION_FORMAT`, and `parse_tmux_sessions_from_panes`
    And `sources/hosts.rs` retains the SSH-based reachability probe used by both refresh paths

  # =======================================================================
  # AC1 — TUI refresh sources LOCAL data from daemon WorkView, not cache_sources
  # =======================================================================

  @unit
  Scenario: start_full_refresh fetches local data via daemon::Client::work_view
    Given a `tui::App` constructed with a fake daemon client recording every call
    And the fake client returns a `WorkViewSnapshot` with one project, one local worktree on branch "issue429/spec", one local tmux session, and one claude instance
    When `App::start_full_refresh` runs to completion
    Then the fake daemon client recorded exactly one `work_view` call
    And `cache_sources::refresh_issues` was NOT invoked
    And `cache_sources::refresh_prs` was NOT invoked
    And `cache_sources::refresh_worktrees` was NOT invoked
    And `cache_sources::refresh_tmux_sessions` was NOT invoked

  @unit
  Scenario: start_local_refresh fetches local data via daemon::Client::work_view
    Given a `tui::App` constructed with a fake daemon client
    When `App::start_local_refresh` runs to completion
    Then the fake daemon client recorded exactly one `work_view` call
    And `cache_sources::refresh_worktrees` was NOT invoked
    And `cache_sources::refresh_tmux_sessions` was NOT invoked

  @integration
  Scenario: WorkView snapshot adapts into LocalRepoSnapshot consumable by build_state_with_cached_snapshots
    Given a `WorkViewSnapshot` with two projects, four local worktrees, two local tmux sessions, and one claude instance
    When `daemon::work_view_adapter::build_local_state` is called
    Then a `LocalRepoSnapshot` is returned containing two repos
    And each worktree is enriched with PR/issue join from the `WorkView` payload
    And each tmux session is joined with its claude instance via pane reference
    And the result is accepted by `merge_remote::build_state_with_cached_snapshots` without further normalization

  # =======================================================================
  # AC2 — Remote worktrees + remote tmux sessions continue via cache_sources
  # =======================================================================

  @unit
  Scenario: start_full_refresh continues to call refresh_remote_worktrees + refresh_remote_tmux_sessions
    Given a `tui::App` configured with one remote of `RemoteKind::OrchardProxy`
    And a fake daemon client returning a `WorkViewSnapshot` with no remote worktrees (daemon hosts are local-only)
    When `App::start_full_refresh` runs
    Then `cache_sources::refresh_remote_worktrees` was invoked for the configured remote
    And `cache_sources::refresh_remote_tmux_sessions` was invoked for the configured remote

  @integration
  Scenario: Remote worktrees from cache_sources merge into OrchardState alongside daemon-fresh local data
    Given a `WorkViewSnapshot` with two local worktrees for repo "owner/repo"
    And the cache file `owner_repo_remote_worktrees.json` contains one remote worktree on host "boxd@vm.boxd.sh" for the same repo
    When the merge runs through `build_state_with_cached_snapshots`
    Then the resulting `OrchardState` contains three worktrees for "owner/repo"
    And exactly two carry `host == "local"`
    And exactly one carries `host == "boxd@vm.boxd.sh"`

  # =======================================================================
  # AC3 — cache_sources survives as a thin documented shim
  # =======================================================================

  @unit
  Scenario: cache_sources retains the documented surviving surface
    Then the public surface of `cache_sources` includes:
      | item                              | reason                                                            |
      | refresh_remote_worktrees          | consumed by `tui::App` until federation Phase 3                   |
      | refresh_remote_tmux_sessions      | consumed by `tui::App` until federation Phase 3                   |
      | refresh_issues                    | consumed by `watch::daemon` (separate refactor)                    |
      | refresh_prs                       | consumed by `watch::daemon` (separate refactor)                    |
      | refresh_worktrees                 | consumed by `watch::daemon` and `heal` (separate refactor)         |
      | refresh_tmux_sessions             | consumed by `watch::daemon` and `heal` (separate refactor)         |
      | TMUX_SESSION_FORMAT               | constant referenced by `remote_adapter`                           |
      | parse_tmux_sessions_from_panes    | pure-data parser referenced by `remote_adapter`                   |
    And the module-level rustdoc explicitly labels `cache_sources` as a thin shim with named owners

  # =======================================================================
  # AC4 — sources/{github,worktrees,tmux,claude}.rs deleted
  # =======================================================================

  @unit
  Scenario: sources directory contains only hosts.rs and mod.rs
    Then `crates/orchard/src/sources/github.rs` does NOT exist
    And `crates/orchard/src/sources/worktrees.rs` does NOT exist
    And `crates/orchard/src/sources/tmux.rs` does NOT exist
    And `crates/orchard/src/sources/claude.rs` does NOT exist
    And `crates/orchard/src/sources/hosts.rs` exists
    And `crates/orchard/src/sources/mod.rs` re-exports only `hosts`-relevant symbols

  @unit
  Scenario: sources/mod.rs no longer re-exports the deleted pass-throughs
    Then `crates/orchard/src/sources/mod.rs` does NOT name `github`, `worktrees`, `tmux`, or `claude` as a child module
    And no caller in `crates/orchard/src/` references `sources::github`, `sources::worktrees`, `sources::tmux`, or `sources::claude`

  # =======================================================================
  # AC5 — sources/hosts.rs retained
  # =======================================================================

  @unit
  Scenario: sources/hosts.rs is retained for SSH reachability probing
    Then `crates/orchard/src/sources/hosts.rs` exists
    And its public `probe_reachability_*` functions remain unchanged in shape

  @integration
  Scenario: start_full_refresh still calls sources::hosts::probe_reachability for per-host badges
    Given a `tui::App` configured with two remotes
    When `App::start_full_refresh` runs
    Then `sources::hosts::probe_reachability_all_for_remotes` is invoked exactly once
    And the resulting host-reachability map populates the dashboard's per-host badges

  # =======================================================================
  # AC6 — grep gate: no Command::new of git/gh/tmux/ssh from TUI or local-data fetch path
  # =======================================================================

  @e2e
  Scenario: grep verifies the TUI module does NOT shell out to git, gh, tmux, or ssh
    When the verification command is run:
      """
      grep -rnE 'std::process::Command::new\("(git|gh|ssh)"\)|std::process::Command::new\("tmux"\).*list-(sessions|panes)' crates/orchard/src/tui/
      """
    Then the command exits with code 1 (no matches)
    And the only allowed cross-reference from `tui/` to `cache_sources` is to `refresh_remote_worktrees`, `refresh_remote_tmux_sessions`, or `TMUX_SESSION_FORMAT` (constant use)

  @e2e
  Scenario: grep verifies the local-data fetch path does NOT shell out
    When the verification command is run:
      """
      grep -rnE 'std::process::Command::new\("(git|gh)"\)|std::process::Command::new\("tmux"\).*list-(sessions|panes)' crates/orchard/src/daemon/ crates/orchard/src/build_state.rs crates/orchard/src/derive.rs
      """
    Then the command exits with code 1 (no matches)

  @unit
  Scenario: TMUX_SESSION_FORMAT constant references in remote_adapter remain allowed
    Then `crates/orchard/src/remote_adapter.rs` is permitted to reference `cache_sources::TMUX_SESSION_FORMAT` as a string constant
    And `crates/orchard/src/remote_adapter.rs` is permitted to call `cache_sources::parse_tmux_sessions_from_panes` (pure-data parser)
    And neither reference invokes `Command::new` of `git`, `gh`, or `tmux list-*`

  @unit
  Scenario: sources::hosts::probe_* SSH probes are explicitly allowed outside the data-fetch path
    Then `sources::hosts::probe_reachability_*` may invoke `Command::new("ssh")` for reachability probes
    And these probes do NOT count against the AC6 grep gate
    And the AC6 grep regex excludes `sources/hosts.rs` from the failure set

  # =======================================================================
  # AC7 — Daemon-down UX: graceful fallback, NOT a 5s blank screen
  # =======================================================================

  @integration
  Scenario: TUI startup with daemon unreachable falls back to last-known cached state
    Given the daemon is unreachable on the configured port
    And `~/.cache/orchard/` contains valid cache files from a prior session (worktrees, tmux sessions, remote snapshots)
    When `orchard-tui` is launched
    Then the dashboard renders within 100ms with the cached worktree rows
    And a status-line indicator reads "daemon unreachable" or "daemon starting"
    And the screen is NOT blank for the daemon client's default 5s timeout

  @integration
  Scenario: TUI mid-session daemon outage retains last-known state
    Given the TUI is rendering live data sourced from a healthy daemon
    When the daemon process is terminated
    And the next `start_full_refresh` tick fires
    Then the previously rendered worktree rows remain on screen
    And a status-line indicator transitions to "daemon unreachable"
    And no panic, no empty redraw, and no synchronous 5s stall on the UI thread occur

  @integration
  Scenario: TUI recovers automatically when the daemon comes back
    Given the TUI is rendering with the "daemon unreachable" indicator
    When the daemon becomes reachable again
    And the next `start_full_refresh` tick fires
    Then the dashboard refreshes with the daemon-fresh `WorkViewSnapshot`
    And the "daemon unreachable" indicator is cleared

  # =======================================================================
  # AC8 — Watch daemon stops writing orphaned *_issues.json + *_prs.json
  # =======================================================================

  @integration
  Scenario: watch daemon does NOT write orphaned issues + prs caches by default
    Given `orchard-tui watch` runs through one full `refresh_all_sources` cycle
    Then `~/.cache/orchard/` contains no new `*_issues.json` files written this cycle
    And `~/.cache/orchard/` contains no new `*_prs.json` files written this cycle
    And `*_worktrees.json` and `tmux_sessions.json` are still produced (consumed by remote-only paths)

  @integration
  Scenario: --keep-diagnostic-caches flag re-enables the orphan writes for debugging
    Given `orchard-tui watch --keep-diagnostic-caches` runs through one full cycle
    Then `~/.cache/orchard/` contains the expected `{owner}_{repo}_issues.json` for each configured project
    And `~/.cache/orchard/` contains the expected `{owner}_{repo}_prs.json` for each configured project
    And the documented purpose of these files is "diagnostic only, not consumed by TUI"

  # =======================================================================
  # AC9 — json_freshness_integration tests pass
  # =======================================================================

  @e2e
  Scenario: json_freshness_integration tests pass post-rip
    When `cargo test --test json_freshness_integration` runs
    Then it exits 0
    And any pre-existing flake noted in #420 is acknowledged in the run log but does NOT block the gate

  # =======================================================================
  # AC10 — Existing TUI dashboard UX preserved
  # =======================================================================

  @e2e
  Scenario: Worktree list renders identical rows post-rip for local repos
    Given a daemon serving a `WorkViewSnapshot` with two repos and seven local worktrees
    And no remote configuration
    When `orchard-tui` launches
    Then the rendered worktree list contains seven rows
    And each row displays the worktree's branch name
    And each row's repo grouping matches the daemon's `Project.directory` mapping

  @e2e
  Scenario: PR badges for OPEN PRs render identically post-rip
    Given a daemon `WorkViewSnapshot` with one worktree whose branch matches an OPEN PR
    And the OPEN PR carries `statusCheckRollup`, `reviewDecision`, and `mergeStateStatus` fields
    When the dashboard renders
    Then the worktree row displays the PR number, status check badge, review badge, and merge state badge
    And these badges match what the legacy `cache_sources` path produced for the same OPEN PR

  @e2e
  Scenario: Claude state renders identically post-rip
    Given a daemon `WorkViewSnapshot` with one tmux session containing a claude pane in state "working"
    When the dashboard renders
    Then the corresponding worktree row displays the claude state badge "working"
    And the join is performed client-side via the `ClaudeInstance.pane → TmuxPane → TmuxSession` path

  @e2e
  Scenario: Attach behaviour from the dashboard preserved
    Given a worktree row corresponding to a local tmux session named "issue429"
    When the user invokes the attach action
    Then the existing `tmux attach -t issue429` flow runs
    And no `daemon::Client` mutation is required for attach

  @e2e
  Scenario: Remote worktrees still render in the dashboard during Phase 1
    Given a remote of kind `OrchardProxy` whose `*_remote_worktrees.json` cache contains two worktrees
    When the TUI launches
    Then the dashboard shows two remote worktree rows tagged with the remote's host
    And those rows came from `cache_sources::refresh_remote_worktrees` (not from the daemon WorkView)

  @e2e
  Scenario: Merged-PR cleanup affordance still appears for stale worktrees
    Given a worktree whose corresponding PR was merged in GitHub
    And the merged-PR enrichment flows through the cache-driven path retained for Phase 1
    When the dashboard renders
    Then the worktree appears in `DisplayGroup::ReadyToMerge` with the stale-fade affordance
    And the cleanup action remains available from the dashboard

  # =======================================================================
  # AC11 — Sessions↔claude join stays client-side via pane reference
  # =======================================================================

  @unit
  Scenario: Sessions↔claude join is performed client-side from WorkViewSnapshot
    Given a `WorkViewSnapshot` with two tmux sessions and three claude instances
    And each claude instance carries a `pane` reference identifying its host tmux pane
    When `daemon::work_view_adapter::build_local_state` joins the snapshot
    Then each claude instance is attached to its tmux session by traversing `ClaudeInstance.pane → TmuxPane → TmuxSession`
    And no daemon-side join field is consulted for the sessions↔claude relationship

  # =======================================================================
  # Test fixture support — non-AC, but referenced by the plan
  # =======================================================================

  @unit
  Scenario: derive.rs tests construct OrchardState from WorkView-shaped fixtures
    Given a `WorkViewFixture` builder that produces a `WorkViewSnapshot`
    When the fixture is run through `daemon::work_view_adapter::build_local_state` then `derive::derive_all_repos`
    Then the resulting `OrchardState` matches the previously hand-constructed assertion shape
    And no test in `derive.rs` constructs `OrchardState` directly when daemon-shaped inputs are available

  # --- AC Coverage Map ---
  # AC 1: "tui::App::start_full_refresh and start_local_refresh source local data from daemon::Client::work_view"
  #   → @unit "start_full_refresh fetches local data via daemon::Client::work_view"
  #   → @unit "start_local_refresh fetches local data via daemon::Client::work_view"
  #   → @integration "WorkView snapshot adapts into LocalRepoSnapshot consumable by build_state_with_cached_snapshots"
  #
  # AC 2: "Remote worktrees + remote tmux sessions continue to be driven by cache_sources::refresh_remote_*"
  #   → @unit "start_full_refresh continues to call refresh_remote_worktrees + refresh_remote_tmux_sessions"
  #   → @integration "Remote worktrees from cache_sources merge into OrchardState alongside daemon-fresh local data"
  #
  # AC 3: "cache_sources.rs survives as a thin shim for refresh_remote_*, watch::daemon, heal, TMUX_SESSION_FORMAT/parser"
  #   → @unit "cache_sources retains the documented surviving surface"
  #
  # AC 4: "sources/{github,worktrees,tmux,claude}.rs deleted"
  #   → @unit "sources directory contains only hosts.rs and mod.rs"
  #   → @unit "sources/mod.rs no longer re-exports the deleted pass-throughs"
  #
  # AC 5: "sources/hosts.rs retained — daemon does not yet probe per-config remotes"
  #   → @unit "sources/hosts.rs is retained for SSH reachability probing"
  #   → @integration "start_full_refresh still calls sources::hosts::probe_reachability for per-host badges"
  #
  # AC 6: "grep -rn 'std::process::Command' crates/orchard/src/ shows zero Command::new of git/gh/tmux list-*/ssh from tui/ and local-data fetch path"
  #   → @e2e "grep verifies the TUI module does NOT shell out to git, gh, tmux, or ssh"
  #   → @e2e "grep verifies the local-data fetch path does NOT shell out"
  #   → @unit "TMUX_SESSION_FORMAT constant references in remote_adapter remain allowed"
  #   → @unit "sources::hosts::probe_* SSH probes are explicitly allowed outside the data-fetch path"
  #
  # AC 7: "Daemon-down UX: TUI degrades gracefully — falls back to last-known cache OR shows explicit 'daemon starting' indicator, not an empty 5s blank screen"
  #   → @integration "TUI startup with daemon unreachable falls back to last-known cached state"
  #   → @integration "TUI mid-session daemon outage retains last-known state"
  #   → @integration "TUI recovers automatically when the daemon comes back"
  #
  # AC 8: "Watch daemon updated to NOT write *_issues.json + *_prs.json caches that nobody reads anymore (or behind --keep-diagnostic-caches)"
  #   → @integration "watch daemon does NOT write orphaned issues + prs caches by default"
  #   → @integration "--keep-diagnostic-caches flag re-enables the orphan writes for debugging"
  #
  # AC 9: "json_freshness_integration tests pass (some flakes pre-existing per #420)"
  #   → @e2e "json_freshness_integration tests pass post-rip"
  #
  # AC 10: "Existing TUI dashboard UX (worktree list, PR badges for OPEN PRs, claude state, attach behaviour, remote worktrees, merged-PR cleanup affordance) is preserved"
  #   → @e2e "Worktree list renders identical rows post-rip for local repos"
  #   → @e2e "PR badges for OPEN PRs render identically post-rip"
  #   → @e2e "Claude state renders identically post-rip"
  #   → @e2e "Attach behaviour from the dashboard preserved"
  #   → @e2e "Remote worktrees still render in the dashboard during Phase 1"
  #   → @e2e "Merged-PR cleanup affordance still appears for stale worktrees"
  #
  # Total ACs in issue body: 10 (each as a `- [ ]` checkbox in the body). All 10 mapped above.
  #
  # Bonus guard scenario (NOT an AC, retained as a guard against regression of the architectural
  # invariant called out in the issue's plan section):
  #   → @unit "Sessions↔claude join is performed client-side from WorkViewSnapshot"
  #   → @unit "derive.rs tests construct OrchardState from WorkView-shaped fixtures"
  # These guard the "dual-join is non-negotiable" invariant — PR/issue join lives daemon-side,
  # sessions↔claude join lives client-side. They are extra coverage, not contract.
