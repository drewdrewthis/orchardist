Feature: TUI remote worktree federation (SSH path)
  As the orchard TUI
  I need to merge remote worktrees fetched over SSH into the local dashboard
  So that operators running across multiple machines see all their worktrees in one view.

  Background:
    Given the global config defines one or more remote hosts under repos[].remotes
    And the full-refresh cycle is active (fires every 60 seconds or on 'r')

  # Remote data does NOT come from the daemon's workView (Workstream F not yet landed).
  # Instead the TUI fans out over SSH via cache_sources::refresh_remote_worktrees
  # and cache_sources::refresh_remote_tmux_sessions, then merges the results via
  # merge_remote::merge_remote_snapshot.
  # This is a known L7 violation that is tracked for future daemon migration.

  @integration
  Scenario: SSH reachability probe precedes remote refresh
    Given the full-refresh thread begins
    When it probes SSH hosts
    Then each configured remote host is probed for SSH reachability
    And only reachable hosts proceed to cache_sources::refresh_remote_worktrees

  @integration
  Scenario: Remote worktrees are merged into OrchardState after local data
    Given the daemon workView delivers local repos and worktrees
    And cached remote snapshots exist on disk for at least one SSH host
    When the TUI calls rebuild_state_from_snapshot
    Then local worktrees come from the daemon workView snapshot
    And remote worktrees are merged in from orchard_snapshot::load_cached_snapshots
    And the final dashboard shows both local and remote rows

  @integration
  Scenario: Remote worktree rows carry the host tag
    Given a remote worktree was synced from host "boxd@vm.boxd.sh"
    When the TUI builds the dashboard rows
    Then that worktree's worktree_host is Some("boxd@vm.boxd.sh")
    And the dashboard row renders the remote host indicator

  @integration
  Scenario: Fork-host snapshot is taken before worktree refresh per (repo, remote)
    Given two repos share the same remote host
    When the full-refresh thread drives remote refresh
    Then snapshot_fork_hosts_for_remote is called for each (repo, remote) pair
    And the snapshot is captured before refresh_remote_worktrees runs
    And the snapshot is forwarded to refresh_remote_tmux_sessions as old_hosts

  @integration
  Scenario: Tmux refresh is deduped by (kind, host)
    Given two repos have remotes with the same host but different kinds (e.g. Remmy vs BoxdFork)
    When the full-refresh thread drives remote refresh
    Then refresh_remote_tmux_sessions is called once per unique (kind, host) pair
    And it is NOT called once per (repo, remote) for tmux

  @integration
  Scenario: Unreachable SSH host is skipped silently
    Given a remote host is not in the reachable set
    When the full-refresh thread drives remote refresh
    Then no refresh_remote_worktrees or refresh_remote_tmux_sessions call is made for that host
    And no crash or hard error is produced

  @integration
  Scenario: Transitive federation snapshots are merged
    Given the config has a OrchardProxy remote with allow_transitive: true
    And the transitive walker has written a depth-2+ snapshot to the cache
    When the TUI calls rebuild_state_from_snapshot
    Then load_cached_snapshots includes the transitive host snapshots
    And depth-2+ remote worktrees appear in the dashboard
