Feature: Extract post-probe remote-refresh pipeline shared by refresh_and_build + start_full_refresh (#295)
  As an orchard maintainer
  I want the post-probe per-(repo, remote) refresh loop to live in one helper
  So that the next change to "what we refresh per reachable host" lands in one place
     instead of two near-identical pipelines

  # Refactor — behavior is preserved at Site B, normalized up at Site A.
  # Two semantic deltas are intentional and bounded:
  #   * Site A's tmux dedup key changes from raw host to "{kind}:{host}" (matches Site B).
  #   * Site A's fork-host snapshot now happens BEFORE worktree refresh (honors the
  #     documented contract at cache_sources::snapshot_fork_hosts_for_remote).
  # See the issue body's Plan §"Key decisions" for the rationale.

  Background:
    Given `crates/orchard/src/sources/` exists in the workspace
    And `crates/orchard/src/build_state.rs::refresh_and_build` (Site A) drives the `--json` / refresh-and-build read path
    And `crates/orchard/src/tui/mod.rs::start_full_refresh` (Site B) drives the live TUI background refresh
    And both sites currently iterate `config.repos × repo.remotes` after probing reachability

  # =======================================================================
  # AC1 — One helper unifies the post-probe remote-refresh loop
  # =======================================================================

  @unit
  Scenario: A single helper drives the post-probe per-(repo, remote) refresh loop
    Then `crates/orchard/src/sources/` exposes a public function `refresh_remotes_for_reachable_hosts`
    And the function's body is the only place in the workspace that fans out `cache_sources::refresh_remote_worktrees`
       paired with `cache_sources::refresh_remote_tmux_sessions` across `(repo, remote)` pairs filtered by reachability
    And neither `build_state::refresh_and_build` nor `tui::start_full_refresh` retains an inline copy of that fan-out loop

  @integration
  Scenario: Site A delegates the post-probe loop to the helper
    Given a `GlobalConfig` with one repo and one `OrchardProxy` remote whose host probes as reachable
    When `build_state::refresh_and_build` runs
    Then it calls `refresh_remotes_for_reachable_hosts(&config, &reachable)` exactly once
    And the inline `tmux_dispatch` `Vec` and the surrounding `std::thread::scope` block that previously lived in
       `refresh_and_build` are gone

  @integration
  Scenario: Site B delegates the post-probe loop to the helper
    Given a `GlobalConfig` with one repo and one `OrchardProxy` remote whose host probes as reachable
    When `tui::App::start_full_refresh` runs to completion
    Then it calls `refresh_remotes_for_reachable_hosts(&config, &reachable)` exactly once
    And the inline `refresh_parallel::for_each_repo_parallel` block that previously drove remote refresh in
       `start_full_refresh` is gone

  # =======================================================================
  # AC2 — Helper lives in crates/orchard/src/sources/ without breaking hosts.rs cohesion
  # =======================================================================

  @unit
  Scenario: Helper lives in a new sources/ module, not in sources/hosts.rs
    Then `crates/orchard/src/sources/refresh.rs` exists
    And `pub fn refresh_remotes_for_reachable_hosts` is defined inside `sources/refresh.rs`
    And `crates/orchard/src/sources/hosts.rs` does NOT define `refresh_remotes_for_reachable_hosts`
    And `crates/orchard/src/sources/mod.rs` declares `pub mod refresh;`
    And `sources/hosts.rs`'s module-level rustdoc continues to describe it as "probe reachability" only

  # =======================================================================
  # AC3 — Tmux dedup key matches Site B today ("{kind}:{host}")
  # =======================================================================

  @unit
  Scenario: Helper dedupes tmux refresh by "{kind}:{host}", not raw host
    Given a config with two remotes that share the host "vm.boxd.sh" but differ in kind
       (one `Remmy`, one `BoxdFork`)
    And both hosts are reachable
    When the helper runs
    Then `cache_sources::refresh_remote_tmux_sessions` is invoked twice — once per `(kind, host)` pair
    And the dedup key used by the helper is `format!("{kind}:{host}", ...)` (matching `tui::mod::dedup_key`)

  @unit
  Scenario: Helper dedupes tmux refresh once when two remotes share both kind and host
    Given a config with two repos, each pointing to the same remote (`BoxdFork @ vm.boxd.sh`)
    And the host is reachable
    When the helper runs
    Then `cache_sources::refresh_remote_tmux_sessions` is invoked exactly once for that `(kind, host)` pair
    And `cache_sources::refresh_remote_worktrees` is invoked twice (once per repo) — worktree refresh is not deduped by host

  # =======================================================================
  # AC4 — Fork-host snapshot is taken BEFORE refresh_remote_worktrees
  # =======================================================================

  @unit
  Scenario: Helper takes the fork-host snapshot before dispatching worktree refresh
    Given a `BoxdFork` remote whose pre-refresh cache contains fork host "vm-old.boxd.sh"
    And the remote's authoritative `list --json` will return only "vm-new.boxd.sh"
    When the helper processes that `(repo, remote)` pair
    Then `cache_sources::snapshot_fork_hosts_for_remote` is called BEFORE
       `cache_sources::refresh_remote_worktrees` for the same `(repo, remote)`
    And the snapshot returned to the tmux refresh contains "vm-old.boxd.sh" (the pre-mutation cache state)
    And `cache_sources::refresh_remote_tmux_sessions` receives that snapshot unchanged

  @integration
  Scenario: Site A's pre-existing race no longer reproduces
    Given a `BoxdFork` remote configured at Site A whose pre-refresh cache lists fork host "vm-stale.boxd.sh"
    And the remote's authoritative `list --json` will no longer return "vm-stale.boxd.sh"
    When `build_state::refresh_and_build` runs to completion
    Then the cache entry for "vm-stale.boxd.sh" is treated as a vanished fork (eligible for deletion)
    And the cache entry was NOT deleted before `refresh_remote_worktrees` mutated the cache
    And `cache_sources::delete_vanished_fork_caches` was driven from a snapshot taken pre-mutation

  # =======================================================================
  # AC5 — Site B still emits AppMsg::HostReachability per host; helper is sink-free
  # =======================================================================

  @unit
  Scenario: Helper signature does not depend on AppMsg or any message sink
    Then `pub fn refresh_remotes_for_reachable_hosts` takes exactly two parameters:
       `config: &GlobalConfig` and `reachable: &HashSet<String>`
    And the function's signature mentions neither `AppMsg`, `mpsc::Sender`, nor any sink-like callback

  @integration
  Scenario: Site B emits AppMsg::HostReachability per probed host BEFORE calling the helper
    Given a `tui::App` configured with two remotes whose hosts probe with mixed reachability (one true, one false)
    And a fake `tx: Sender<AppMsg>` recording every send
    When `tui::App::start_full_refresh` runs
    Then `tx` recorded one `AppMsg::HostReachability(host, reachable)` per probed host
    And every recorded `HostReachability` send happened strictly before the `refresh_remotes_for_reachable_hosts` call

  # =======================================================================
  # AC6 — Site A still produces HashMap<String, HostState> for build_state_with_hosts
  # =======================================================================

  @integration
  Scenario: Site A builds HostState map and feeds it to build_state_with_hosts after the helper returns
    Given a `GlobalConfig` with one repo and three remotes whose hosts probe (true, false, true)
    When `build_state::refresh_and_build` runs to completion
    Then `refresh_remotes_for_reachable_hosts` was called with a `HashSet<String>` containing the two reachable hosts
    And `build_state_with_hosts(&config, &hosts)` is invoked with a `HashMap<String, HostState>` of length 3
    And the `HostState.reachable` flag for each host matches the probe result
    And the `--json` output shape produced from `OrchardState` is byte-identical to the pre-refactor `--json` output
       for the same fixture (no schema drift)

  # =======================================================================
  # AC7 — Parallelism shape consolidates to one pattern
  # =======================================================================

  @unit
  Scenario: Helper uses std::thread::scope for fan-out, not refresh_parallel::for_each_repo_parallel
    Then the body of `refresh_remotes_for_reachable_hosts` uses `std::thread::scope`
    And the body does NOT call `crate::refresh_parallel::for_each_repo_parallel`
    And worktree refreshes fan out per `(repo, remote)` pair (one spawn per reachable pair)
    And tmux refreshes fan out per first-seen `"{kind}:{host}"` (one spawn per deduped pair)

  @unit
  Scenario: refresh_parallel::for_each_repo_parallel remains available to other callers
    Then `crate::refresh_parallel::for_each_repo_parallel` is still defined and exported
    And its callers outside `tui::start_full_refresh` (notably the local-refresh path) continue to use it unchanged

  # =======================================================================
  # AC8 — Tests
  # =======================================================================

  @unit
  Scenario: Regression test asserts fork-host snapshot precedes refresh_remote_worktrees
    Given a unit test in `sources/refresh.rs` (or a colocated `tests` module) using a fake `cache_sources` recorder
    And a `BoxdFork` remote configured for one repo, host reachable
    When `refresh_remotes_for_reachable_hosts(&config, &reachable)` runs
    Then the recorder shows `snapshot_fork_hosts_for_remote(repo, remote)` was invoked
       strictly before `refresh_remote_worktrees(repo, remote)` for the same `(repo, remote)`
    And the recorder shows the snapshot's return value was passed unchanged to `refresh_remote_tmux_sessions`

  @unit
  Scenario: Regression test asserts dedup-by-"{kind}:{host}"
    Given a unit test in `sources/refresh.rs` (or a colocated `tests` module) using a fake `cache_sources` recorder
    And a config where two reachable remotes share the host "vm.boxd.sh" but differ in kind (`Remmy`, `BoxdFork`)
    And a separate config where two reachable remotes share both kind and host (`BoxdFork @ vm.boxd.sh` x2)
    When `refresh_remotes_for_reachable_hosts(&config, &reachable)` runs against each config
    Then the first config records two `refresh_remote_tmux_sessions` invocations (one per kind, same host)
    And the second config records exactly one `refresh_remote_tmux_sessions` invocation (kind+host both equal)
    And in both configs `refresh_remote_worktrees` is invoked once per `(repo, remote)` pair regardless of host

  @e2e
  Scenario: cargo test passes after the refactor lands
    When the contributor runs `cargo test --workspace`
    Then the suite exits with code 0
    And no pre-existing test regresses
    And the two new unit tests in `sources/refresh.rs` (or colocated tests module) are present and pass

  # =======================================================================
  # AC9 — Out of scope guard
  # =======================================================================

  @unit
  Scenario: Helper does NOT parallelize refresh_remote itself
    Then the body of `refresh_remotes_for_reachable_hosts` does NOT contain inner threading inside
       `cache_sources::refresh_remote_worktrees` or `cache_sources::refresh_remote_tmux_sessions`
    And inner parallelization of `refresh_remote` (item 6 of #272) remains tracked as a separate issue
    And the helper's parallelism is the outer fan-out over `(repo, remote)` pairs only

  # --- AC Coverage Map ---
  # AC 1: "A single helper unifies the post-probe remote-refresh loop currently duplicated across
  #        build_state::refresh_and_build and tui::start_full_refresh. After the change, 'what we
  #        refresh per reachable host' lives in one place."
  #   -> @unit "A single helper drives the post-probe per-(repo, remote) refresh loop"
  #   -> @integration "Site A delegates the post-probe loop to the helper"
  #   -> @integration "Site B delegates the post-probe loop to the helper"
  #
  # AC 2: "The helper lives in crates/orchard/src/sources/ — either as a new module
  #        (sources/refresh.rs or similar) or inside sources/hosts.rs. The location must keep
  #        sources/hosts.rs cohesive (probing) — see Plan §3."
  #   -> @unit "Helper lives in a new sources/ module, not in sources/hosts.rs"
  #
  # AC 3: "The helper's tmux-dedup behavior matches Site B today: dedup key is '{kind_str}:{host}',
  #        not raw host. (Site A's raw-host dedup is normalized up to B's behavior.)"
  #   -> @unit "Helper dedupes tmux refresh by '{kind}:{host}', not raw host"
  #   -> @unit "Helper dedupes tmux refresh once when two remotes share both kind and host"
  #
  # AC 4: "The helper takes the fork-host snapshot before dispatching refresh_remote_worktrees,
  #        honoring the contract at snapshot_fork_hosts_for_remote (cache_sources.rs:1942-1943).
  #        Site A's latent race disappears as a consequence."
  #   -> @unit "Helper takes the fork-host snapshot before dispatching worktree refresh"
  #   -> @integration "Site A's pre-existing race no longer reproduces"
  #
  # AC 5: "Site B continues to emit AppMsg::HostReachability per host. Message sending stays in
  #        the TUI shell — the helper does not depend on AppMsg or mpsc::Sender."
  #   -> @unit "Helper signature does not depend on AppMsg or any message sink"
  #   -> @integration "Site B emits AppMsg::HostReachability per probed host BEFORE calling the helper"
  #
  # AC 6: "Site A continues to produce HashMap<String, HostState> and feed it to
  #        build_state_with_hosts. The shape of the post-helper local-cache build is unchanged
  #        for --json."
  #   -> @integration "Site A builds HostState map and feeds it to build_state_with_hosts after the helper returns"
  #
  # AC 7: "Parallelism shape is consolidated: the helper uses one of the two existing patterns
  #        (thread::scope flat per-(repo, remote) or refresh_parallel::for_each_repo_parallel) —
  #        not both. Choice is documented in Plan." (Plan §"Key decisions" picks thread::scope.)
  #   -> @unit "Helper uses std::thread::scope for fan-out, not refresh_parallel::for_each_repo_parallel"
  #   -> @unit "refresh_parallel::for_each_repo_parallel remains available to other callers"
  #
  # AC 8: "All existing tests pass. Two new tests cover the previously-divergent behaviors:
  #        - A unit test asserts the helper takes the fork-host snapshot before mutating the
  #          remote_worktrees cache (covers AC #4 / Site A's old race).
  #        - A unit test asserts dedup-by-{kind}:{host} (two remotes sharing a host but different
  #          kinds both run; two remotes sharing both share one tmux refresh)."
  #   -> @unit "Regression test asserts fork-host snapshot precedes refresh_remote_worktrees"
  #   -> @unit "Regression test asserts dedup-by-'{kind}:{host}'"
  #   -> @e2e "cargo test passes after the refactor lands"
  #
  # AC 9: "Out of scope: parallelizing refresh_remote itself (item 6 of #272). The helper's
  #        parallelism is the outer fan-out only."
  #   -> @unit "Helper does NOT parallelize refresh_remote itself"
  #
  # Total ACs in issue body: 9. All 9 mapped above.
