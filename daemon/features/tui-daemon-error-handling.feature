Feature: TUI daemon error handling
  As the orchard TUI
  I need to handle every daemon failure mode without crashing
  So that operators always have a usable dashboard even when the daemon is degraded.

  Background:
    Given the TUI is running and periodically fires workView queries

  # Failure modes from DaemonError:
  #   Unreachable { url, cause } — connection refused or timeout
  #   Transport(msg)             — network-level failure
  #   HttpStatus { status, body } — non-2xx HTTP response
  #   Parse(msg)                 — 2xx but unparseable response body
  #   GraphQl(errors)            — response carried GraphQL errors array

  Scenario: DaemonError::Unreachable shows the "daemon down" indicator
    Given the daemon is not listening on the configured URL
    When the TUI fires a workView query
    Then it receives DaemonError::Unreachable
    And it sends DaemonStatus::Unreachable
    And the header shows "daemon unreachable"
    And the last-known snapshot continues to power the dashboard

  Scenario: DaemonError::Transport does not crash the TUI
    Given a network-level error occurs (e.g. TLS failure, connection reset)
    When the TUI fires any query
    Then the error is logged at WARN level
    And the TUI remains running with the previous data

  Scenario: HTTP 500 from the daemon is handled gracefully
    Given the daemon returns HTTP 500 with a body
    When the TUI fires a workView query
    Then it receives DaemonError::HttpStatus { status: 500, body: ... }
    And it sends DaemonStatus::Unreachable
    And no raw HTTP error text is shown to the operator

  Scenario: Malformed JSON response does not crash the TUI
    Given the daemon returns 200 OK with invalid JSON
    When the TUI fires a workView query
    Then it receives DaemonError::Parse
    And the TUI logs a warning and falls back to the previous snapshot

  Scenario: GraphQL errors array in response is surfaced safely
    Given the daemon returns {"errors":[{"message":"introspection disabled"}],"data":null}
    When the TUI fires a workView query
    Then it receives DaemonError::GraphQl(["introspection disabled"])
    And the TUI does not crash
    And DaemonStatus::Unreachable is emitted

  Scenario: Concurrent refresh threads do not produce a data race
    Given a full refresh and a local refresh are both in flight
    When both return workView snapshots
    Then the Mutex-protected work_view_snapshot slot serialises the writes
    And the dashboard renders one coherent snapshot, not a mix of two

  Scenario: NullWorkViewSource fallback when client cannot be built
    Given ORCHARD_DAEMON_URL is set to an unparseable value at startup
    When the TUI constructs its daemon client
    Then it falls back to NullWorkViewSource
    And each refresh attempt logs a warning
    And the TUI starts and renders the disk-cached snapshot if available
