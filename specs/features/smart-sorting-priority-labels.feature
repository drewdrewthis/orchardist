Feature: Smart sorting, priority indicators, and label badges
  As a developer managing multiple worktrees
  I want rows sorted by actionability, priorities visually distinct, and labels visible
  So that I can quickly find what needs my attention without reading every row

  Background:
    Given the existing DisplayGroup enum controls primary sort order
    And priority.rs stores prioritized worktree paths in priorities.json
    And issue_labels and pr.labels are available on WorktreeRow (from #237)
    And sort logic is extracted into a shared comparator used by derive_all_repos
      and OrchardState::all_worktrees to prevent drift

  # ===================================================================
  # Smart Sorting — secondary sort within groups
  # ===================================================================

  @unit
  Scenario: Within NeedsAttention group, rows with claude input sort first
    Given two worktrees both in NeedsAttention group
    And worktree A has a claude session in "input" state
    And worktree B has no claude session
    When derive_all_repos sorts the rows
    Then worktree A appears before worktree B

  @unit
  Scenario: Within same group, rows with recent activity sort before stale rows
    Given two worktrees both in Other group
    And worktree A has last_commit_pushed_at "2026-04-13T10:00:00Z"
    And worktree B has last_commit_pushed_at "2026-04-01T10:00:00Z"
    When derive_all_repos sorts the rows
    Then worktree A appears before worktree B

  @unit
  Scenario: Rows with None timestamps sort after rows with timestamps
    Given two worktrees both in Other group
    And worktree A has last_commit_pushed_at "2026-04-01T10:00:00Z"
    And worktree B has last_commit_pushed_at None
    When derive_all_repos sorts the rows
    Then worktree A appears before worktree B

  @unit
  Scenario: Two rows with None timestamps fall back to issue number tiebreaker
    Given two worktrees both in Other group
    And both have last_commit_pushed_at None
    And worktree A has issue_number 50
    And worktree B has issue_number 30
    When derive_all_repos sorts the rows
    Then worktree B appears before worktree A (lower issue number first)

  @unit
  Scenario: Within same group and same recency, issue number is tiebreaker
    Given two worktrees both in Other group
    And both have last_commit_pushed_at "2026-04-13T10:00:00Z"
    And worktree A has issue_number 50
    And worktree B has issue_number 30
    When derive_all_repos sorts the rows
    Then worktree B appears before worktree A (lower issue number first)

  @unit
  Scenario: Worktrees with no PR sort after worktrees with a PR within same group
    Given two worktrees both in Other group
    And worktree A has a linked PR
    And worktree B has no linked PR
    When derive_all_repos sorts the rows
    Then worktree A appears before worktree B

  @unit
  Scenario: DisplayGroup primary sort is unchanged
    Given worktree A in NeedsAttention group
    And worktree B in Other group with more recent activity
    When derive_all_repos sorts the rows
    Then worktree A still appears before worktree B (group trumps recency)

  @unit
  Scenario: Fuzzy filter overrides smart sort
    Given multiple worktrees with smart sort ordering
    When a fuzzy filter is active with non-empty text
    Then rows are sorted by descending fuzzy score, not by smart sort
    And main worktree rows remain pinned to the front

  # ===================================================================
  # Priority Indicators — visual distinction in TUI
  # ===================================================================

  @unit
  Scenario: Prioritized row shows star indicator replacing hash in issue column
    Given a worktree row with DisplayGroup::Prioritized and issue_number 42
    When the row is rendered in the TUI
    Then the issue cell shows "★42" (star replaces "#" prefix)

  @unit
  Scenario: Priority indicator uses themed priority color
    Given a prioritized worktree row
    When rendered in the TUI
    Then the star indicator uses theme.prioritized color (Yellow)

  @unit
  Scenario: Non-prioritized rows have no star indicator
    Given a worktree row with DisplayGroup::Other
    When the row is rendered in the TUI
    Then no star indicator appears in the issue cell

  @unit
  Scenario: Prioritized color updated from White to Yellow
    When the Theme is constructed with defaults
    Then theme.prioritized is Yellow

  # ===================================================================
  # Label Badges — inline in TUI rows
  # ===================================================================

  @unit
  Scenario: Issue labels render as inline badges after the title
    Given a worktree row with issue_labels ["bug", "enhancement"]
    When the row is rendered in the TUI
    Then the title cell contains badge text "[bug]" and "[enhancement]"

  @unit
  Scenario: Label badges use dimmed styling to not overwhelm the title
    Given a worktree row with issue_labels ["bug"]
    When the row is rendered in the TUI
    Then the badge "[bug]" uses dimmed/muted style

  @unit
  Scenario: Phase labels are excluded from badge rendering
    Given a worktree row with issue_labels ["bug", "in-progress", "planned"]
    And phase labels are those in the PHASE_PRIORITY constant from derive.rs
    When the row is rendered in the TUI
    Then only "[bug]" badge renders (phase labels are already shown via display_group)

  @unit
  Scenario: No badges render when issue has no non-phase labels
    Given a worktree row with issue_labels ["in-progress"]
    When the row is rendered in the TUI
    Then no label badges appear in the title cell

  @unit
  Scenario: Label badges truncate when title column is narrow
    Given a worktree row with issue_labels ["bug", "enhancement", "documentation", "help-wanted"]
    And the title column is only 40 characters wide
    When the row is rendered in the TUI
    Then labels are truncated to fit and remaining count shown as "+2"

  @unit
  Scenario: Labels suppressed entirely when title fills the column
    Given a worktree row with a long title that fills the available column width
    And issue_labels ["bug"]
    When the row is rendered in the TUI
    Then no label badges appear (title text takes priority over badges)

  # ===================================================================
  # JSON Output — last_activity_at exposed
  # ===================================================================

  @unit
  Scenario: JSON output includes last_activity_at field on worktrees
    Given a worktree with pr.last_commit_pushed_at "2026-04-13T10:00:00Z"
    When orchard --json is run
    Then the worktree object contains "last_activity_at": "2026-04-13T10:00:00Z"

  @unit
  Scenario: last_activity_at is null when no activity timestamps exist
    Given a worktree with no PR and no worktree_last_commit_at
    When orchard --json is run
    Then the worktree object contains "last_activity_at": null

  # ===================================================================
  # Integration — full pipeline
  # ===================================================================

  @integration
  Scenario: Smart sort order verified through orchard --json
    Given fixture caches with multiple worktrees at different activity levels
    When orchard --json is run
    Then worktrees within the same display_group are ordered by recency
    And the overall order respects DisplayGroup primary sort

  @integration
  Scenario: Priority indicator visible in TUI render
    Given a fixture with one prioritized worktree with issue_number 42
    When the TUI is rendered to a test backend
    Then the buffer contains "★42" on the prioritized row
    And the star is colored with the priority theme color

  @integration
  Scenario: Label badges visible in TUI render
    Given a fixture with a worktree whose issue has labels ["bug", "wontfix"]
    When the TUI is rendered to a test backend
    Then the buffer contains "[bug]" and "[wontfix]" on that row
