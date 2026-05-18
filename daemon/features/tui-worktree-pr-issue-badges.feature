Feature: TUI worktree PR and issue badge rendering
  As the orchard TUI
  I need each worktree row to carry pre-joined PR and issue data from the daemon
  So that I can render badges (CI, review, conflicts, draft, labels) without a client-side GitHub call.

  Background:
    Given the daemon is running and returns a workView snapshot
    And the snapshot contains repos with worktrees that have PR and issue joins

  # All PR/issue data comes from workView.repos[].worktrees[].pr and .issue.
  # The daemon performs the join server-side; the TUI only reads these fields.

  Scenario: PR CI status badge rendered from statusCheckRollup
    Given a worktree's pr.statusCheckRollup is "SUCCESS"
    When the TUI adapter converts the PR
    Then the worktree row's ci_code_state is "passing"
    And the dashboard renders a green CI badge

    Given a worktree's pr.statusCheckRollup is "FAILURE" or "ERROR"
    When the TUI adapter converts the PR
    Then ci_code_state is "failing"
    And the dashboard renders a red CI badge

    Given a worktree's pr.statusCheckRollup is "PENDING" or any unrecognised value
    Then ci_code_state is "pending"

  Scenario: PR review decision badge rendered from reviewDecision
    Given a worktree's pr.reviewDecision is "APPROVED"
    When the TUI adapter converts the PR
    Then review_decision is "approved" (normalised to lowercase)
    And the dashboard renders an "approved" review badge

    Given pr.reviewDecision is "CHANGES_REQUESTED"
    Then review_decision is "changes_requested"

    Given pr.reviewDecision is "REVIEW_REQUIRED"
    Then review_decision is "review_required"

  Scenario: Conflict detection uses mergeable field only
    Given a worktree's pr.mergeable is "CONFLICTING"
    When the TUI adapter converts the PR
    Then has_conflicts is true regardless of mergeStateStatus

    Given pr.mergeable is "MERGEABLE" but mergeStateStatus is "BLOCKED"
    Then has_conflicts is false
    And the BLOCKED state does not incorrectly signal merge conflicts

    Given pr.mergeable is absent (null)
    Then has_conflicts is false

  Scenario: Draft PR flag is carried through
    Given a worktree's pr.draft is true
    When the TUI renders the dashboard row
    Then the row shows a draft indicator

  Scenario: PR label list is forwarded
    Given a worktree's pr.labels is ["phase-1", "enhancement"]
    When the TUI adapter converts the PR
    Then the row's label list is ["phase-1", "enhancement"]

  Scenario: Issue linked to a worktree via daemon join
    Given a worktree's issue is { number: 429, state: "OPEN", title: "...", labels: ["bug"] }
    When the TUI adapter converts the worktree
    Then the row's issue_number is 429 and issue_state is "open" (normalised lowercase)
    And issue labels are forwarded to the row

  Scenario: Worktree with no PR and no issue renders cleanly
    Given a worktree's pr and issue are both null
    When the TUI renders the dashboard row
    Then no CI, review, or issue badge is shown
    And no crash or unwrap error occurs

  Scenario: Worktree ahead/behind counts from daemon
    Given a worktree's ahead is 2 and behind is 1
    When the TUI adapter converts the worktree
    Then worktree_ahead is Some(2) and worktree_behind is Some(1)
    And the row renders the ahead/behind indicator

    Given a worktree has no upstream configured (ahead is null)
    Then worktree_ahead is None and no indicator is rendered
