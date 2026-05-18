Feature: GUI tmux lens — server → sessions → windows → panes view
  As the orchard-gui LensSidebar in "tmux" mode
  I need the full tmux server tree grouped by session
  So that the operator sees which Claude REPLs are alive and in which tmux session.

  Operation consumed:
    TmuxLens → tmuxServer { id, alive, sessions { id, name, attached, activeAttached, lastActivityAt, windows { id, index, name, active, panes [...PaneCard] } }, clients { tty, currentPane { paneId } } }

  Background:
    Given the daemon is running on 127.0.0.1:7777
    And a tmux server is running locally

  Scenario: TmuxLens returns server alive flag
    When the TmuxLens query runs
    Then tmuxServer.alive = true when the tmux daemon is reachable
    And tmuxServer.sessions is a non-empty list when at least one session exists

  Scenario: Active pane detection — clients[].currentPane drives "here" badge
    Given tmuxServer.clients[0].currentPane.paneId = "%26"
    When buildTmuxSnapshot runs
    Then activePaneIds contains "%26"
    And any sidebar row with pane.paneId = "%26" renders the "here" badge

  Scenario: Tmux lens groups rows by session — one sidebar section per session
    Given tmuxServer has sessions ["orchard", "langwatch"]
    When buildTmuxSections runs
    Then the sidebar has two sections: one labelled "orchard", one "langwatch"
    And each section contains rows for panes with a claudeInstance only
    And panes with no Claude REPL are dropped from the section's item list

  Scenario: PaneCard shape — required fields for the tmux lens
    When a pane is included in TmuxLens
    Then pane.paneId is the tmux pane ID (e.g. "%26")
    And pane.title is a non-empty string (tmux pane_title)
    And pane.currentCommand is the raw tmux pane_current_command value (may be a version string, not a basename)
    And pane.currentPid is an integer or null
    And pane.window.session.name matches the section label

  Scenario: PaneCard.claudeInstance — nil when pane runs zsh/vim/etc
    Given a pane whose currentCommand is "zsh"
    When the TmuxLens query returns
    Then pane.claudeInstance is null for that pane
    And the pane is excluded from the tmux lens sidebar (buildTmuxSections drops it)

  Scenario: PaneCard.claudeInstance — present for Claude panes
    Given a pane running the Claude REPL
    When TmuxLens returns
    Then pane.claudeInstance is a full SessionCard
    And pane.claudeInstance.worktree carries a WorktreeEnrichment (daemon-resolved by cwd)
    And pane.claudeInstance.conversation carries agentName and customTitle for the title hint

  Scenario: Tmux server unreachable — empty lens with informative message
    When tmuxServer.alive = false
    And tmuxStore.fetching = false
    Then the tmux lens sidebar renders "No tmux server reachable."
    And sections is an empty list (buildTmuxSections returns [])

  Scenario: PaneCard.process.command — reliable command basename via ps
    When the daemon builds the PaneCard
    Then process.command is the basename as ps reports it (e.g. "claude", "zsh")
    And process.command is more reliable than pane.currentCommand for intent detection
    # See PaneCard.gql comment: on macOS pane_current_command can be the version string

  Scenario: TmuxLens does NOT include pane content (screen capture)
    When the TmuxLens query runs
    Then no content, contentRange, or contentFull fields are included in the response
    And no tmux capture-pane shellouts are triggered per request
