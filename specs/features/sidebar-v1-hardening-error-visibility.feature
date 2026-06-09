Feature: orchard-gui sidebar v1 hardening + error visibility (parent #548)
  As an orchard-gui user juggling worktrees, tmux panes, and Claude sessions
  I want the sidebar to render every pane reliably, refresh on lens re-entry, surface repo + Claude state, collapse, and never swallow errors silently — and terminal URLs to be clickable
  So that the sidebar is trustworthy at-a-glance and failures are diagnosable

  Background:
    Given the orchard daemon is running at "127.0.0.1:7777"
    And the daemon GraphQL exposes "Worktree.tmuxPanes", "Worktree.repo", "ClaudeInstance.state", "ClaudeInstance.pane", and "TmuxPane.{process,currentCommand,title,paneId,claudeInstance}"
    And the active lens enum is "attention | recent | tmux | issue | worktree"

  # ===================================================================
  # AC1 (parent #548) — Worktree lens shows one item per tmux pane
  # ===================================================================

  @e2e
  Scenario: Worktree lens shows one sidebar item per tmux pane on a multi-pane session
    Given a worktree "feature/foo" has a tmux session with 3 panes
    And only pane 1 has a Claude instance attached
    When orchard-gui renders the worktree lens
    Then the sidebar shows 3 sidebar items for that worktree
    And the item for pane 1 renders Claude-state information
    And the items for panes 2 and 3 render as tmux-only rows (no Claude pill)

  @integration
  Scenario: Pane without an attached Claude session still renders
    Given a worktree has a single tmux pane with no Claude instance attached
    When the worktree lens builds sidebar items
    Then a sidebar item exists for that pane
    And the item is keyed by the pane's "paneId"
    And the item shows pane process / currentCommand / title rendered from "TmuxPane" fields

  @integration
  Scenario: Worktree lens iterates Worktree.tmuxPanes (not Worktree.claudeInstances)
    Given the WorktreeLens GraphQL query
    When the query is introspected
    Then the selection set on "worktrees" includes "tmuxPanes { ...PaneCard, claudeInstance { ...SessionCard } }"
    And the selection set on "worktrees" does NOT use "claudeInstances" as the iteration source for sidebar items

  @unit
  Scenario: buildWorktreeSections keys items by pane id, not by claude instance id
    Given a Worktree with two panes, both with the same ClaudeInstance attached (edge case)
    When buildWorktreeSections runs
    Then it emits two sidebar items (one per pane)
    And each item's key is derived from "pane.paneId"

  @unit
  Scenario: A worktree with zero panes still appears in the sidebar (dormant row)
    Given a Worktree has no tmux panes and no Claude instances
    When buildWorktreeSections runs
    Then a single dormant worktree item is emitted (mirrors existing buildDormantWorktreeItem behavior)

  # ===================================================================
  # AC2 (parent #548) — Worktree lens refreshes on re-entry
  # ===================================================================

  @e2e
  Scenario: Switching back to the worktree lens always refreshes the sidebar
    Given orchard-gui is mounted on the worktree lens
    And the user switches to the attention lens
    And the daemon's worktree state changes (e.g. a new pane appears)
    When the user switches back to the worktree lens
    Then the sidebar reflects the daemon's current worktree state (not the snapshot from initial mount)

  @integration
  Scenario: Each lens fetches its store on re-entry
    Given LensSidebar is mounted with active lens "worktree"
    When the active lens changes from "worktree" to "attention" and back to "worktree"
    Then "worktreeStore.fetch()" is invoked on each entry to the worktree lens
    And the fetch policy is CacheAndNetwork (cache served instantly, network revalidates)

  @integration
  Scenario: Lens-switch does not re-fetch stores for inactive lenses
    Given the active lens is "worktree"
    When the lens switches to "attention"
    Then only the attention store fetches
    And the worktree store does not refetch until the user returns to the worktree lens

  @unit
  Scenario: LensSidebar uses $effect keyed on lens, not onMount, for store fetches
    Given the LensSidebar component source
    When the source is parsed
    Then per-lens store fetches are inside a "$effect" block keyed on the active lens
    And no per-lens store fetch is invoked from a one-shot "onMount" handler
    And the stale comment claiming "subscribeAll → cache patch re-renders the sidebar" is removed or corrected

  # ===================================================================
  # Sub-issue #550 — Sidebar items show which repo they belong to
  # ===================================================================

  @e2e
  Scenario: Sidebar item shows a repo chip alongside the branch
    Given the user has worktrees across two repos: "orchardist" and "langwatch"
    When orchard-gui renders the sidebar
    Then each sidebar item visibly shows its repo (chip, label, or icon) alongside the branch
    And two items on different repos but the same branch name are visually distinguishable without hovering or expanding

  @integration
  Scenario: Repo data is sourced from Worktree.repo via the existing fragment
    Given the WorktreeEnrichment fragment
    Then the fragment selects "repo" from "Worktree"
    And SidebarItem.svelte renders "item.repo" without an additional query

  @unit
  Scenario: Items with no repo metadata fall back gracefully
    Given a sidebar item whose "Worktree.repo" is null or empty
    When SidebarItem.svelte renders
    Then no broken/empty repo chip is rendered
    And the item still renders the branch and host

  # ===================================================================
  # Sub-issue #551 — Sidebar sections are collapsible
  # ===================================================================

  @e2e
  Scenario: User collapses a section
    Given the sidebar shows multiple sections in any lens
    When the user clicks a section header (or its chevron)
    Then that section's items collapse from view
    And the chevron flips to indicate collapsed state
    And other sections are unaffected

  @e2e
  Scenario: Collapsed state persists across app restart
    Given the user collapses a sidebar section
    When orchard-gui is closed and reopened
    Then the previously-collapsed section renders collapsed on startup

  @integration
  Scenario: Collapse state is persisted in localStorage
    Given the user toggles a section's collapse state
    Then a localStorage key "orchard:sidebar:collapsed" records each section's collapsed/expanded state by section identifier

  @unit
  Scenario: Each section toggles independently
    Given two sections "A" and "B" are both expanded
    When the user collapses section "A"
    Then section "B" remains expanded

  # ===================================================================
  # Sub-issue #553 — Claude instance state pill (working/idle/input/stalled/dead/no_claude)
  # ===================================================================

  @e2e
  Scenario Outline: Sidebar item visually distinguishes each Claude instance state
    Given a sidebar item whose attached ClaudeInstance has state "<state>"
    When SidebarItem.svelte renders
    Then the state pill renders with a visual affordance distinct from every other state
    And the rendering is not the binary working/idle pip that exists today

    Examples:
      | state    |
      | working  |
      | idle     |
      | input    |
      | stalled  |
      | dead     |

  @e2e
  Scenario: "input" state is rendered prominently (blocked on user)
    Given a sidebar item whose ClaudeInstance state is "input"
    When SidebarItem.svelte renders
    Then the state pill is visually prominent (e.g. high-contrast color, glyph) compared to "idle" or "working"

  @integration
  Scenario: SidebarItem reads state from item.state (sourced from ClaudeInstance.state)
    Given a Worktree's pane has a ClaudeInstance with state "stalled"
    When the sidebar item is built
    Then "item.state" is "stalled"
    And SidebarItem.svelte renders the "stalled" pill

  @unit
  Scenario: Pane without a Claude instance renders no state pill
    Given a sidebar item whose pane has no ClaudeInstance attached
    When SidebarItem.svelte renders
    Then no Claude state pill is rendered
    And the item reads as a tmux-only / worktree-only row

  # ===================================================================
  # Sub-issue #600 — Error visibility (Toaster, catch audit, activity-lens disambiguation)
  # ===================================================================

  @e2e
  Scenario: A failed mutation surfaces a user-visible toast
    Given the global "<Toaster>" is wired into the app root layout
    And the user triggers an action whose underlying mutation throws (e.g. daemon disconnected)
    When the failure occurs
    Then a toast is rendered with the error message
    And the dev-tools console contains the stack trace

  @integration
  Scenario: A toast.error helper is provided and wired to Toaster
    Given the orchard-gui codebase
    Then a "toast.error(err)" helper exists
    And the helper renders through the global "<Toaster>"

  @integration
  Scenario: Every catch block is annotated or wired through toast.error
    Given the source tree under "crates/orchard-gui/src/"
    When every "catch" block is audited
    Then each catch block either:
      | behavior                                                       |
      | calls toast.error(...) (and rethrows or logs)                  |
      | has a "// intentional swallow: <reason>" comment with rationale|
    And no unannotated silent swallow remains

  @e2e
  Scenario: The lens previously called "activity" (the attention lens) renders items or shows a typed error
    Given the user clicks the attention lens (referred to as "activity lens" in #600)
    When the lens activates
    Then either the lens renders sidebar items
    Or a typed error is surfaced via the toast and console (no silent no-op)

  @unit
  Scenario: "activity lens" is treated as the existing attention lens (unless user confirms a new sixth lens)
    Given the live Lens enum is "attention | recent | tmux | issue | worktree"
    When this PR ships
    Then no new "activity" lens is introduced
    And any reference labeled "activity lens" maps to the attention lens

  # ===================================================================
  # Sub-issue #601 — Terminal URLs are clickable
  # ===================================================================

  @e2e
  Scenario: A printed URL underlines on hover and opens in the default browser on click
    Given a terminal pane prints "https://github.com/drewdrewthis/orchardist"
    When the user hovers the URL
    Then the URL underlines
    When the user clicks the URL (cmd-click on macOS, plain click on platforms where that is conventional)
    Then the URL opens in the system default browser

  @integration
  Scenario: WebLinksAddon is configured with an activateCallback
    Given TerminalAttach.svelte loads "@xterm/addon-web-links" (WebLinksAddon)
    Then WebLinksAddon is constructed with an "activateCallback" that opens URLs via the Tauri shell API (or window.open as a fallback)
    And initialization errors are surfaced via toast.error (not silently caught)

  @unit
  Scenario: Click-to-open respects platform convention
    Given orchard-gui is running on macOS
    Then the URL activates on cmd-click (not plain click)
    Given orchard-gui is running on a platform where plain click is conventional
    Then the URL activates on plain click

  @integration
  Scenario: URL clickability does not regress text selection or copy/paste
    Given a terminal pane contains a URL inline with surrounding text
    When the user drags to select a region that includes the URL
    Then the selection succeeds
    And copy/paste of the selection works as before

  # ===================================================================
  # AC3 (parent #548) — PR closes all six issues
  # ===================================================================

  @integration
  Scenario: The bundled PR closes the parent and all sub-issues
    Given the PR landing this work
    Then the PR description body contains:
      | closes  |
      | #548    |
      | #550    |
      | #551    |
      | #553    |
      | #600    |
      | #601    |
    And merging the PR auto-closes all six issues

  # --- AC Coverage Map ---
  # AC1 (panes): "Worktree lens shows one sidebar item per tmux pane attached to that worktree, not one per ClaudeInstance. Panes without a Claude session render too, sourced from Worktree.tmuxPanes."
  #   → Scenario: Worktree lens shows one sidebar item per tmux pane on a multi-pane session
  #   → Scenario: Pane without an attached Claude session still renders
  #   → Scenario: Worktree lens iterates Worktree.tmuxPanes (not Worktree.claudeInstances)
  #   → Scenario: buildWorktreeSections keys items by pane id, not by claude instance id
  #   → Scenario: A worktree with zero panes still appears in the sidebar (dormant row)
  #
  # AC2 (worktree lens re-entry): "Switching to the worktree lens after visiting another lens always refreshes the sidebar — the data shown reflects the daemon's current state, not the snapshot taken on initial mount."
  #   → Scenario: Switching back to the worktree lens always refreshes the sidebar
  #   → Scenario: Each lens fetches its store on re-entry
  #   → Scenario: Lens-switch does not re-fetch stores for inactive lenses
  #   → Scenario: LensSidebar uses $effect keyed on lens, not onMount, for store fetches
  #
  # AC3 (sub-issues): "Each sub-issue (#550, #551, #553, #600, #601) has its own AC met by the bundled PR. PR auto-closes all six."
  #   → #550 (repo): Sidebar item shows a repo chip alongside the branch / Repo data is sourced from Worktree.repo / Items with no repo metadata fall back gracefully
  #   → #551 (collapsible): User collapses a section / Collapsed state persists across app restart / Collapse state is persisted in localStorage / Each section toggles independently
  #   → #553 (state pill): Sidebar item visually distinguishes each Claude instance state (Outline) / "input" state is rendered prominently / SidebarItem reads state from item.state / Pane without a Claude instance renders no state pill
  #   → #600 (error visibility): A failed mutation surfaces a user-visible toast / A toast.error helper is provided / Every catch block is annotated or wired through toast.error / The lens previously called "activity" renders items or shows a typed error / "activity lens" is treated as the existing attention lens
  #   → #601 (clickable URLs): A printed URL underlines on hover and opens in the default browser on click / WebLinksAddon is configured with an activateCallback / Click-to-open respects platform convention / URL clickability does not regress text selection or copy/paste
  #   → Closure: The bundled PR closes the parent and all sub-issues
