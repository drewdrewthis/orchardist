Feature: TUI federated session switcher
  As the orchard TUI
  I need to list tmux sessions across all known hosts
  So that the operator can switch to any session — local or remote — from one screen.

  Background:
    Given the local daemon is running on 127.0.0.1:7777
    And the local daemon knows about one or more peer hosts via Query.hosts

  # Queries fired:
  #   1. { hosts { id hostname address reachable peers { id hostname address reachable } } }
  #   2. { tmuxSessions { id name attached activeAttached lastActivityAt } }
  #      → repeated per reachable peer at https://graphql.<peer-address>/graphql
  #
  # Note: the TUI fans out to peers itself (daemon::federated::fan_out).
  # The daemon does NOT aggregate peer sessions in workView today (Workstream F).

  @integration
  Scenario: Local sessions fetched via tmuxSessions query
    When the TUI fetches local sessions
    Then it calls Query.tmuxSessions on the local daemon
    And the response carries id, name, attached, activeAttached, lastActivityAt for each session
    And sessions with activeAttached == true are highlighted as active

  @integration
  Scenario: Peer hosts resolved via hosts query
    When the TUI begins a federated fan-out
    Then it calls Query.hosts on the local daemon
    And the response carries a hosts array with id, hostname, address, reachable, and peers
    And peers are nested under the host that reported them
    And only peers with reachable == true and a non-empty address are queried for sessions

  @integration
  Scenario: Fan-out reaches each reachable peer
    Given the local daemon reports two reachable peers with addresses
    When the TUI executes fan_out
    Then it sends a tmuxSessions query to each peer's GraphQL endpoint
      (https://graphql.<peer-address>/graphql)
    And sessions from all reachable peers are merged into the session list
    And each session row is tagged with its host label (local hostname or peer hostname)

  @integration
  Scenario: Unreachable peer does not block the local session list
    Given one peer is reachable and one is unreachable
    When the TUI executes fan_out
    Then local and reachable-peer sessions appear in the list
    And the failed peer contributes a "host unreachable" status row
    And the overall fan-out completes within 2× the per-peer timeout

  @integration
  Scenario: Peer with empty address is silently skipped
    Given the daemon reports a peer with an empty or null address
    When the TUI evaluates the peer during fan_out
    Then no HTTP request is issued for that peer
    And no error row is emitted for the empty-address peer

  @integration
  Scenario: peer_url construction rule
    Given a peer has address "box-1.boxd.sh" (bare hostname)
    Then the TUI constructs "https://graphql.box-1.boxd.sh/graphql" as the endpoint
    And if the address already starts with "graphql." it is prefixed with "https://" only
    And if the address is already a full "http(s)://" URL it is used as-is
