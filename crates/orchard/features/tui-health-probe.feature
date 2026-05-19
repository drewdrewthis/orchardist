Feature: TUI daemon health probe
  As the orchard TUI
  I need to confirm the daemon is alive before relying on its data
  So that I can surface a clear "daemon down" indicator rather than silently showing stale data.

  Background:
    Given the TUI daemon client targets http://127.0.0.1:7777/graphql by default
    And the URL can be overridden via the ORCHARD_DAEMON_URL environment variable

  # Query: { health { status uptimeS } }
  # Used as a connectivity smoke-check; the TUI does NOT call health explicitly
  # today — reachability is inferred from workView success/failure. This feature
  # documents the health query's contract so the daemon must keep it stable.

  @integration
  Scenario: health query returns status "ok" when the daemon is serving
    When a client sends { health { status uptimeS } }
    Then the response contains health.status == "ok"
    And health.uptimeS is a non-negative integer

  @integration
  Scenario: health query is rejected with a GraphQL error when the daemon is degraded
    Given the daemon is in a degraded state
    When a client sends { health { status uptimeS } }
    Then the response either returns health.status != "ok"
    Or the response carries a top-level GraphQL error entry

  @integration
  Scenario: ORCHARD_DAEMON_URL override is respected
    Given ORCHARD_DAEMON_URL is set to a non-default endpoint
    When the TUI boots and constructs its daemon client
    Then all queries (including workView) are sent to the overridden URL
    And the default 127.0.0.1:7777 endpoint is never contacted

  @integration
  Scenario: Client timeout is 5 seconds per request
    Given the daemon endpoint hangs and does not respond
    When the TUI fires a workView query
    Then the request times out after 5 seconds
    And the TUI receives DaemonError::Unreachable rather than blocking indefinitely
