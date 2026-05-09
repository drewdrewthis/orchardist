Feature: Daemon — `tmuxPane.process` is wired so `panes { process { cwd } }` resolves (#463)
  As an orchardist or daemon client running `tmuxSessions { windows { panes { process { cwd } } } }`
  I want the natural traversal `Session → Window → Pane → Process → cwd` to return populated nodes
  So that I do not need a two-query workaround (`Host.processes(filter:)` + client-side join) just to learn which session is sitting in which worktree

  # Scope: replaces the `tmuxPaneResolver.Process` stub at
  # `internal/server/resolvers/schema.resolvers.go:695-697` with a real resolver
  # that joins `pane.CurrentPid` to the ps cache and projects via the existing
  # `projectProcess` helper. Also parallelizes the now-reachable lsof fan-out in
  # `internal/server/providers/ps/adapter.go:fetchCwdsDarwin` and updates schema
  # docs. Out of scope: `claudeInstance.process` / `claudeInstance.pane` (#468),
  # Linux `/proc/<pid>/cwd` reader, new opt-in flags / fields.

  Background:
    Given the daemon's tmux provider is configured to return panes with deterministic `currentPid` values
    And the ps provider is configured with a fake adapter whose `lsofCwd` is deterministic and instrumented
    And `r.PS` (process service) is non-nil in the resolver harness
    And `r.Tmux` (tmux service) exposes the helpers `lookupPane` and `splitTmuxPaneID`

  # =======================================================================
  # AC1 — Populated Process node for live panes (happy path)
  # =======================================================================

  @e2e @issue-463
  Scenario: Querying `panes { process { pid command cwd } }` returns a populated Process for live panes
    Given a tmux pane "TmuxPane:local-mac:%5" whose `currentPid` is 4242
    And the ps cache contains a row with pid 4242, command "claude", cwd "/tmp/orchard-pane-process-test"
    When I issue `{ tmuxSessions { windows { panes { process { pid command cwd } } } } }` against the daemon
    Then the response's pane has a non-null `process` node
    And `process.pid` equals 4242
    And `process.command` equals "claude"
    And `process.cwd` equals "/tmp/orchard-pane-process-test"
    And no GraphQL `errors` array is present

  # =======================================================================
  # AC2 — Honest null when no current pid or pid not in ps cache
  # =======================================================================

  @integration @issue-463
  Scenario: Pane with `currentPid == 0` resolves to null process (no error, no panic)
    Given a tmux pane "TmuxPane:local-mac:%6" whose `currentPid` is 0
    When I issue `{ tmuxSessions { windows { panes { process { pid } } } } }` against the daemon
    Then the response's pane has `process: null`
    And no GraphQL `errors` array is present

  @integration @issue-463
  Scenario: Pane whose pid has exited / is not in the ps cache resolves to null process
    Given a tmux pane "TmuxPane:local-mac:%7" whose `currentPid` is 9999
    And the ps cache does NOT contain any row with pid 9999
    When I issue `{ tmuxSessions { windows { panes { process { pid } } } } }` against the daemon
    Then the response's pane has `process: null`
    And the resolver did NOT trigger an adapter fallback shellout (cache-only lookup)
    And no GraphQL `errors` array is present

  # =======================================================================
  # AC3 — Federated pane ids project Process nodes onto the peer's host
  # =======================================================================

  @integration @issue-463 @federation
  Scenario: A federated pane id produces a Process node tagged with the peer's host id
    Given the local daemon has hostID "local-mac"
    And a tmux pane id "TmuxPane:peer-host:%26" reaches the resolver (e.g. via `Host(id: "Host:peer-host") { tmuxSessions { ... } }` or pre-constructed in a test harness)
    And the ps cache contains a row with `host = "peer-host"`, pid 8080, command "node"
    When the resolver resolves `pane.process` for that pane
    Then the returned `Process.id` equals "peer-host:8080"
    And the returned `Process.host.id` equals "Host:peer-host"
    And the returned `Process.id` does NOT begin with "local-mac:"

  # =======================================================================
  # AC4 — `lsof` fan-out for `panes { process { cwd } }` is not serial
  # =======================================================================

  @unit @issue-463
  Scenario: fetchCwdsDarwin does not invoke `lsofCwd` serially N times for N pids
    Given the ps adapter's `fetchCwdsDarwin` is invoked with 50 distinct pids
    And the fake `lsofCwd` records the timestamp of every invocation and sleeps 10ms
    When `fetchCwdsDarwin` returns
    Then either:
      | strategy                   | assertion                                                                |
      | bounded-concurrency errgroup | concurrent `lsofCwd` invocations observed at any instant <= 16         |
      | batched multi-pid lsof     | exactly 1 lsof invocation observed, with all 50 pids passed via -p flags |
    And the elapsed time is materially less than `50 * 10ms` (i.e. parallelism / batching actually happened)

  @integration @issue-463
  Scenario: `panes { process { cwd } }` against many panes does not trigger N serial shellouts
    Given the daemon has 30 panes whose `currentPid` values map to 30 distinct ps cache rows
    And the fake `lsofCwd` records every invocation
    When I issue `{ tmuxSessions { windows { panes { process { cwd } } } } }` against the daemon
    Then the recorded `lsofCwd` invocations either:
      | bounded-concurrency: at most 16 in flight at any time |
      | batched: exactly 1 invocation covering all 30 pids   |
    And every pane in the response has a populated `process.cwd`

  # =======================================================================
  # AC5 — Schema docs updated
  # =======================================================================

  @structural @issue-463
  Scenario: `TmuxPane.process` schema doc no longer claims the field is unwired
    Given `internal/server/resolvers/schema.graphql` is the source of truth
    When I read the doc string for the `process` field on `TmuxPane`
    Then it does NOT contain the substring "Null until ws-b-ps wires it"
    And it states that the field returns the OS-level Process for the foreground pid
    And it states that null means tmux reported no current pid OR the pid is no longer in the ps cache

  @structural @issue-463
  Scenario: `Process.cwd` schema doc clarifies the de-facto opt-in path
    Given `internal/server/resolvers/schema.graphql` is the source of truth
    When I read the doc string for the `cwd` field on `Process`
    Then it states that traversal via `pane.process.cwd` is the de-facto opt-in for the lsof slow path
    And it does NOT introduce a new opt-in flag or argument

  # =======================================================================
  # AC6 — End-to-end regression test that fails on the stub and passes after wiring
  # =======================================================================

  @e2e @issue-463 @regression
  Scenario: End-to-end traversal `pane → process → cwd` against fake providers
    Given a wired-up resolver harness backed by a fake `Tmux` provider and a fake `PS` provider
    And the fake providers report a pane with `currentPid = 1234` whose ps row has cwd "/tmp/orchard"
    When the test issues `{ tmuxSessions { windows { panes { process { pid cwd } } } } }`
    Then every pane's `process` is non-null
    And every pane's `process.cwd` equals "/tmp/orchard"
    And the test FAILS when run against the pre-fix `return nil, nil` stub
    And the test PASSES when run against the wired resolver

  # =======================================================================
  # AC7 — Linux behaviour is unchanged (cwd silently null until /proc reader lands)
  # =======================================================================

  @integration @issue-463 @linux
  Scenario: On Linux, `pane.process` is populated but `pane.process.cwd` remains null
    Given the daemon is running on Linux (GOOS=linux)
    And a tmux pane whose `currentPid` is 4242
    And the ps cache contains a row with pid 4242 and command "node"
    When I issue `{ tmuxSessions { windows { panes { process { pid command cwd } } } } }` against the daemon
    Then the pane's `process` is non-null
    And `process.pid` equals 4242
    And `process.command` equals "node"
    And `process.cwd` is null
    And no GraphQL `errors` array is present
    # Implementing /proc/<pid>/cwd is explicitly out of scope; this asymmetry is documented.

  # =======================================================================
  # AC8 — `claudeInstance` resolvers are NOT touched in this change
  # =======================================================================

  @structural @issue-463 @scope-guard
  Scenario: `claudeInstanceResolver.Process` and `claudeInstanceResolver.Pane` are unchanged
    Given the diff for this PR
    When I inspect `internal/server/resolvers/schema.resolvers.go`
    Then the bodies of `claudeInstanceResolver.Process` and `claudeInstanceResolver.Pane` are byte-identical to the pre-PR baseline
    # The same bug shape on `claudeInstance.*` is tracked separately as #468.

  # =======================================================================
  # AC Coverage Map
  # =======================================================================
  # AC1 (populated Process for live panes) → "Querying `panes { process { pid command cwd } }` returns a populated Process for live panes"
  # AC2 (null on `currentPid == 0` or pid missing from cache, no error/panic) → "Pane with `currentPid == 0` resolves to null process" + "Pane whose pid has exited / is not in the ps cache resolves to null process"
  # AC3 (federated pane id projects Process onto peer host) → "A federated pane id produces a Process node tagged with the peer's host id"
  # AC4 (no serial N-times lsof) → "fetchCwdsDarwin does not invoke `lsofCwd` serially N times for N pids" + "`panes { process { cwd } }` against many panes does not trigger N serial shellouts"
  # AC5 (schema doc updates) → "`TmuxPane.process` schema doc no longer claims the field is unwired" + "`Process.cwd` schema doc clarifies the de-facto opt-in path"
  # AC6 (regression test failing on stub, passing after wiring) → "End-to-end traversal `pane → process → cwd` against fake providers"
  # AC7 (Linux unchanged — cwd null) → "On Linux, `pane.process` is populated but `pane.process.cwd` remains null"
  # AC8 (claudeInstance untouched) → "`claudeInstanceResolver.Process` and `claudeInstanceResolver.Pane` are unchanged"
