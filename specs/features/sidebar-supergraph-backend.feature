# orchardist#844 — sidebar: read from supergraph (127.0.0.1:7788) behind an endpoint switch
Feature: sidebar supergraph backend switch
  As someone running orchard-sidebar
  I want to point it at the supergraph endpoint instead of the daemon via an
    environment variable
  So that the sidebar can run against either backend while both are
    maintained in parallel, with zero behavior change for anyone who does
    nothing

  Background:
    Given orchard-sidebar reads ORCHARD_SIDEBAR_BACKEND ("daemon" or "supergraph", default "daemon")
    And orchard-sidebar reads ORCHARD_SIDEBAR_GRAPHQL_URL as an optional override

  @unit
  Scenario: Default backend is unchanged
    Given ORCHARD_SIDEBAR_BACKEND is unset
    When orchard-sidebar resolves its endpoints
    Then the HTTP URL resolves to "http://127.0.0.1:7777/graphql"
    And the WS URL resolves to "ws://127.0.0.1:7777/graphql"

  @unit
  Scenario Outline: Backend selection resolves the documented default URLs
    Given ORCHARD_SIDEBAR_BACKEND is "<backend>"
    And ORCHARD_SIDEBAR_GRAPHQL_URL is unset
    When orchard-sidebar resolves its endpoints
    Then the HTTP URL resolves to "<http_url>"
    And the WS URL resolves to "<ws_url>"

    Examples:
      | backend    | http_url                       | ws_url                        |
      | daemon     | http://127.0.0.1:7777/graphql  | ws://127.0.0.1:7777/graphql   |
      | supergraph | http://127.0.0.1:7788/graphql  | ws://127.0.0.1:7788/graphql   |

  @unit
  Scenario Outline: An explicit GraphQL URL override derives its WS URL by scheme swap, not by string match
    Given ORCHARD_SIDEBAR_BACKEND is "supergraph"
    And ORCHARD_SIDEBAR_GRAPHQL_URL is "<override_url>"
    When orchard-sidebar resolves its endpoints
    Then the HTTP URL resolves to "<override_url>"
    And the WS URL resolves to "<derived_ws_url>"

    Examples:
      | override_url                              | derived_ws_url                            |
      | http://10.0.0.5:9000/graphql               | ws://10.0.0.5:9000/graphql                |
      | https://supergraph.example.com/graphql      | wss://supergraph.example.com/graphql      |

  @unit
  Scenario: An unknown backend value fails loudly before any I/O
    Given ORCHARD_SIDEBAR_BACKEND is "bogus"
    When orchard-sidebar starts
    Then it exits non-zero
    And stderr names the invalid value "bogus" and the valid options "daemon" and "supergraph"
    And no tmux or network call is attempted

  @unit
  Scenario: A malformed GraphQL URL override fails loudly before any I/O
    Given ORCHARD_SIDEBAR_GRAPHQL_URL is "not-a-url"
    When orchard-sidebar starts
    Then it exits non-zero
    And stderr names the invalid value "not-a-url"
    And no outbound connection is attempted

  @integration
  Scenario: Supergraph unreachable degrades like a daemon-down failure, without a panic
    Given ORCHARD_SIDEBAR_BACKEND is "supergraph"
    And ORCHARD_SIDEBAR_GRAPHQL_URL points at a port with nothing listening
    When the fast lane polls
    Then the poll fails within its existing timeout
    And the sidebar sets its degraded-lane failure state instead of crashing
    And the subscription dialer's existing backoff/redial loop handles the dial failure without exiting the process

  @integration
  Scenario: Supergraph fast lane fills rows from typed fields, not workView
    Given a stub supergraph HTTP server returning fixture claudeInstances, tmuxSessions and tmuxPanes data
    And ORCHARD_SIDEBAR_BACKEND is "supergraph" pointed at that stub
    When the fast lane polls
    Then the resulting row list has one row per tmux session in the fixture
    And a row whose session matches a claudeInstances entry has that entry's state and model
    And a tmux session with no matching claudeInstances entry still produces a row, with no panic

  @integration
  Scenario: Supergraph slow lane enriches a row via issue, pullRequest and paneForBranch
    Given a stub supergraph HTTP server returning a fixture issue for a worktree's branch
    And ORCHARD_SIDEBAR_BACKEND is "supergraph" pointed at that stub
    When the slow lane polls
    Then the enriched row's issue number and title match the fixture

  @integration
  Scenario: Fields supergraph does not expose render as an em dash, never a fabricated value
    Given a stub supergraph backend whose fixtures omit PR draft, reviewDecision, statusCheckRollup, mergeStateStatus, worktree ahead/behind, and pane title
    When the sidebar composes its view against that stub
    Then each of those fields renders as "—"
    And a parallel daemon-backend fixture that supplies real values for the same fields does not render "—" for them

  @integration
  Scenario: Health reduction surfaces a named degraded plugin
    Given a stub supergraph /health endpoint reporting one plugin at a non-"ok" state
    And ORCHARD_SIDEBAR_BACKEND is "supergraph" pointed at that stub
    When the sidebar reads health
    Then the sidebar's failure-reason surface names that plugin

  @integration
  Scenario: Supergraph push lane triggers a fast-lane refetch
    Given a stub graphql-transport-ws server that completes the connection_init/connection_ack/subscribe handshake
    And ORCHARD_SIDEBAR_BACKEND is "supergraph" pointed at that stub
    When the stub sends one tmuxEvents "next" frame
    Then the sidebar issues a new fast-lane fetch within the test's timeout

  @integration
  Scenario: New supergraph tests are race-clean alongside the existing suite
    Given the full cmd/orchard-sidebar test suite, excluding the ORCHARD_LIVE-gated live test
    When it runs under go test -race
    Then every test passes
    And no data race is reported

  @e2e
  Scenario: Use-proof against the real, running supergraph
    Given the orchard-sidebar binary is built
    And ORCHARD_SIDEBAR_BACKEND=supergraph and ORCHARD_SIDEBAR_GRAPHQL_URL=http://127.0.0.1:7788/graphql
    When the binary runs against the live local supergraph
    Then the rendered sidebar shows at least one real tmux-session row
    And the binary's own log output shows zero panics or fatal errors and the resolved row count
    And a screenshot of the rendered pane is captured

  @unit
  Scenario: No new client-side git/gh/tmux exec is introduced for supergraph reads
    Given the supergraph adapter code added by this change
    When its calls are inspected
    Then every read reaches ORCHARD_SIDEBAR_GRAPHQL_URL over HTTP or WS
    And no new exec.Command call to git, gh, or tmux exists outside the pre-existing client.go seam

  # --- AC Coverage Map ---
  # AC 1: "Default backend unchanged" → Scenario: Default backend is unchanged
  # AC 2: "Explicit backend selection resolves the documented URLs, WS derivation is general" → Scenario Outline: Backend selection resolves the documented default URLs; Scenario Outline: An explicit GraphQL URL override derives its WS URL by scheme swap, not by string match
  # AC 3: "Unknown backend value / malformed URL override fails loudly at startup" → Scenario: An unknown backend value fails loudly before any I/O; Scenario: A malformed GraphQL URL override fails loudly before any I/O
  # AC 4: "Supergraph unreachable does not panic or hang" → Scenario: Supergraph unreachable degrades like a daemon-down failure, without a panic
  # AC 5: "Supergraph fast lane fills the row model from typed fields, not workView" → Scenario: Supergraph fast lane fills rows from typed fields, not workView
  # AC 6: "Supergraph slow lane enriches via issue/pullRequest/paneForBranch" → Scenario: Supergraph slow lane enriches a row via issue, pullRequest and paneForBranch
  # AC 7: "Fields supergraph doesn't expose render as em-dash, never fabricated" → Scenario: Fields supergraph does not expose render as an em dash, never a fabricated value
  # AC 8: "Health reduction surfaces a named degraded plugin" → Scenario: Health reduction surfaces a named degraded plugin
  # AC 9: "Supergraph push lane triggers a fast-lane refetch" → Scenario: Supergraph push lane triggers a fast-lane refetch
  # AC 10: "Regression: all tests including new ones are race-clean" → Scenario: New supergraph tests are race-clean alongside the existing suite
  # AC 11: "Use-proof against the real, running supergraph, verifiable beyond the screenshot" → Scenario: Use-proof against the real, running supergraph
  # AC 12: "No client-side git/gh/tmux exec added for supergraph reads" → Scenario: No new client-side git/gh/tmux exec is introduced for supergraph reads
