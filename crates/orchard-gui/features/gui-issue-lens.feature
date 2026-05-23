Feature: GUI issue lens — active PR/issue work grouped by GitHub issue
  As the orchard-gui LensSidebar in "issue" mode
  I need worktrees that have both an open PR and a linked issue to appear grouped by issue number
  So that the operator sees in-progress GitHub work organized by its issue.

  Operation consumed:
    IssueLens:
      claudeInstances [...SessionCard]
      repos[].worktrees [...WorktreeEnrichment + claudeInstances[...SessionCard]]

  Background:
    Given the daemon is running on 127.0.0.1:7777
    And at least one worktree has an open PR linked to a GitHub issue

  @integration
  Scenario: IssueLens includes only worktrees with OPEN or DRAFT PR and a linked issue
    When buildIssueSections runs
    Then worktrees with no pr field are excluded
    And worktrees with pr.state = "CLOSED" or "MERGED" are excluded
    And worktrees with pr.state = "OPEN" or "DRAFT" and a non-null issue are included
    And worktrees with no issue (no issue<N>/... branch convention) are excluded

  @integration
  Scenario: One section per unique issue number
    Given worktrees for issues #123 and #456 are both in scope
    When buildIssueSections runs
    Then the sidebar has two sections: one for #123, one for #456
    And the section label for issue #123 is "#123 · <issue title>" when title is non-null
    And when issue.title is null the label is "#123"

  @integration
  Scenario: IssueLens drops worktrees with no live ClaudeInstance
    Given worktree A has an open PR + issue but claudeInstances is empty
    When buildIssueSections runs
    Then no row appears for worktree A in the issue lens

  @integration
  Scenario: WorktreeEnrichment pr field required — gap acknowledged
    When the IssueLens query runs
    Then the worktree includes issue { number, state, title }
    # NOTE: IssueLens depends on pr.state for the OPEN/DRAFT filter.
    # WorktreeEnrichment.gql explicitly excludes pr to avoid ~12s REST calls.
    # The IssueLens query's worktree projection DOES NOT carry pr in WorktreeEnrichment.
    # This means buildIssueSections's pr.state filter can only operate when pr is populated
    # at query time. As of the current schema, this is a functional gap:
    # the issue lens must either include pr (with the REST latency cost)
    # or drop the OPEN/DRAFT filter. File a daemon issue before implementing step defs.

  @integration
  Scenario: IssueLens top-level claudeInstances — secondary enrichment
    When the IssueLens query runs
    Then the top-level claudeInstances list is also returned
    And it carries the same SessionCard fragment shape as other lenses
    And buildIssueSections uses the worktree-scoped claudeInstances, not the top-level list

  @integration
  Scenario: Empty issue lens state
    Given no worktrees have both an open PR and a linked issue with a live Claude session
    When issueTotal = 0
    Then the sidebar renders "No issues with open PRs in scope."
