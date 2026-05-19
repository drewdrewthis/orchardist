Feature: GUI worktree lens — all worktrees grouped by repo, anchored on tmuxPanes
  As the orchard-gui LensSidebar in "worktree" mode
  I need every registered worktree visible regardless of session state
  So that the operator can see their full topology and act on dormant worktrees.

  Operation consumed:
    WorktreeLens → workView.repos[].worktrees [...WorktreeEnrichment + tmuxPanes[...PaneCard]]

  Background:
    Given the daemon is running on 127.0.0.1:7777
    And at least one repo is configured

  @integration
  Scenario: Every worktree appears — including those with no tmux panes
    When the WorktreeLens query runs
    And buildWorktreeSections runs
    Then every worktree in workView appears as at least one row
    And worktrees with tmuxPanes = [] render as a single dormant row
    And worktrees with N panes render as N rows (one per pane)

  @integration
  Scenario: One sidebar section per repo
    Given repos ["langwatch/langwatch", "drewdrewthis/orchard"]
    When buildWorktreeSections runs
    Then the sidebar has two sections labelled by repo slug
    And each section contains rows for panes/worktrees within that repo only

  @integration
  Scenario: Pane with Claude session — full enriched row
    Given a pane with a claudeInstance
    When buildWorktreeSections processes it
    Then the row is keyed as "pane:<paneId>" for stable dedup
    And the row's title derives from conversation.agentName or customTitle or branch
    And the row's worktree is taken from claudeInstance.worktree (daemon-joined, not client cwd match)

  @integration
  Scenario: Pane with no Claude session — tmux-only row
    Given a pane whose claudeInstance is null
    When buildWorktreeSections processes it
    Then a tmux-only row appears for that pane
    And the row's lastActivityMs comes from pane.window.session.lastActivityAt
    And the row renders without a state pill (no ClaudeInstance to derive from)

  @integration
  Scenario: Dormant worktree — no panes but still visible
    Given a worktree with tmuxPanes = [] and a branch "feature/x"
    When buildWorktreeSections processes it
    Then a dormant row appears showing "feature/x" and any PR/issue chips
    And the row's lastActivityMs = 0, so it sinks to the bottom of the activity-sort

  @integration
  Scenario: Activity sort within a repo section — most-active panes float up
    Given repo section "langwatch/langwatch" has panes with lastActivityAt T1 < T2 < T3
    When buildWorktreeSections sorts within the section
    Then the row with T3 appears first, T2 second, T1 third
    And dormant rows (lastActivityMs = 0) appear last

  @integration
  Scenario: Dedup by item.id within a section
    Given a pane appears in two worktrees of the same repo (daemon edge case)
    When buildWorktreeSections deduplicates by item.id
    Then the pane appears exactly once in the section
    And no keyed-each crash occurs in the Svelte renderer

  @integration
  Scenario: Empty repo config — worktree lens shows no config message
    Given the orchard config has no repos registered
    When worktreeTotal = 0
    Then the sidebar renders "No repos in config — run `orchard config init`."
