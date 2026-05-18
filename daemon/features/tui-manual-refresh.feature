Feature: TUI manual refresh keystroke
  As the orchard TUI
  I need to trigger a full data refresh on demand
  So that operators can force an update without waiting for the timer.

  Background:
    Given the daemon is running on 127.0.0.1:7777
    And the TUI is in List view with existing snapshot data displayed

  # Keybinding: 'r' → Message::Refresh → start_full_refresh()
  # Keybinding: 'R' → Message::ReconnectHosts → reconnect_unreachable_hosts()

  Scenario: 'r' keystroke triggers a full refresh
    When the operator presses 'r'
    Then the TUI fires a workView query to the daemon
    And it also probes SSH remote hosts for reachability
    And on success it delivers DaemonStatus::Reachable
    And on success it delivers CacheRefreshed
    And the dashboard rows update to reflect the new data

  Scenario: 'r' during an active refresh does not double-fire
    Given a refresh is already in progress
    When the operator presses 'r'
    Then the in-progress refresh completes normally
    And no duplicate workView queries are issued that would produce a race

  Scenario: 'r' while daemon is unreachable shows the error indicator
    Given the daemon is not reachable
    When the operator presses 'r'
    Then the TUI fires the workView query and receives an error
    And it emits DaemonStatus::Unreachable
    And the "daemon unreachable" indicator appears in the header

  Scenario: 'R' keystroke reconnects unreachable SSH hosts
    Given one or more SSH remote hosts are marked unreachable
    When the operator presses 'R'
    Then the TUI re-probes each unreachable host for SSH reachability
    And a "reconnecting..." warning is shown during the probe
    And on success the host is marked reachable and its remote data is refreshed
