Feature: Validate launch-remote + TUI Enter + federation end-to-end on v0.9.0
  As an orchardist using both local and remote (boxd-fork, boxd-shared, orchard-proxy) workflows
  I want /launch-remote, TUI Enter on remote rows, and federated discovery to all work
    coherently against v0.9.0
  So that I can trust that launching a remote session is immediately visible,
    attaching to a remote session lands me in a shell on the VM,
    and a host configured `"type": "orchard-proxy"` reflects the remote's own snapshot
    (PRs, CI, tmux) rather than re-derived shell-discovery.

  # Scope: this issue is a VALIDATION + REGRESSION-LOCK pass on v0.9.0 (per Plan).
  # Implementation gaps (refresh-on-launch, proxy-failure UI surface,
  # `orchard launch-precheck`, worktree-gone UX) are filed as follow-up issues.
  # Tests in this feature file MUST NOT depend on those follow-ups landing.

  Background:
    Given local orchard is built at v0.9.0 or later
    And `~/.config/orchard/config.json` contains the user's remotes
    And the per-host cache directory is "~/.cache/orchard/"
    And `events.jsonl` is at "~/.local/state/git-orchard/events.jsonl"
    And the `JsonOutput.version` supported list is `[6]` for this branch

  # =======================================================================
  # AC1 — Fresh launch (boxd-fork): /launch-remote on a langwatch issue
  # creates the fork, launches Claude, and within 30s the session appears
  # under the remote's `sessions` in `orchard --json` locally.
  # =======================================================================

  @e2e
  Scenario: /launch-remote against a langwatch issue surfaces the new boxd-fork session locally
    Given the user has a configured boxd-fork remote for "langwatch/langwatch"
    And no existing fork or tmux session exists for issue #<N>
    When the user runs `/launch-remote langwatch/langwatch#<N>`
    And the user runs `orchard refresh` after the launch completes
    And the user runs `orchard --json` locally
    Then within 30 seconds (after the explicit refresh) the output contains a worktree
        for the new fork host
    And that worktree's `sessions` array contains a session whose name references issue <N>
    And the session's host equals the boxd-fork host
    And evidence (the matching `--json` excerpt + elapsed seconds) is captured in the PR

  @integration
  Scenario: boxd-fork adapter routing emits a session row matching the launched session
    Given a fake SSH exec runner configured for a boxd-fork host
    And the runner is primed to return a tmux session named "or_issue<N>" at the fork's worktree path
    When `BoxdForkAdapter::list_sessions()` runs against that host
    Then exactly one CachedTmuxSession is returned with name "or_issue<N>"
    And its host equals the boxd-fork host
    And its working directory equals the worktree path

  # =======================================================================
  # AC2 — Fresh launch (boxd-shared): /launch-remote on a git-orchard-rs
  # issue uses the shared VM adapter, creates the session, and it appears
  # locally.
  # =======================================================================

  @e2e
  Scenario: /launch-remote against a git-orchard-rs issue surfaces the new boxd-shared session locally
    Given the user has a configured boxd-shared remote for "drewdrewthis/git-orchard-rs"
        (host "boxd@orchard-rs.boxd.sh")
    And no existing tmux session exists for issue #<N> on that VM
    When the user runs `/launch-remote drewdrewthis/git-orchard-rs#<N>`
    And the user runs `orchard refresh` after the launch completes
    And the user runs `orchard --json` locally
    Then within 30 seconds (after the explicit refresh) the output contains a worktree
        on host "boxd@orchard-rs.boxd.sh" for the issue branch
    And that worktree's `sessions` array contains a session whose name references issue <N>
    And evidence (the matching `--json` excerpt + elapsed seconds) is captured in the PR

  @integration
  Scenario: boxd-shared adapter discovers the new session on the shared VM
    Given a fake SSH exec runner configured for "boxd@orchard-rs.boxd.sh"
    And the runner is primed to return a tmux session "or_issue<N>" at the shared-VM worktree path
    When `BoxdSharedAdapter::list_sessions()` runs against that host
    Then exactly one CachedTmuxSession is returned with name "or_issue<N>"
    And its host equals "boxd@orchard-rs.boxd.sh"

  # =======================================================================
  # AC3 — TUI Enter on an existing remote session creates the local proxy,
  # SSH-attaches, and the user lands in a shell on the VM.
  # Already validated 2026-04-22 06:14 (log: createRemoteProxySession:
  # claude -> remote_claude; hostname=issue3201). Lock with a regression test.
  # =======================================================================

  @unit
  Scenario: TaskEnterAction builder selects JoinSession for a remote worktree with an existing session
    Given a worktree row with `host == Some("boxd@issue3201.boxd.sh")`
    And `sessions` contains exactly one entry named "claude"
    When `handle_enter_action` builds a TaskEnterAction from that row
    Then the action equals `JoinSession { host: Some("boxd@issue3201.boxd.sh"), session: "claude", .. }`
    And the action does NOT equal `CreateSession`

  @integration
  Scenario: join_or_create_session on a JoinSession with a remote host calls create_remote_proxy_session
    Given a TaskEnterAction::JoinSession with `host == Some("boxd@issue3201.boxd.sh")` and session "claude"
    And a fake remote-session executor that records every call it receives
    When `join_or_create_session` runs that action
    Then the executor records exactly one `create_remote_proxy_session` call
        with host "boxd@issue3201.boxd.sh" and source session "claude"
    And the proxy session name on the recorded call equals "remote_claude"

  # =======================================================================
  # AC4 — TUI Enter on a remote worktree with no tmux session creates the
  # remote session then attaches; the probe-unknown fix (#285) does not
  # block Enter; boxd-fork adapter routing (#288) is still honored.
  # =======================================================================

  @unit
  Scenario: TaskEnterAction builder selects CreateSession for a remote worktree with no sessions
    Given a worktree row with `host == Some("boxd@vm.boxd.sh")`
    And `sessions` is empty
    And `host_reachable == Some(true)`
    When `handle_enter_action` builds a TaskEnterAction from that row
    Then the action equals `CreateSession { host: Some("boxd@vm.boxd.sh"), .. }`
    And the chosen host equals the worktree's host (boxd-fork routing preserved)

  @unit
  Scenario: Enter is NOT blocked when host_reachable is Unknown (regression for #285)
    Given a remote worktree row with `host_reachable == None` (probe never ran or returned Unknown)
    And `sessions` is empty
    When `handle_enter_action` builds a TaskEnterAction from that row
    Then the action is built (Enter is not short-circuited / blocked)
    And the action targets the worktree's remote host

  @integration
  Scenario: CreateSession on a reachable remote host calls create_remote_session then attaches
    Given a TaskEnterAction::CreateSession with `host == Some("boxd@vm.boxd.sh")`
    And a fake remote executor that succeeds
    When `join_or_create_session` runs that action
    Then the executor records a `create_remote_session` call for that host
    And then a `create_remote_proxy_session` (or attach) call is made for the new session

  @integration
  Scenario: CreateSession failure on a reachable remote surfaces a warning in the app
    Given a TaskEnterAction::CreateSession with `host == Some(...)` and `host_reachable == Some(true)`
    And a fake remote executor that returns an error from create_remote_session
    When `join_or_create_session` runs that action
    Then the app records a warning describing the remote-session failure
    And the warning is visible to the user (not silently swallowed)

  # =======================================================================
  # AC5 — Federation wiring: a remote flipped to `"type": "orchard-proxy"`
  # surfaces its worktrees/sessions/PRs from its own snapshot in local
  # `orchard --json`. No silent fallback: missing remote orchard yields a
  # stale warning, not phantom data.
  #
  # The "no silent fallback" sub-clause in this issue is split per the Plan:
  # the WIRE invariant (no legacy git/tmux call attempted on proxy failure +
  # `remote_adapter.proxy_failure` event written + last-known snapshot stays)
  # is validated here. The TUI surfacing of `(proxy stale)` is a follow-up.
  # =======================================================================

  @e2e
  Scenario: Single host flipped to orchard-proxy shows remote-sourced data in local --json
    Given the host "boxd@orchard-rs.boxd.sh" has `orchard --version` returning >= "0.9.0"
    And `~/.config/orchard/config.json` for that remote is updated to `"type": "orchard-proxy"`
    When the user runs `orchard refresh`
    Then a file matching `~/.cache/orchard/*orchard-rs*orchard_snapshot.json` is written
    And `orchard --json` includes worktrees attributed to that host
    And those worktrees carry `pr` / `issue` / `check_state` enrichment fields
        sourced from the remote snapshot

  @unit
  Scenario: Proxy failure does NOT trigger any legacy shell-discovery on that host
    Given an OrchardProxyAdapter wired to a fake SSH runner that records every command
    And the runner returns exit 127 ("orchard: command not found") for `orchard --json`
    When `OrchardProxyAdapter::list_worktrees()` runs
    Then the recorded commands include `orchard --json`
    And the recorded commands do NOT include `git worktree list --porcelain`
    And the recorded commands do NOT include `tmux list-sessions`
    And `events.jsonl` contains a `remote_adapter.proxy_failure` event with the host
        and a reason mentioning exit 127

  @unit
  Scenario: Last-known snapshot stays visible after a proxy failure (no phantom data, but no data loss)
    Given a prior `~/.cache/orchard/{safe_host}_orchard_snapshot.json` exists with 2 worktrees
        for host "boxd@orchard-rs.boxd.sh"
    And the next `OrchardProxyAdapter::fetch_snapshot` for that host returns exit 127
    When `build_state_with_cached_snapshots` constructs OrchardState
    Then the 2 worktrees from the cached snapshot remain in the merged state
    And the cached snapshot file is NOT deleted
    And a `remote_adapter.proxy_failure` event is present in `events.jsonl`

  @integration
  Scenario: Multi-snapshot merge dedupes by (host, path) across two orchard-proxy remotes
    Given two `OrchardProxy` remotes both configured against orchard-installed VMs
    And both snapshots include a worktree row with the same `(host, path)` tuple
    When `merge_remote_snapshot` runs
    Then exactly one WorktreeState is emitted for that tuple
    And the dedupe key `(host, path)` is honored

  @integration
  Scenario: Same-slug repo from local cache and remote snapshot is host-attributed correctly
    Given a local cache contains a worktree on slug "drewdrewthis/git-orchard-rs"
        with `host == None`
    And a remote snapshot from "boxd@orchard-rs.boxd.sh" contains a worktree on the same slug
        with `host == Some("boxd@orchard-rs.boxd.sh")`
    When the merged OrchardState is built
    Then the local row's host stays None
    And the remote row's host equals "boxd@orchard-rs.boxd.sh"
    And the two rows do not collapse into one (different `(host, path)` tuples)

  # =======================================================================
  # AC6 — /launch-remote uses federation when available: when targeting a
  # host with `orchard-proxy` enabled, decide whether a session already
  # exists from the remote orchard's view (avoid double-launch). When not
  # enabled, fall back to the adapter-based tmux probe (current behavior).
  #
  # NOTE: per the Plan's "Suggested follow-ups", AC6 is destined for a
  # follow-up `orchard launch-precheck <host> <session>` Rust subcommand.
  # For #337, validate the BEHAVIORAL CONTRACT at the data layer:
  # the federated snapshot is a sufficient input for a precheck, and
  # absent a federated snapshot the adapter-based tmux probe is what
  # /launch-remote sees.
  # =======================================================================

  @unit
  Scenario: With orchard-proxy enabled, the cached snapshot lists existing sessions for a host
    Given a `{safe_host}_orchard_snapshot.json` exists for host "boxd@orchard-rs.boxd.sh"
    And the snapshot contains a tmux session "or_issue<N>" attached to a worktree on that host
    When a precheck-style consumer reads `load_cached_snapshots()` for that host
    Then the loaded data exposes the session named "or_issue<N>"
    And a duplicate-launch check could be performed without any new SSH call

  @unit
  Scenario: Without orchard-proxy, /launch-remote falls back to the adapter-based tmux probe
    Given a remote configured as `"type": "remmy"` (or "boxd-fork" / "boxd-shared")
    And no `{safe_host}_orchard_snapshot.json` exists for that host
    When the system needs to determine whether a tmux session already exists for the target issue
    Then the per-kind adapter's `list_sessions` is the source of truth for that decision
    And no `ssh host orchard --json` call is made for hosts that are not orchard-proxy

  @integration
  Scenario: Mixed-config refresh — proxy host uses snapshot, non-proxy host uses legacy probe
    Given two remotes: host A is `"type": "orchard-proxy"`, host B is `"type": "remmy"`
    And both hosts are reachable
    When `orchard refresh` runs
    Then host A's session list is sourced from `ssh A orchard --json`
    And host B's session list is sourced from `ssh B tmux list-sessions ...` (legacy)
    And host A does NOT trigger any legacy `tmux list-sessions` call
    And host B does NOT trigger any `ssh B orchard --json` call

  # =======================================================================
  # AC Coverage Map
  # =======================================================================

  # --- AC Coverage Map ---
  # AC1 "Fresh launch — boxd-fork: session visible in local `orchard --json` after launch"
  #   -> "/launch-remote against a langwatch issue surfaces the new boxd-fork session locally" (@e2e)
  #   -> "boxd-fork adapter routing emits a session row matching the launched session" (@integration)
  # AC2 "Fresh launch — boxd-shared: session visible in local `orchard --json` after launch"
  #   -> "/launch-remote against a git-orchard-rs issue surfaces the new boxd-shared session locally" (@e2e)
  #   -> "boxd-shared adapter discovers the new session on the shared VM" (@integration)
  # AC3 "TUI Enter — existing remote session: creates local proxy, SSH-attaches, lands in shell on VM"
  #   -> "TaskEnterAction builder selects JoinSession for a remote worktree with an existing session" (@unit)
  #   -> "join_or_create_session on a JoinSession with a remote host calls create_remote_proxy_session" (@integration)
  # AC4 "TUI Enter — no-session remote worktree: creates remote session then attaches; #285 + #288 still honored"
  #   -> "TaskEnterAction builder selects CreateSession for a remote worktree with no sessions" (@unit)
  #   -> "Enter is NOT blocked when host_reachable is Unknown (regression for #285)" (@unit)
  #   -> "CreateSession on a reachable remote host calls create_remote_session then attaches" (@integration)
  #   -> "CreateSession failure on a reachable remote surfaces a warning in the app" (@integration)
  # AC5 "Federation wiring: orchard-proxy remote surfaces its own snapshot data; no silent fallback"
  #   -> "Single host flipped to orchard-proxy shows remote-sourced data in local --json" (@e2e)
  #   -> "Proxy failure does NOT trigger any legacy shell-discovery on that host" (@unit)
  #   -> "Last-known snapshot stays visible after a proxy failure (no phantom data, but no data loss)" (@unit)
  #   -> "Multi-snapshot merge dedupes by (host, path) across two orchard-proxy remotes" (@integration)
  #   -> "Same-slug repo from local cache and remote snapshot is host-attributed correctly" (@integration)
  #   -> NOTE: TUI surfacing of `(proxy stale)` is split out as a follow-up issue per Plan Phase 7
  # AC6 "/launch-remote uses federation when available; falls back to adapter-based tmux probe otherwise"
  #   -> "With orchard-proxy enabled, the cached snapshot lists existing sessions for a host" (@unit)
  #   -> "Without orchard-proxy, /launch-remote falls back to the adapter-based tmux probe" (@unit)
  #   -> "Mixed-config refresh — proxy host uses snapshot, non-proxy host uses legacy probe" (@integration)
  #   -> NOTE: `orchard launch-precheck <host> <session>` Rust subcommand is a follow-up per Plan Phase 7
