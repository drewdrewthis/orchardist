Feature: Federation — `Host.processes` returns the peer's process table, not the local one (#465)
  As an orchardist querying `peers { processes }` to see "which Claude session is on which host"
  I want each peer's processes field to return that peer's actual process list
  And I want unreachable peers to surface a typed error rather than silent local data
  So that federation answers match reality and confidently-wrong responses cannot mislead a decision

  # Scope: federates `Host.processes` only. Other Host.* subfields
  # (tmuxSessions, claudeInstances, hostServices, ...) are tracked
  # separately and will reuse the new `peerproxy.Provider.Query` transport.

  Background:
    Given `internal/server/providers/peerproxy/provider.go` exposes a generic
      `Provider.Query(ctx, peer, query, vars) (QueryResult, error)` forwarder
    And the local `hostResolver` knows how to identify a local-vs-peer Host via `isLocalHostNode`
    And the federation glue lives in `internal/server/resolvers/federate_peer_processes.go`
      so `gqlgen generate` does not rewrite it

  # =======================================================================
  # AC1 — Reachable peer returns peer-tagged process table
  # =======================================================================

  @e2e @issue-465
  Scenario: Querying `peers[].processes` against a reachable peer returns the peer's data
    Given a "remote" daemon with hostID "remote-host" and ps stub emitting one row tagged "remote-cmd" (pid 7777)
    And a "local" daemon with hostID "local-mac" and ps stub emitting one row tagged "local-cmd" (pid 1111)
    And the local daemon is configured with the remote as a peer
    And the local supervisor's reachability probe has succeeded
    When I query `{ host { peers { id processes { id pid command } } } }` against the local daemon
    Then `peers[0].id` equals "Host:remote-host"
    And `peers[0].processes` contains a row whose command is "remote-cmd"
    And that row's `id` equals "remote-host:7777" (peer-prefixed, not local)
    And `peers[0].processes` does NOT contain any row whose command is "local-cmd"

  # =======================================================================
  # AC2 — Unreachable peer surfaces a typed error (no silent local data)
  # =======================================================================

  @e2e @issue-465
  Scenario: Querying `peers[].processes` against an unreachable peer surfaces an error
    Given a "local" daemon with hostID "local-mac" and ps stub emitting "local-cmd" (pid 1111)
    And the local daemon is configured with peer "fork-orchardist-punch" pointing at a closed port
    And the local supervisor has marked that peer unreachable
    When I query `{ host { peers { processes { command } } } }` against the local daemon
    Then the GraphQL response includes an `errors` array mentioning the unreachable peer
    And `peers[0].processes` does NOT contain any row whose command is "local-cmd"
    And the local daemon's process table is NEVER returned in place of the peer's

  # =======================================================================
  # AC3 — Local host path is unchanged
  # =======================================================================

  @e2e @regression
  Scenario: Querying `host { processes }` on the local host still consults the local ps provider
    Given a daemon with hostID "local-mac" and a ps stub emitting "local-cmd" (pid 1111)
    When I query `{ host { processes { command } } }` against that daemon
    Then the response contains a row whose command is "local-cmd"
    And no peerproxy.Query call was issued (single-host flow stays local)

  # =======================================================================
  # AC4 — Process filter is forwarded to the peer
  # =======================================================================

  @unit @issue-465
  Scenario Outline: ProcessFilter projects to peer variables only when populated
    Given a `ProcessFilter` with <fields>
    When `buildProcessFilterVars` projects the filter
    Then the returned variable map equals <expectedVars>

    Examples:
      | fields                                                       | expectedVars                                                |
      | nil                                                          | nil                                                         |
      | empty struct                                                 | nil (avoids sending {} which the peer might treat as match-nothing) |
      | pidIn=[1,2,3]                                                | {pidIn:[1,2,3]}                                             |
      | commandIn=["claude"]                                         | {commandIn:["claude"]}                                      |
      | cwdPrefix="/home/boxd/"                                      | {cwdPrefix:"/home/boxd/"}                                   |
      | pidIn=[1,2,3] commandIn=["claude"] cwdPrefix="/home/boxd/"   | {pidIn:[...], commandIn:[...], cwdPrefix:"/home/boxd/"}     |

  # =======================================================================
  # AC5 — `process.host` resolves to the peer
  # =======================================================================

  @unit @issue-465
  Scenario: Decoded peer processes carry the peer's host pointer
    Given a peer named "boxd-vm" returned a `host.processes` body with two rows
    When `decodePeerProcesses` projects the response
    Then every returned `Process.Host.ID` equals "Host:boxd-vm"
    And every returned `Process.Host.MachineID` equals "boxd-vm"
    And every returned `Process.Host.Hostname` equals "boxd-vm"

  # =======================================================================
  # AC6 — Peerproxy transport extension
  # =======================================================================

  @unit @issue-465
  Scenario: peerproxy.Provider.Query forwards arbitrary GraphQL to a configured peer
    Given a peerproxy.Provider with peer "remote-host" configured
    When the resolver calls `Provider.Query(ctx, "remote-host", query, vars)`
    Then the call returns the decoded `QueryResult` from the peer
    And `Provider.Get` is unchanged (still a node-id forwarder)

  @unit @issue-465
  Scenario: peerproxy.Provider.Query rejects unknown peers
    Given a peerproxy.Provider with no peer named "ghost"
    When the resolver calls `Provider.Query(ctx, "ghost", query, vars)`
    Then it returns an error mentioning the unknown peer

  # =======================================================================
  # AC7 — gqlgen-safe layout
  # =======================================================================

  @structural @issue-465
  Scenario: Federation glue lives outside gqlgen-generated files
    Given `internal/server/resolvers/schema.resolvers.go` is regenerated from the schema
    Then `federate_peer_processes.go` is NOT regenerated and retains its hand-written content
    And `peer_helpers.go` is NOT regenerated and retains `isLocalHostNode`

  # =======================================================================
  # AC8 — Peer name derivation is robust to Host shape variations
  # =======================================================================

  @unit @issue-465
  Scenario Outline: peerNameFromHost picks the right field by precedence
    Given a `Host` shaped <input>
    When `peerNameFromHost` is called
    Then it returns <expectedName>

    Examples:
      | input                                                          | expectedName     |
      | { ID: "Host:other", MachineID: "boxd-1", Hostname: "fallback" }| "boxd-1"         |
      | { ID: "Host:other", Hostname: "via-hostname" }                 | "via-hostname"   |
      | { ID: "Host:via-id" }                                          | "via-id"         |
      | nil                                                            | ""               |
      | empty `{}`                                                     | ""               |
