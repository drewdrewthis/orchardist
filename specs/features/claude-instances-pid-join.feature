Feature: claudeInstances pid join — wire heartbeats to live processes
  As an orchardist consuming the orchard daemon's GraphQL schema
  I want every ClaudeInstance whose heartbeat session hosts a live claude process
  to expose a non-null `process` and `pane`, with `state` reflecting real liveness
  So that the dashboard can route work by ClaudeInstance instead of falling back
  to `host.processes(filter: { commandIn: ["claude"] })`

  # Issue #468. Three independent gaps in production wiring:
  #   1. Composer constructed with `nil, nil, nil` finders in daemon.go:151
  #   2. Hook script doesn't carry claude_pid (out of scope per AC 10)
  #   3. tmuxPaneResolver.Process and .ClaudeInstance are stubbed (nil, nil)
  #
  # Fix scope (AC #1–#5):
  #   - Add three thin adapters: ProcessFinder, PaneFinder, AccountFinder
  #   - Replace `nil, nil, nil` in daemon.go with those adapters
  #   - Implement tmuxPaneResolver.Process and tmuxPaneResolver.ClaudeInstance
  #   - Source pid from pane.CurrentPid (NOT from heartbeat — out of scope)

  Background:
    Given the daemon serves a GraphQL schema at 127.0.0.1:7777
    And the ClaudeInstance type exists with fields (process, pane, account, state, sessionUuid, ...)
    And the ps.Provider, tmux.Provider, and claudeaccount.Provider already snapshot live host data
    And the claudeinstance composer's join logic is unit-tested with fakes (composer_test.go)
    And `pidFromPaneID` remains a no-op stub (AC 9)

  # ===================================================================
  # AC 1 — Production composer wired with concrete finders
  # AC 2 — Three production adapters exist
  # ===================================================================

  @unit
  Scenario: ProcessFinder.FindByPid wraps ps.Provider.Get and returns a Process for a live pid
    Given a ps.Provider snapshot containing a process with pid 88631 on host "local"
    When ProcessFinder.FindByPid is called with host "local" and pid 88631
    Then it returns the corresponding *graphql.Process
    And the returned Process.pid equals 88631

  @unit
  Scenario: ProcessFinder.FindByPid returns nil for a pid that is not present in the ps snapshot
    Given a ps.Provider snapshot that does not contain pid 99999 on host "local"
    When ProcessFinder.FindByPid is called with host "local" and pid 99999
    Then it returns nil with no error

  @unit
  Scenario: PaneFinder.FindByPid walks tmux.Provider panes and returns the pane whose currentPid matches
    Given a tmux.Provider snapshot with pane "%59" reporting currentPid 88631 on host "local"
    When PaneFinder.FindByPid is called with host "local" and pid 88631
    Then it returns the *graphql.TmuxPane for pane "%59"

  @unit
  Scenario: PaneFinder.FindByPid returns nil when no pane on the host has the given currentPid
    Given a tmux.Provider snapshot with no pane reporting currentPid 88631 on host "local"
    When PaneFinder.FindByPid is called with host "local" and pid 88631
    Then it returns nil with no error

  @unit
  Scenario: PaneFinder.FindBySession returns the first pane whose currentPid is owned by a claude process
    Given a tmux session "issue468" on host "local" with two panes
    And pane "%10" has currentPid 1000 owned by command "vim"
    And pane "%11" has currentPid 2000 owned by command "claude"
    When PaneFinder.FindBySession is called with host "local" and session "issue468"
    Then it returns the *graphql.TmuxPane for pane "%11"

  @unit
  Scenario: PaneFinder.FindBySession returns nil when no pane in the session runs a claude process
    Given a tmux session "no-claude-here" on host "local" with two panes
    And no pane in the session has a currentPid owned by command "claude"
    When PaneFinder.FindBySession is called with host "local" and session "no-claude-here"
    Then it returns nil with no error

  @unit
  Scenario: PaneFinder.FindBySession returns nil for a session that does not exist
    Given a tmux.Provider snapshot containing no session named "ghost-session" on host "local"
    When PaneFinder.FindBySession is called with host "local" and session "ghost-session"
    Then it returns nil with no error

  @unit
  Scenario: AccountFinder.Active wraps claudeaccount.Provider.Active and returns the active account
    Given a claudeaccount.Provider whose Active(ctx) returns a populated *ClaudeAccount on host "local"
    When AccountFinder.Active is called with host "local"
    Then it returns the same *ClaudeAccount value

  @unit
  Scenario: AccountFinder.Active returns nil when claudeaccount.Provider has no active account
    Given a claudeaccount.Provider whose Active(ctx) returns nil on host "local"
    When AccountFinder.Active is called with host "local"
    Then it returns nil with no error

  @integration
  Scenario: Daemon wires the composer with non-nil finders for tmux, ps, and claude account
    Given internal/cli/daemon/daemon.go is reviewed
    When claudeinstance.NewComposer is constructed
    Then its PaneFinder argument is a non-nil adapter wrapping the live tmux.Provider
    And its ProcessFinder argument is a non-nil adapter wrapping the live ps.Provider
    And its AccountFinder argument is a non-nil adapter wrapping the live claudeaccount.Provider
    And the literal `claudeinstance.NewComposer("local", nil, nil, nil)` no longer appears in the file

  # ===================================================================
  # AC 3 — tmuxPaneResolver.Process resolves to a non-null Process
  # ===================================================================

  @integration
  Scenario: tmuxPaneResolver.Process returns the Process whose pid matches pane.currentPid
    Given a TmuxPane with id "%59" and currentPid 88631 on host "local"
    And ps.Provider knows of a process with pid 88631, command "claude" on host "local"
    When the GraphQL query selects tmuxPanes { process { pid command } } for that pane
    Then the resolver returns a non-null Process
    And the returned Process.pid equals 88631
    And the returned Process.command contains "claude"

  @integration
  Scenario: tmuxPaneResolver.Process returns null when the pane's currentPid does not match any known process
    Given a TmuxPane with currentPid 99999 on host "local"
    And ps.Provider has no process with pid 99999 on host "local"
    When the GraphQL query selects tmuxPanes { process { pid } } for that pane
    Then the resolver returns null

  @integration
  Scenario: tmuxPaneResolver.Process returns null when the pane has no currentPid
    Given a TmuxPane whose currentPid is 0 / unset on host "local"
    When the GraphQL query selects tmuxPanes { process { pid } } for that pane
    Then the resolver returns null
    And ps.Provider is not consulted

  # ===================================================================
  # AC 4 — tmuxPaneResolver.ClaudeInstance resolves to a non-null ClaudeInstance
  # ===================================================================

  @integration
  Scenario: tmuxPaneResolver.ClaudeInstance returns the ClaudeInstance whose pane.id matches the obj
    Given a TmuxPane with id "%59" on host "local"
    And the claudeinstance.Provider has an instance whose matched pane.id is "%59"
    When the GraphQL query selects tmuxPanes { claudeInstance { sessionUuid } } for that pane
    Then the resolver returns a non-null ClaudeInstance
    And the returned ClaudeInstance's pane.id equals "%59"

  @integration
  Scenario: tmuxPaneResolver.ClaudeInstance returns null when no heartbeat maps to the pane
    Given a TmuxPane with id "%99" on host "local"
    And no claudeinstance.Provider entry has a matched pane with id "%99"
    When the GraphQL query selects tmuxPanes { claudeInstance } for that pane
    Then the resolver returns null

  # ===================================================================
  # AC 5 — claudeInstances[].process is non-null when pane has a live claude pid
  # AC 6 — claudeInstances[].pane is non-null when heartbeat tmux_session matches a live session
  # ===================================================================

  @integration
  Scenario: claudeInstances[].pane resolves when the heartbeat's tmux_session matches a live tmux session
    Given a heartbeat file with tmux_session "orchardist" on host "local"
    And a tmux session "orchardist" on host "local" with a pane whose currentPid is owned by a claude process
    When the GraphQL query selects claudeInstances { pane { id } }
    Then the instance whose heartbeat tmux_session is "orchardist" reports a non-null pane
    And the pane's id matches the matched tmux pane

  @integration
  Scenario: claudeInstances[].process resolves to a non-null Process when the matched pane has a claude currentPid
    Given a heartbeat file with tmux_session "orchardist" on host "local"
    And a tmux session "orchardist" on host "local" with a pane reporting currentPid 88631
    And ps.Provider knows pid 88631 with command "claude" on host "local"
    When the GraphQL query selects claudeInstances { process { pid } }
    Then the instance reports a non-null process
    And process.pid equals 88631

  @integration
  Scenario: claudeInstances[].process is null when the heartbeat's tmux_session does not match any live tmux session
    Given a heartbeat file with tmux_session "long-gone-session" on host "local"
    And no tmux session named "long-gone-session" exists in the tmux snapshot
    When the GraphQL query selects claudeInstances { process pane }
    Then the instance reports a null process
    And the instance reports a null pane

  @unit
  Scenario: composer.resolvePid prefers heartbeat ClaudePid over pane.CurrentPid when ClaudePid is non-zero
    Given a heartbeat with ClaudePid 12345 on host "local"
    And a matched pane reporting currentPid 88631
    When composer.resolvePid is invoked
    Then it returns 12345

  @unit
  Scenario: composer.resolvePid falls back to pane.CurrentPid when heartbeat ClaudePid is zero and a pane is matched
    Given a heartbeat with ClaudePid 0 on host "local"
    And a matched pane reporting currentPid 88631
    When composer.resolvePid is invoked
    Then it returns 88631

  @unit
  Scenario: composer.resolvePid returns 0 when heartbeat ClaudePid is zero and no pane is matched
    Given a heartbeat with ClaudePid 0 on host "local"
    And no pane is matched (pane: nil)
    When composer.resolvePid is invoked
    Then it returns 0

  # ===================================================================
  # AC 7 — state reports working/idle/input (not no_claude) when heartbeat is fresh,
  #        state is one of those values, AND the pid corresponds to a live process
  # ===================================================================

  @integration
  Scenario: claudeInstances[].state reports working when heartbeat is fresh, says working, and pid is live
    Given a heartbeat with state "working" and a timestamp within the last 30 seconds
    And the resolved pid corresponds to a process present in ps.Provider
    When the GraphQL query selects claudeInstances { state }
    Then the instance state is "working"

  @integration
  Scenario: claudeInstances[].state reports idle when heartbeat says idle, is fresh, and pid is live
    Given a heartbeat with state "idle" and a timestamp within the last 30 seconds
    And the resolved pid corresponds to a process present in ps.Provider
    When the GraphQL query selects claudeInstances { state }
    Then the instance state is "idle"

  @integration
  Scenario: claudeInstances[].state reports input when heartbeat says input, is fresh, and pid is live
    Given a heartbeat with state "input" and a timestamp within the last 30 seconds
    And the resolved pid corresponds to a process present in ps.Provider
    When the GraphQL query selects claudeInstances { state }
    Then the instance state is "input"

  @integration
  Scenario: claudeInstances[].state falls back to no_claude when heartbeat is older than 30 seconds
    Given a heartbeat with state "working" and a timestamp older than 30 seconds
    And the resolved pid corresponds to a process present in ps.Provider
    When the GraphQL query selects claudeInstances { state }
    Then the instance state is "no_claude"

  @integration
  Scenario: claudeInstances[].state falls back to no_claude when the resolved pid is not live
    Given a heartbeat with state "working" and a timestamp within the last 30 seconds
    And the resolved pid does NOT correspond to any process present in ps.Provider
    When the GraphQL query selects claudeInstances { state }
    Then the instance state is "no_claude"

  @integration
  Scenario: claudeInstances[].state falls back to no_claude when heartbeat state is unrecognized
    Given a heartbeat with state "garbage-value" and a timestamp within the last 30 seconds
    And the resolved pid corresponds to a process present in ps.Provider
    When the GraphQL query selects claudeInstances { state }
    Then the instance state is "no_claude"

  # ===================================================================
  # AC 8 — End-to-end repro is fixed on a multi-claude host
  # ===================================================================

  @e2e
  Scenario: Host with N live claude processes reports N instances with non-null process, pane, and state != no_claude
    Given the daemon is running on 127.0.0.1:7777
    And the host has N live claude processes spread across N tmux sessions
    And N corresponding heartbeat files exist with state "working" and timestamps within the last 30 seconds
    When a GraphQL query selects { claudeInstances { state pane { id } process { pid } } }
    Then at least N rows have a non-null process
    And at least N rows have a non-null pane
    And at least N rows report a state other than "no_claude"

  @e2e
  Scenario: Original repro from issue 468 — 7 live claudes, 6 heartbeat-tracked instances
    Given the host has 7 live claude processes
    And the daemon has 6 heartbeat-tracked instances whose tmux_session each maps to a live tmux session running claude
    When a GraphQL query selects { claudeInstances { process { pid } pane { id } state } }
    Then 6 instances report a non-null process (was 0 before fix)
    And 6 instances report a non-null pane (was 0 before fix)
    And no more than 1 instance reports state "no_claude" (was 5 before fix)

  # ===================================================================
  # AC 9 — pidFromPaneID stub is left untouched
  # ===================================================================

  @unit
  Scenario: pidFromPaneID remains a no-op stub
    Given the composer.go pidFromPaneID function
    When the implementation is inspected
    Then the function body is unchanged from main
    And the function still returns 0 for any pane ID input
    And the doc comment still notes "no-op fallback today"

  # ===================================================================
  # AC 10 — Hook script change is out of scope; pid is sourced from pane.CurrentPid
  # ===================================================================

  @unit
  Scenario: Heartbeat parser still accepts both claudePid and claude_pid (no schema regression)
    Given a heartbeat file written with the field name "claudePid": 12345
    And another heartbeat file written with the field name "claude_pid": 67890
    When each file is parsed by adapter.go
    Then the first heartbeat's ClaudePid equals 12345
    And the second heartbeat's ClaudePid equals 67890

  @integration
  Scenario: Fix works on heartbeats that lack claudePid entirely
    Given a heartbeat file with no claudePid / claude_pid field
    And tmux_session "orchardist" exists with a pane whose currentPid is a live claude process
    When the GraphQL query selects claudeInstances { process { pid } pane { id } state }
    Then the instance reports a non-null process whose pid equals the pane's currentPid
    And the instance reports a non-null pane
    And the instance state is not "no_claude" (assuming heartbeat is fresh and state is working/idle/input)

  @unit
  Scenario: hook script orchard-state.sh is unchanged by this PR
    Given the hook script crates/orchard/hooks/orchard-state.sh on main
    When the file is compared after this PR
    Then the file content is byte-identical (the fix does not modify the hook)

  # ===================================================================
  # AC 11 — Tests pass
  # ===================================================================

  @e2e
  Scenario: go test for the changed packages is green
    Given the working tree contains all changes for issue 468
    When the developer runs `go test ./internal/server/providers/claudeinstance/... ./internal/server/resolvers/...`
    Then every test passes
    And new adapter unit tests are present alongside the new code
    And new resolver integration coverage exercises the composer chain end-to-end (not pure unit)

  # ===================================================================
  # AC 12 — No regressions for cases that were already null
  # ===================================================================

  @integration
  Scenario: Heartbeat with no matching tmux session still produces null process, null pane, no_claude
    Given a heartbeat file whose tmux_session matches no live tmux session on the host
    When the GraphQL query selects claudeInstances { process pane state }
    Then the instance reports a null process
    And the instance reports a null pane
    And the instance state is "no_claude"

  @integration
  Scenario: Query.tmuxSessions and Query.tmuxPanes return identical shape pre/post-fix
    Given the daemon snapshot is identical before and after the fix
    When a GraphQL query selects { tmuxSessions { id } tmuxPanes { id currentPid } }
    Then the response shape and identifiers are unchanged from main

  @integration
  Scenario: Query.host.processes returns identical shape pre/post-fix
    Given the same ps.Provider snapshot
    When a GraphQL query selects { host { processes(filter: { commandIn: ["claude"] }) { pid command } } }
    Then the response is unchanged from main

  @integration
  Scenario: claudeInstances top-level shape (length, ids) is unchanged for inputs with no possible join
    Given a daemon state where every heartbeat lacks a matching tmux session
    When a GraphQL query selects { claudeInstances { sessionUuid id } }
    Then the count and identifiers are identical to main's output for the same inputs

  # --- AC Coverage Map ---
  # AC 1: "Production composer is wired with concrete finders"
  #   -> @integration "Daemon wires the composer with non-nil finders for tmux, ps, and claude account"
  #
  # AC 2: "Three production adapters exist (ProcessFinder, PaneFinder, AccountFinder) with happy + not-found unit tests"
  #   -> @unit "ProcessFinder.FindByPid wraps ps.Provider.Get and returns a Process for a live pid"
  #   -> @unit "ProcessFinder.FindByPid returns nil for a pid that is not present in the ps snapshot"
  #   -> @unit "PaneFinder.FindByPid walks tmux.Provider panes and returns the pane whose currentPid matches"
  #   -> @unit "PaneFinder.FindByPid returns nil when no pane on the host has the given currentPid"
  #   -> @unit "PaneFinder.FindBySession returns the first pane whose currentPid is owned by a claude process"
  #   -> @unit "PaneFinder.FindBySession returns nil when no pane in the session runs a claude process"
  #   -> @unit "PaneFinder.FindBySession returns nil for a session that does not exist"
  #   -> @unit "AccountFinder.Active wraps claudeaccount.Provider.Active and returns the active account"
  #   -> @unit "AccountFinder.Active returns nil when claudeaccount.Provider has no active account"
  #
  # AC 3: "tmuxPaneResolver.Process resolves to a non-null Process when pane.currentPid matches a known process"
  #   -> @integration "tmuxPaneResolver.Process returns the Process whose pid matches pane.currentPid"
  #   -> @integration "tmuxPaneResolver.Process returns null when the pane's currentPid does not match any known process"
  #   -> @integration "tmuxPaneResolver.Process returns null when the pane has no currentPid"
  #
  # AC 4: "tmuxPaneResolver.ClaudeInstance resolves to a non-null ClaudeInstance when pane hosts a tracked claude"
  #   -> @integration "tmuxPaneResolver.ClaudeInstance returns the ClaudeInstance whose pane.id matches the obj"
  #   -> @integration "tmuxPaneResolver.ClaudeInstance returns null when no heartbeat maps to the pane"
  #
  # AC 5: "Query.claudeInstances[].process resolves to non-null when pane has a live claude pid"
  #   -> @integration "claudeInstances[].process resolves to a non-null Process when the matched pane has a claude currentPid"
  #   -> @integration "claudeInstances[].process is null when the heartbeat's tmux_session does not match any live tmux session"
  #   -> @unit "composer.resolvePid prefers heartbeat ClaudePid over pane.CurrentPid when ClaudePid is non-zero"
  #   -> @unit "composer.resolvePid falls back to pane.CurrentPid when heartbeat ClaudePid is zero and a pane is matched"
  #   -> @unit "composer.resolvePid returns 0 when heartbeat ClaudePid is zero and no pane is matched"
  #
  # AC 6: "Query.claudeInstances[].pane resolves to non-null when tmux_session matches a live session"
  #   -> @integration "claudeInstances[].pane resolves when the heartbeat's tmux_session matches a live tmux session"
  #
  # AC 7: "state reports working/idle/input when fresh + recognized + live, no_claude otherwise"
  #   -> @integration "claudeInstances[].state reports working when heartbeat is fresh, says working, and pid is live"
  #   -> @integration "claudeInstances[].state reports idle when heartbeat says idle, is fresh, and pid is live"
  #   -> @integration "claudeInstances[].state reports input when heartbeat says input, is fresh, and pid is live"
  #   -> @integration "claudeInstances[].state falls back to no_claude when heartbeat is older than 30 seconds"
  #   -> @integration "claudeInstances[].state falls back to no_claude when the resolved pid is not live"
  #   -> @integration "claudeInstances[].state falls back to no_claude when heartbeat state is unrecognized"
  #
  # AC 8: "End-to-end repro is fixed: N live claudes -> N rows with process != null, pane != null, state != no_claude"
  #   -> @e2e "Host with N live claude processes reports N instances with non-null process, pane, and state != no_claude"
  #   -> @e2e "Original repro from issue 468 — 7 live claudes, 6 heartbeat-tracked instances"
  #
  # AC 9: "pidFromPaneID stub is left untouched"
  #   -> @unit "pidFromPaneID remains a no-op stub"
  #
  # AC 10: "Hook script change is out of scope; pid sourced from pane.CurrentPid; firstNonZero precedence still preferred when claudePid is present"
  #   -> @unit "Heartbeat parser still accepts both claudePid and claude_pid (no schema regression)"
  #   -> @integration "Fix works on heartbeats that lack claudePid entirely"
  #   -> @unit "hook script orchard-state.sh is unchanged by this PR"
  #
  # AC 11: "go test for the changed packages is green; new adapters have unit tests; resolver layer has integration coverage"
  #   -> @e2e "go test for the changed packages is green"
  #
  # AC 12: "No regressions in existing queries (tmuxSessions, tmuxPanes, host.processes, claudeInstances top-level)"
  #   -> @integration "Heartbeat with no matching tmux session still produces null process, null pane, no_claude"
  #   -> @integration "Query.tmuxSessions and Query.tmuxPanes return identical shape pre/post-fix"
  #   -> @integration "Query.host.processes returns identical shape pre/post-fix"
  #   -> @integration "claudeInstances top-level shape (length, ids) is unchanged for inputs with no possible join"
