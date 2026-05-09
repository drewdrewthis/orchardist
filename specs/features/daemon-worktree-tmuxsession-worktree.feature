Feature: daemon Worktree.tmuxPanes / Worktree.tmuxSession — server-side cwd join (#511)
  As a client (Orchard GUI, Rust TUI, scripts) querying the daemon for "which tmux session is attached to this worktree"
  I want the Worktree GraphQL type to expose tmuxPanes and tmuxSession derived from a server-side join of pane.process.cwd against worktree.path
  So that no client-side naming heuristics, no commandIn allowlists, and no multi-query JS joins are required to attach a terminal to a worktree

  # Server-side join: for each worktree, enumerate every tmux pane on its host,
  # resolve pane.process.cwd via the ps provider, and keep panes whose cwd
  # equals worktree.path or sits under worktree.path + "/". tmuxSession is
  # sugar for "the most-recently-active session among those panes".
  #
  # Blocked by #463 (tmuxPane.process wiring). Independent of #468.

  Background:
    Given the daemon serves a GraphQL schema at 127.0.0.1:7777
    And the existing Worktree type already exposes id, path, branch, head, bare, processes
    And the existing TmuxPane type exposes paneId, window.session, and a process accessor backed by the ps provider
    And the existing TmuxSession type exposes name, host, and lastActivityAt
    And the ps provider can resolve a foreground process cwd from a pid (where the platform supports it)

  # =======================================================================
  # AC1 — tmuxPanes returns the matching pane list for every worktree
  # =======================================================================

  @integration @issue-511
  Scenario: tmuxPanes returns panes whose cwd exactly equals the worktree path
    Given a worktree at path "/Users/me/repo/.worktrees/feat-x"
    And a tmux pane "%1" whose foreground process cwd is "/Users/me/repo/.worktrees/feat-x"
    When the GraphQL query "{ projects { worktrees { id path tmuxPanes { paneId } } } }" is executed
    Then tmuxPanes for that worktree contains the pane "%1"

  @integration @issue-511
  Scenario: tmuxPanes returns panes whose cwd sits under the worktree path
    Given a worktree at path "/Users/me/repo/.worktrees/feat-x"
    And a tmux pane "%2" whose foreground process cwd is "/Users/me/repo/.worktrees/feat-x/internal/server"
    When tmuxPanes is resolved for that worktree
    Then tmuxPanes contains the pane "%2"

  @unit @issue-511
  Scenario: tmuxPanes excludes panes whose cwd is a sibling of the worktree path (no false-prefix match)
    Given a worktree at path "/Users/me/repo/.worktrees/feat-x"
    And a tmux pane whose foreground process cwd is "/Users/me/repo/.worktrees/feat-xtra"
    When tmuxPanes is resolved for that worktree
    Then the pane is NOT included in tmuxPanes
    And the cwd-match rule is "exact OR prefix `path + '/'`"

  @unit @issue-511
  Scenario: tmuxPanes returns panes ordered deterministically by paneId ascending
    Given three matching panes with paneIds "%5", "%2", "%9"
    When tmuxPanes is resolved
    Then the returned slice is ordered "%2", "%5", "%9"

  @integration @issue-511
  Scenario: tmuxPanes is non-nullable in the schema
    Given the generated GraphQL schema
    When the Worktree.tmuxPanes field is inspected
    Then its type is "[TmuxPane!]!" (non-null list of non-null TmuxPane)

  # =======================================================================
  # AC2 — tmuxSession returns the most-recently-active matching session
  # =======================================================================

  @integration @issue-511
  Scenario: tmuxSession returns the unique session of the single matching pane
    Given a worktree at path "/Users/me/repo/.worktrees/feat-x"
    And exactly one tmux pane matches that worktree, attached to session "feat-x" (lastActivityAt 2026-05-09T10:00:00Z)
    When tmuxSession is resolved
    Then tmuxSession.name equals "feat-x"
    And tmuxSession.lastActivityAt equals "2026-05-09T10:00:00Z"

  @integration @issue-511
  Scenario: tmuxSession field is nullable in the schema
    Given the generated GraphQL schema
    When the Worktree.tmuxSession field is inspected
    Then its type is "TmuxSession" (nullable)

  # =======================================================================
  # AC3 — Two sessions both matching: most-recently-active wins, deterministic tie-break
  # =======================================================================

  @unit @issue-511
  Scenario: tmuxSession picks the session with the higher lastActivityAt when two sessions match
    Given a worktree at path "/Users/me/repo/.worktrees/feat-x"
    And session "alpha" has lastActivityAt 2026-05-09T09:00:00Z and a pane matching the worktree
    And session "beta" has lastActivityAt 2026-05-09T11:00:00Z and a pane matching the worktree
    When tmuxSession is resolved
    Then tmuxSession.name equals "beta"

  @unit @issue-511
  Scenario: tmuxSession ties on lastActivityAt are broken by session name lex order ascending
    Given a worktree at path "/Users/me/repo/.worktrees/feat-x"
    And session "zebra" has lastActivityAt 2026-05-09T11:00:00Z and a pane matching the worktree
    And session "alpha" has lastActivityAt 2026-05-09T11:00:00Z and a pane matching the worktree
    When tmuxSession is resolved
    Then tmuxSession.name equals "alpha"

  # =======================================================================
  # AC4 — No matches: empty list and null session
  # =======================================================================

  @unit @issue-511
  Scenario: Worktree with no matching panes returns empty tmuxPanes and null tmuxSession
    Given a worktree at path "/Users/me/repo/.worktrees/lonely"
    And no tmux pane has a foreground process cwd at or under that path
    When tmuxPanes and tmuxSession are resolved
    Then tmuxPanes equals an empty list "[]"
    And tmuxSession equals null

  # =======================================================================
  # AC5 — Panes whose cwd is null (e.g. Linux without /proc/<pid>/cwd) are silently skipped
  # =======================================================================

  @unit @issue-511
  Scenario: A pane whose process.cwd is null is not treated as matching everything
    Given a worktree at path "/Users/me/repo/.worktrees/feat-x"
    And a tmux pane whose foreground process exists but whose cwd is null
    When tmuxPanes is resolved for that worktree
    Then the pane is NOT included in tmuxPanes
    And the pane is silently skipped (no error surfaced)

  @unit @issue-511
  Scenario: A pane whose process resolution fails entirely is silently skipped
    Given a worktree at path "/Users/me/repo/.worktrees/feat-x"
    And a tmux pane whose foreground pid is unresolvable by the ps provider
    When tmuxPanes is resolved for that worktree
    Then the pane is NOT included in tmuxPanes

  # =======================================================================
  # AC6 — Federation attribution via pane.window.session.host
  # =======================================================================

  @integration @issue-511
  Scenario: Pane attribution uses pane.window.session.host, not the local daemon's host id
    Given a worktree on host "B" at path "/home/me/repo"
    And a tmux pane whose pane.window.session.host equals "B" and whose foreground cwd is "/home/me/repo"
    And the local daemon's host id is "A"
    When tmuxPanes is resolved for the worktree on host "B"
    Then the matching pane is included
    And attribution is read from pane.window.session.host (value "B"), not synthesised from the local daemon's host id

  @integration @issue-511
  Scenario: Panes from a different host do not match a local-host worktree
    Given a worktree on host "A" at path "/Users/me/repo"
    And a tmux pane whose pane.window.session.host equals "B" with cwd "/Users/me/repo"
    When tmuxPanes is resolved for the worktree on host "A"
    Then the cross-host pane is NOT included in tmuxPanes for the host "A" worktree

  # =======================================================================
  # AC7 — Integration test: 4 worktrees × 6 panes resolves correctly
  # =======================================================================

  @integration @issue-511
  Scenario: Integration test against fake Tmux + fake PS providers — 4 worktrees, 6 panes resolve correctly
    Given fake Tmux and fake PS providers wired into the resolver harness
    And worktree "wt-A" at "/repo/.worktrees/A"
    And worktree "wt-B" at "/repo/.worktrees/B"
    And worktree "wt-C" at "/repo/.worktrees/C"
    And worktree "wt-D" at "/repo/.worktrees/D"
    And pane "p1" with cwd "/repo/.worktrees/A"
    And pane "p2" with cwd "/repo/.worktrees/A/sub"
    And pane "p3" with cwd "/repo/.worktrees/B"
    And pane "p4" with cwd "/elsewhere"
    And pane "p5" with cwd "/repo"
    And pane "p6" with cwd null
    When the GraphQL dashboard query selects { worktrees { id tmuxPanes { paneId } tmuxSession { name } } }
    Then "wt-A".tmuxPanes equals ["p1", "p2"] (sorted by paneId)
    And "wt-B".tmuxPanes equals ["p3"]
    And "wt-C".tmuxPanes equals []
    And "wt-D".tmuxPanes equals []
    And "wt-A".tmuxSession equals the session of the most-recently-active of p1/p2
    And "wt-C".tmuxSession equals null
    And the test fails against a placeholder resolver that returns []/null and passes against the real one

  # =======================================================================
  # AC8 — Schema doc strings name this issue and the cwd-match semantics
  # =======================================================================

  @unit @issue-511
  Scenario: tmuxPanes schema doc string documents cwd-match semantics and references this issue
    Given the Worktree.tmuxPanes field in the generated schema
    When its description is inspected
    Then the description states the match rule "exact OR `path + '/'` prefix"
    And the description references issue #511

  @unit @issue-511
  Scenario: tmuxSession schema doc string documents the most-recently-active selection and references this issue
    Given the Worktree.tmuxSession field in the generated schema
    When its description is inspected
    Then the description states the field is the most-recently-active session among tmuxPanes
    And the description references issue #511

  # =======================================================================
  # E2E — full dashboard query against the live daemon
  # =======================================================================

  @e2e @issue-511
  Scenario: Live daemon returns tmuxPanes and tmuxSession for a worktree with a real shell sitting in it
    Given the daemon is running with at least one worktree at path "$WT"
    And a real tmux session has a pane whose foreground shell cwd is "$WT"
    When a GraphQL query selects { projects { worktrees { id path tmuxPanes { paneId window { session { name } } } tmuxSession { name lastActivityAt } } } }
    Then the worktree at "$WT" has a non-empty tmuxPanes list
    And tmuxSession is non-null and matches the live session's name
    And a worktree with no shell sitting in it returns "tmuxPanes: []" and "tmuxSession: null"

  # --- AC Coverage Map ---
  # AC1: "tmuxPanes returns the matching pane list for every worktree, derived from cwd-equality / prefix match against the worktree path"
  #   -> @integration "tmuxPanes returns panes whose cwd exactly equals the worktree path"
  #   -> @integration "tmuxPanes returns panes whose cwd sits under the worktree path"
  #   -> @unit "tmuxPanes excludes panes whose cwd is a sibling of the worktree path (no false-prefix match)"
  #   -> @unit "tmuxPanes returns panes ordered deterministically by paneId ascending"
  #   -> @integration "tmuxPanes is non-nullable in the schema"
  #   -> @e2e  "Live daemon returns tmuxPanes and tmuxSession for a worktree with a real shell sitting in it"
  #
  # AC2: "tmuxSession returns the most-recently-active matching session, or null when no panes match"
  #   -> @integration "tmuxSession returns the unique session of the single matching pane"
  #   -> @integration "tmuxSession field is nullable in the schema"
  #   -> @e2e  "Live daemon returns tmuxPanes and tmuxSession for a worktree with a real shell sitting in it"
  #
  # AC3: "When two panes from different sessions both sit under a worktree path, tmuxSession returns the session with the higher lastActivityAt; tie-broken deterministically by session name lex order"
  #   -> @unit "tmuxSession picks the session with the higher lastActivityAt when two sessions match"
  #   -> @unit "tmuxSession ties on lastActivityAt are broken by session name lex order ascending"
  #
  # AC4: "A worktree with no tmux activity returns tmuxPanes: [] and tmuxSession: null"
  #   -> @unit "Worktree with no matching panes returns empty tmuxPanes and null tmuxSession"
  #   -> @e2e  "Live daemon returns tmuxPanes and tmuxSession for a worktree with a real shell sitting in it"
  #
  # AC5: "A pane whose process.cwd is null is silently skipped, NOT treated as matching everything"
  #   -> @unit "A pane whose process.cwd is null is not treated as matching everything"
  #   -> @unit "A pane whose process resolution fails entirely is silently skipped"
  #
  # AC6: "Federated case: a worktree on host B with a pane on host B attaches via this field; attribution uses pane.window.session.host, not the local daemon's host id"
  #   -> @integration "Pane attribution uses pane.window.session.host, not the local daemon's host id"
  #   -> @integration "Panes from a different host do not match a local-host worktree"
  #
  # AC7: "Integration test against fake Tmux + fake PS providers: 4 worktrees, 6 panes — all 4 worktrees resolve correctly; test fails on placeholder, passes on real resolver"
  #   -> @integration "Integration test against fake Tmux + fake PS providers — 4 worktrees, 6 panes resolve correctly"
  #
  # AC8: "Schema doc on both fields names this issue and documents the cwd-match semantics (exact OR `path + '/'` prefix)"
  #   -> @unit "tmuxPanes schema doc string documents cwd-match semantics and references this issue"
  #   -> @unit "tmuxSession schema doc string documents the most-recently-active selection and references this issue"
