Feature: Workspace tab bar and colored row indicators
  As an orchard user managing worktrees across multiple repositories
  I want a colored tab bar and per-repo row indicators in the TUI
  So I can instantly see which repo each row belongs to without reading text labels

  # Current state:
  #   - `render_repo_tabs` already renders a tab bar with `╭`/`╮` brackets
  #   - `◄►:repos` hint with `[◄ slug ►]` active-repo indicator is in the hints bar
  #   - Left/Right arrow keys cycle `active_repo_index` via PrevRepo/NextRepo messages
  #   - Rows carry `repo_slug: String` and can be matched to `global_config.repos` index
  #   - Tab bar is already rendered between header and table in `render_task_list`
  #
  # What changes:
  #   1. Add `REPO_COLORS` palette (6 colors) and `repo_color(index)` function in `src/tui/theme.rs`
  #   2. Tab bar styling: replace `╭`/`╮` brackets with rounded pill `(` `)` or equivalent,
  #      color each tab using `repo_color(index)` from the new palette
  #   3. Add a thin colored vertical bar `▎` as the first column of each worktree row
  #      - Update `render_task_list` fixed-width arithmetic to account for the new column
  #      - Update `group_header_row` to prepend an empty bar cell so header text stays aligned
  #      - Bar cell must carry explicit `Cell::style()` to survive `Row::style()` override on selection
  #   4. Remove the `◄►:repos` hint and `[◄ slug ►]` indicator from the hints bar
  #
  # Files affected:
  #   - `src/tui/list.rs` — tab bar styling, row rendering, hints bar
  #   - `src/tui/theme.rs` — add `REPO_COLORS` constant and `repo_color()` function
  #
  # Non-goals:
  #   - No changes to data model or derive logic
  #   - No new keybindings (Left/Right already work)
  #   - No mouse click on tabs (future work)
  #   - No user-configurable repo colors (uses fixed REPO_COLORS palette)

  Background:
    Given the TUI is running with multiple repos configured in global config
    And there are worktree rows from at least two different repos

  # ===================================================================
  # Tab bar styling
  # ===================================================================

  @unit
  Scenario: Tab bar shows ALL tab plus one tab per configured repo
    Given global config has repos "owner/alpha" and "owner/beta"
    When the tab bar renders
    Then it shows tabs labeled "ALL", "ALPHA", "BETA" in that order
    And tab labels use the repo name portion of the slug, uppercased

  @unit
  Scenario: Active tab is highlighted with rounded pill styling
    Given the active repo index is 1 (first repo selected)
    When the tab bar renders
    Then the active tab uses bold styling with its repo color
    And the active tab text is wrapped in rounded pill decorators
    # Constraint: replace the current `╭`/`╮` bracket style with rounded pill

  @unit
  Scenario: Inactive tabs show in their repo color, dimmed
    Given the active repo index is 0 (ALL selected)
    When the tab bar renders
    Then each inactive repo tab is styled with its repo color from REPO_COLORS
    And each inactive repo tab has DIM modifier applied

  @unit
  Scenario: ALL tab uses accent color when active
    Given the active repo index is 0
    When the tab bar renders
    Then the ALL tab uses theme.accent color with bold modifier and pill styling

  @unit
  Scenario: ALL tab is dimmed when inactive
    Given the active repo index is 1
    When the tab bar renders
    Then the ALL tab uses theme.dimmed color with DIM modifier
    And no pill decorators are shown on the ALL tab

  @unit
  Scenario: Tab colors match REPO_COLORS palette by config order
    Given global config has 3 repos
    When the tab bar renders
    Then the first repo tab uses REPO_COLORS[0] (Cyan)
    And the second repo tab uses REPO_COLORS[1] (Green)
    And the third repo tab uses REPO_COLORS[2] (Yellow)

  # ===================================================================
  # Colored vertical bar on each row
  # ===================================================================

  @unit
  Scenario: Each worktree row has a colored vertical bar as first visual element
    Given rows from repo at config index 0 and repo at config index 1
    When the task list renders
    Then each row starts with a "▎" character in its repo color
    And the vertical bar color matches the repo's REPO_COLORS entry by config index
    # Constraint: the bar is a Cell in the first column position, before the issue number

  @unit
  Scenario: Vertical bar color is consistent with tab bar color for same repo
    Given a row from repo at config index 2
    When the task list renders
    Then the row's vertical bar uses REPO_COLORS[2] (Yellow)
    And the tab for that repo also uses REPO_COLORS[2] (Yellow)

  @unit
  Scenario: Selected row vertical bar retains repo color
    Given the cursor is on a row from repo at config index 0
    When the task list renders
    Then the vertical bar still uses REPO_COLORS[0] (Cyan) for that row
    And the rest of the row uses the selected row style (bold accent)
    # Constraint: the bar color is per-repo, not overridden by selection style

  @unit
  Scenario: Standalone session rows have no repo vertical bar
    Given there are standalone tmux sessions (not attached to any repo)
    When the task list renders
    Then standalone session rows show an empty first column instead of a colored bar

  @unit
  Scenario: Vertical bar column is narrow
    Given the task list is rendering with the vertical bar column
    When column widths are computed
    Then the vertical bar column uses a fixed width of 1 character
    And it does not have a header label (empty header cell)

  @unit
  Scenario: Group header rows include empty bar cell for alignment
    Given there are worktree rows grouped by display group
    When a group header row renders
    Then the group header row has an empty first cell (bar column placeholder)
    And the group label text remains in the correct column position
    # Constraint: group_header_row must prepend an empty cell to keep text aligned with data rows

  @integration
  Scenario: Repo index lookup maps repo_slug to correct palette color
    Given global config has repos ["owner/alpha", "owner/beta", "owner/gamma"]
    And a WorktreeRow with repo_slug "owner/beta"
    When the row's repo index is resolved against global config
    Then it maps to index 1
    And repo_color(1) returns REPO_COLORS[1] (Green)

  @integration
  Scenario: Repos beyond palette size wrap around
    Given global config has 8 repos (more than REPO_COLORS length of 6)
    When the 7th repo's color is resolved
    Then it uses REPO_COLORS[0] (Cyan) via modulo wrapping
    And the 8th repo uses REPO_COLORS[1] (Green)

  # ===================================================================
  # Hints bar cleanup
  # ===================================================================

  @unit
  Scenario: Repo cycling hint is removed from hints bar
    When the hints bar renders in task mode
    Then it does not contain "◄►" text
    And it does not contain ":repos" text
    # Constraint: Left/Right keybindings still work, they're just not shown in hints

  @unit
  Scenario: Active repo indicator bracket is removed from hints bar
    Given the active repo index is 1
    When the hints bar renders in task mode
    Then it does not contain "[◄" or "►]" text
    And it does not contain the repo slug in bracket notation
    # The tab bar already shows which repo is active, making the hint redundant

  @unit
  Scenario: Other hints remain unchanged after repo hint removal
    When the hints bar renders in task mode
    Then it still shows "enter switch", "o pr", "p:priority", "B:branch", "/:search"
    And the search active indicator still works when search text is non-empty
    And the "q quit" and "? help" hints are still present
