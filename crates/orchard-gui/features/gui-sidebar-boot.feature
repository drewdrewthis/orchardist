Feature: GUI sidebar boot — all five lens prefetch
  As the orchard-gui LensSidebar
  I need all five lens stores to prefetch in parallel on mount
  So that lens switching is instant (pure cache render, no spinner interstitial).

  Operations consumed:
    AttentionLens  — workView.repos[].worktrees[].claudeInstances[...SessionCard] + WorktreeEnrichment
    RecentLens     — conversations + claudeInstances[...SessionCard]
    TmuxLens       — tmuxServer.sessions[].windows[].panes[...PaneCard]
    IssueLens      — claudeInstances[...SessionCard] + workView.repos[].worktrees[...WorktreeEnrichment + claudeInstances]
    WorktreeLens   — workView.repos[].worktrees[...WorktreeEnrichment + tmuxPanes[...PaneCard]]

  Background:
    Given the daemon is running on 127.0.0.1:7777
    And at least one repo is registered in the orchard config
    And the Houdini cache has been hydrated from localStorage (or is cold)

  @integration
  Scenario: Parallel lens prefetch completes without error
    When LensSidebar mounts and fires all five stores with .fetch()
    Then all five Houdini queries complete without GraphQL errors
    And each store's fetching flag transitions false before the first render tick

  @integration
  Scenario: Attention lens response shape
    When the AttentionLens query runs
    Then the response root contains a workView field
    And workView.repos is a list where each repo has id and slug
    And each repo has a worktrees list
    And each worktree includes the WorktreeEnrichment fields: id, path, branch, host, repo, issue{number,state,title}
    And each worktree includes a claudeInstances list of SessionCard nodes
    And a SessionCard includes: id, sessionUuid, state, inflightToolCount, startedAt, lastActivityAt, rcEnabled
    And a SessionCard includes a nullable account field with email
    And a SessionCard includes a nullable pane field with: paneId, title, currentCommand, window{id,index,name,active,session{id,name,attached,activeAttached}}
    And a SessionCard includes a nullable process field with: pid, cwd
    And a SessionCard includes a nullable worktree field spreading WorktreeEnrichment
    And a SessionCard includes a nullable conversation field with: sessionUuid, lastSeenAt, agentName, customTitle

  @integration
  Scenario: Attention lens does NOT include pr in WorktreeEnrichment
    When the AttentionLens query runs
    Then the worktree nodes in workView do NOT carry a pr field
    # PR data is deferred to the panel's WorktreePR fetch to avoid ~12s REST calls per repo

  @integration
  Scenario: RecentLens response shape
    When the RecentLens query runs
    Then the response contains a top-level conversations list
    And each conversation includes: id, sessionUuid, agentName, customTitle, cwd, firstSeenAt, lastSeenAt, messageCount, open, recap
    And the response contains a top-level claudeInstances list spreading SessionCard
    And conversations list is non-empty when any JSONL exists under the configured repos root

  @integration
  Scenario: TmuxLens response shape
    When the TmuxLens query runs
    Then the response contains a tmuxServer field
    And tmuxServer has: id, alive, sessions, clients
    And each session has: id, name, attached, activeAttached, lastActivityAt, windows
    And each window has: id, index, name, active, panes
    And each pane spreads PaneCard: paneId, title, currentCommand, currentPid, window{...session}, claudeInstance{...SessionCard}, process{pid,cwd,command,worktree{...WorktreeEnrichment}}
    And tmuxServer.clients has items with: tty and currentPane{paneId}
    And when tmux is unreachable, tmuxServer.alive is false and tmuxServer.sessions is an empty list

  @integration
  Scenario: IssueLens response shape
    When the IssueLens query runs
    Then the response contains a top-level claudeInstances list spreading SessionCard
    And the response contains a workView with repos and their worktrees spreading WorktreeEnrichment
    And each worktree in IssueLens carries a claudeInstances list spreading SessionCard

  @integration
  Scenario: WorktreeLens response shape
    When the WorktreeLens query runs
    Then the response contains a workView with repos and their worktrees
    And each worktree spreads WorktreeEnrichment
    And each worktree carries a tmuxPanes list spreading PaneCard

  @integration
  Scenario: Houdini cache snapshot survives round-trip
    Given the Houdini cache was persisted to localStorage key "orchard:houdini:cache:v3"
    When the layout hydrates the cache before any store fetches
    Then LensSidebar renders from cache without a "Loading…" flash
    And each store's CacheAndNetwork policy still revalidates against the daemon in the background

  @integration
  Scenario: Sidebar fetch error surfaces a toast — not a silent empty list
    Given the attention store fetch returns a GraphQL error
    When the error does not contain known-noise strings ("use GetPull", "EnrichPullRequest", "is a pull request")
    Then a toast error is shown to the user with a friendly message
    And the raw daemon error string is logged to the browser console only
    And when the error contains "rate limit", the toast reads "GitHub rate limit reached — PR data will catch up shortly."
    And when the error contains "EnrichPullRequest", the toast reads "Couldn't refresh PR status — showing the last known state."

  @integration
  Scenario: Lens switch is a pure render — no new network request
    Given all five stores have fetched successfully on mount
    When the user switches the active lens from "attention" to "tmux"
    Then the sidebar renders immediately from the Houdini cache
    And no new HTTP request is fired to the daemon for the tmux lens data
