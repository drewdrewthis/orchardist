Feature: GUI attention lens — triage view anchored on worktrees
  As the orchard-gui LensSidebar in "attention" mode
  I need every worktree tiered into blocked/waiting/active/quiet
  So that the operator immediately sees what needs human intervention.

  Operation consumed:
    AttentionLens → workView.repos[].worktrees[...WorktreeEnrichment + claudeInstances[...SessionCard]]

  Background:
    Given the daemon is running on 127.0.0.1:7777
    And at least one repo with at least one worktree is configured

  @integration
  Scenario: Every worktree appears in exactly one tier
    When the AttentionLens query runs
    Then every worktree in workView appears as at least one sidebar row
    And a worktree with zero claudeInstances appears as a single "dormant" row
    And a worktree with N live claudeInstances appears as N separate rows
    And no worktree appears in more than one tier section

  @integration
  Scenario: Blocked tier — CI failure promotes worktree to blocked
    Given a worktree whose pr.statusCheckRollup = "FAILURE"
    When buildAttentionSections runs
    Then that worktree's row appears in the "Blocked" tier
    # Note: pr fields are NOT in WorktreeEnrichment at query time.
    # The attention lens uses WorktreeEnrichment which excludes pr.
    # Blocked-tier detection based on pr signals requires the WorktreePR fragment
    # to be fetched separately; this is a known gap (see gaps section in PR body).

  @integration
  Scenario: Waiting tier — live session idle for more than 5 minutes
    Given a ClaudeInstance with lastActivityAt = 10 minutes ago
    And the instance is not in any PR-blocked state
    When buildAttentionSections runs at now
    Then that row appears in the "Waiting" tier with hint "idle 10m"

  @integration
  Scenario: Active tier — live session with recent activity
    Given a ClaudeInstance with lastActivityAt = 30 seconds ago
    And the instance is not blocked or waiting
    When buildAttentionSections runs
    Then that row appears in the "Active" tier

  @integration
  Scenario: Quiet tier — dormant worktree with no live session and no PR signal
    Given a worktree whose claudeInstances is an empty list
    And the worktree's pr is null or not-blocked
    When buildAttentionSections runs
    Then a dormant row appears in the "Quiet" tier

  @integration
  Scenario: Dedup — daemon attaches one ClaudeInstance to multiple repos
    Given sessionUuid "abc" appears on worktrees from both repo-A and repo-B (shared parent dir)
    When buildAttentionSections deduplicates by session.id
    Then sessionUuid "abc" appears exactly once in the sidebar
    And the first-seen occurrence is kept

  @integration
  Scenario: Activity sort within each tier
    Given the "Active" tier has two rows with lastActivityAt T1 < T2
    When the section is rendered
    Then the row with lastActivityAt T2 appears first (most-recent activity floats up)

  @integration
  Scenario: Empty sidebar state — no Claude sessions
    Given all worktrees have zero claudeInstances
    And buildAttentionSections returns sections with all empty items
    When attentionTotal = 0
    Then the sidebar renders "No Claude sessions reported by the daemon."
    And not a blank white box

  @integration
  Scenario: Attention lens error state — daemon GraphQL error
    Given attentionStore.errors is non-empty
    When attentionTotal = 0
    Then the sidebar renders "Daemon couldn't fetch this lens. Try another lens or check the daemon logs."
