Feature: TUI dashboard initial render
  As the orchard TUI
  I need to render the worktree+session+claude grid on first paint
  So that operators see live state in their terminal within one round-trip.

  Background:
    Given the daemon is running on 127.0.0.1:7777
    And the TUI is launched in its default list view

  # Query: workView { repos { slug path worktrees { path branch head bare host repo
  #          ahead behind pr { number state title statusCheckRollup reviewDecision
  #          mergeStateStatus mergeable draft labels }
  #          issue { number state title labels } } }
  #        tmuxSessions { id name attached activeAttached lastActivityAt
  #                       attachedClients windows currentWindow path }
  #        claudeInstances { id pane process state sessionUuid rcEnabled
  #                          lastActivityAt model inflightToolCount } }

  @integration
  Scenario: Initial dashboard fetch returns a complete WorkViewSnapshot
    When the TUI boots and fires its first workView query
    Then the response contains a "repos" array
    And each repo entry has "slug" and "path" fields
    And each repo entry has a "worktrees" array
    And each worktree carries branch, head, bare, host, repo, ahead, behind
    And each worktree carries a nullable "pr" object with number, state, title
    And each worktree carries a nullable "issue" object with number, state, title
    And the response contains a "tmuxSessions" array
    And each tmux session carries id, name, attached, activeAttached, lastActivityAt
    And each tmux session carries attachedClients, windows, currentWindow
    And each tmux session carries an optional "path" (working-directory for session→worktree matching)
    And the response contains a "claudeInstances" array
    And each claude instance carries id, pane, process, state, sessionUuid, rcEnabled
    And each claude instance carries optional lastActivityAt, model, inflightToolCount

  @integration
  Scenario: Round-trip latency is within interactive budget
    When the TUI fires the workView query against a local daemon
    Then the response arrives in under 50ms on the P95

  @integration
  Scenario: Cold-start fallback when daemon is unreachable
    Given the daemon is not reachable
    When the TUI boots
    Then it reads the persisted snapshot from ~/.cache/orchard/work_view_snapshot.json
    And it renders the stale snapshot without a blank screen
    And it shows a "daemon unreachable" indicator in the header
    And no GraphQL error is surfaced as a fatal crash

  @integration
  Scenario: Snapshot is persisted after every successful fetch
    When the TUI receives a successful workView response
    Then it atomically writes the snapshot to ~/.cache/orchard/work_view_snapshot.json
    And a subsequent cold-start with the daemon down reads that file back

  @integration
  Scenario: workView response with empty collections renders correctly
    Given the daemon returns workView with empty repos, tmuxSessions, and claudeInstances
    When the TUI renders the list
    Then no rows are displayed
    And no panic or "index out of bounds" error occurs

  @integration
  Scenario: PR optional fields absent render without crash
    Given a worktree's PR object omits statusCheckRollup, reviewDecision, mergeStateStatus, mergeable
    When the TUI renders the dashboard row
    Then the row renders successfully
    And missing CI/review badges are absent rather than showing a placeholder error
