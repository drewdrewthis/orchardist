Feature: Tags column with draft badge
  As an orchard user managing multiple PRs
  I want to see contextual tags like "draft" on worktree rows
  So I can quickly identify PR status without leaving the TUI

  # Current state:
  #   - `CachedPr` has `state`, `review_decision`, `checks_state`, `has_conflicts`, `unresolved_threads`
  #   - `isDraft` is NOT in the GraphQL query or any PR struct
  #   - `orchard --json` PR objects have no draft indicator
  #   - TUI columns: BAR | # | CLAUDE | ISSUE | TITLE | [BRANCH] | [HOST] | STATUS
  #   - No tags/badges column exists
  #
  # What changes:
  #   1. Add `isDraft` to the GitHub GraphQL PR query (field already available in API)
  #   2. Add `is_draft: bool` to CachedPr, PrInfo, PrState, and JsonPr structs
  #   3. Thread `is_draft` through parse → cache → derive → state → JSON conversion chain
  #   4. Add TAGS column to TUI between [HOST]/[BRANCH] and STATUS
  #   5. Render "draft" badge in TAGS column when pr.is_draft == true
  #   6. TAGS column is conditionally sized — zero-width when no rows have tags
  #
  # Files affected:
  #   - `src/cache.rs` — add `is_draft: bool` to `CachedPr`
  #   - `src/cache_sources.rs` — add `isDraft` to GraphQL query, extract in `parse_prs_graphql`
  #   - `src/derive.rs` — add `is_draft: bool` to `PrInfo`, propagate in `pr_info_from`
  #   - `src/orchard_state.rs` — add `is_draft: bool` to `PrState`, propagate in `From<&PrInfo>`
  #   - `src/json_output.rs` — add `is_draft: bool` to `JsonPr`, propagate in `From<&PrState>`
  #   - `src/tui/list.rs` — TAGS column width/constraint, header cell, row cell rendering
  #
  # Non-goals:
  #   - No new API calls — `isDraft` piggybacks on the existing GraphQL PR query
  #   - No theme/color customization for tags (use existing styles; future work)
  #   - Future tag candidates (conflicts, stale, behind) are out of scope

  Background:
    Given the TUI is running with at least one repo configured
    And there are worktree rows with PRs

  # ===================================================================
  # 1. Data layer: isDraft in GraphQL and PR structs
  # ===================================================================

  @unit
  Scenario: GraphQL query includes isDraft field
    When the PR GraphQL query is built
    Then the query includes "isDraft" in the PR node fields

  @unit
  Scenario: CachedPr stores is_draft from GraphQL response
    Given a GraphQL PR node with isDraft: true
    When the node is parsed into a CachedPr
    Then CachedPr.is_draft is true

  @unit
  Scenario: CachedPr defaults is_draft to false for legacy cache entries
    Given a cached PR JSON without an is_draft field
    When the JSON is deserialized into a CachedPr
    Then CachedPr.is_draft is false

  @unit
  Scenario: is_draft propagates through the conversion chain
    Given a CachedPr with is_draft: true
    When it is converted to PrInfo via pr_info_from
    And PrInfo is converted to PrState
    And PrState is converted to JsonPr
    Then PrInfo.is_draft is true
    And PrState.is_draft is true
    And JsonPr.is_draft is true

  # ===================================================================
  # 2. JSON output: isDraft in orchard --json
  # ===================================================================

  @unit
  Scenario: orchard --json includes is_draft in PR object
    Given a worktree with a draft PR
    When orchard --json output is generated
    Then the pr object contains "is_draft": true

  @unit
  Scenario: orchard --json shows is_draft false for non-draft PRs
    Given a worktree with a non-draft PR
    When orchard --json output is generated
    Then the pr object contains "is_draft": false

  # ===================================================================
  # 3. TUI: Tags column rendering
  # ===================================================================

  @unit
  Scenario: TAGS column appears in header when any row has tags
    Given at least one worktree row has a draft PR
    When the task list header renders
    Then the column order includes TAGS between the last optional column and STATUS

  @unit
  Scenario: TAGS column is hidden when no rows have tags
    Given no worktree rows have draft PRs
    When the task list header renders
    Then the TAGS column is not present
    And STATUS remains the last column

  @unit
  Scenario: Draft badge appears for draft PRs
    Given a worktree row with a draft PR (is_draft: true)
    When the row renders
    Then the TAGS cell contains "draft"

  @unit
  Scenario: TAGS cell is empty for non-draft PRs
    Given a worktree row with a non-draft PR (is_draft: false)
    When the row renders
    Then the TAGS cell is empty

  @unit
  Scenario: TAGS cell is empty for rows without PRs
    Given a worktree row with no PR
    When the row renders
    Then the TAGS cell is empty

  @unit
  Scenario: Standalone session rows have empty TAGS cell
    Given a standalone tmux session row
    And the TAGS column is visible
    When the row renders
    Then the TAGS cell is empty

  @unit
  Scenario: TAGS column width is fixed at 7 characters
    Given the TAGS column is visible
    When column widths are calculated
    Then the TAGS column constraint is Length(7) to fit " draft " with padding
    And the TITLE column absorbs the remaining flexible space

  @unit
  Scenario: TAGS column visible with BRANCH and HOST both hidden
    Given a single-host setup (no remote hosts)
    And show_branch is false
    And at least one worktree row has a draft PR
    When the task list renders
    Then the column order is BAR, #, CLAUDE, ISSUE, TITLE, TAGS, STATUS
    And the fixed arithmetic accounts for tags_extra but not branch_extra or host_extra

  @unit
  Scenario: TAGS column appears dynamically when draft PR arrives on refresh
    Given no worktree rows have draft PRs
    And the TAGS column is not visible
    When state refreshes and a PR becomes draft
    Then the TAGS column becomes visible on the next render

  @unit
  Scenario: TAGS column disappears when last draft PR is un-drafted
    Given one worktree row has a draft PR
    And the TAGS column is visible
    When state refreshes and the PR is marked ready for review
    Then the TAGS column is no longer visible

  @unit
  Scenario: Tag presence is computed during state derivation not render
    When build_task_table_rows computes worktree rows
    Then a has_tags boolean is derived from the row set
    And the render function uses the precomputed has_tags flag
