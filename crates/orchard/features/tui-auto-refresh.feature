Feature: TUI auto-refresh poll cycle
  As the orchard TUI
  I need to poll the daemon on a timed interval
  So that operators see state changes without pressing a key.

  Background:
    Given the daemon is running on 127.0.0.1:7777
    And the TUI is running in its main event loop

  # Refresh intervals (from tui/mod.rs constants):
  #   LOCAL_REFRESH_SECS = 5   → workView query (local data only path)
  #   FULL_REFRESH_SECS  = 60  → workView query + SSH reachability probes + remote refresh

  @integration
  Scenario: Local refresh fires every 5 seconds
    Given 5 seconds have elapsed since the last refresh
    When the TUI's auto-refresh timer fires
    Then the TUI fires a workView query to the local daemon
    And on success it sends a DaemonStatus::Reachable signal
    And on success it sends a LocalCacheRefreshed message
    And the dashboard re-derives rows from the fresh snapshot

  @integration
  Scenario: Full refresh fires every 60 seconds
    Given 60 seconds have elapsed since the last full refresh
    When the TUI's full-refresh timer fires
    Then the TUI probes each configured SSH remote host for reachability
    And the TUI fires a workView query to the local daemon
    And on success it sends a DaemonStatus::Reachable signal
    And on success it sends a CacheRefreshed message
    And it runs remote worktree + tmux refresh for reachable SSH hosts

  @integration
  Scenario: Daemon unreachable during auto-refresh
    Given the daemon does not respond within the 5-second client timeout
    When the auto-refresh workView query times out or is refused
    Then the TUI sends a DaemonStatus::Unreachable signal
    And the dashboard continues rendering the previous snapshot
    And the header shows a "daemon unreachable" indicator

  @integration
  Scenario: Daemon recovers between refresh cycles
    Given the previous refresh returned DaemonStatus::Unreachable
    When the next auto-refresh fires and the workView query succeeds
    Then the TUI sends DaemonStatus::Reachable
    And the "daemon unreachable" indicator is removed from the header
    And the fresh data replaces the stale snapshot

  @integration
  Scenario: State change is reflected on next local refresh
    Given the dashboard rendered snapshot N at time T
    When a worktree's branch advances (commit pushed) or a PR state changes daemon-side
    And 5 seconds have elapsed
    Then the next local refresh delivers the updated workView snapshot
    And the dashboard row reflects the new branch/PR data
