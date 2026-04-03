# TUI Multi-Pane Navigation

**Date:** 2026-03-31
**Status:** Decided
**Decision:** Strategy 3 (Expandable Sub-Rows). Phase A shipped in `a5e3e53`. Phase B tracked in #93.

## Question

How should Orchard's TUI represent and navigate multiple tmux panes within sessions, given the current flat-table worktree list?

**Constraints:**
- Existing ratatui 0.30 Table-based TUI must remain functional
- OrchardState → WorktreeRow → EnrichedSession pipeline is established
- Pane data already collected (pane_titles, pane_commands) but only pane 0 displayed
- Must work for both local and remote (SSH) sessions
- Dashboard "at a glance" quality is core to product value

**Decision type:** Reversible — UI-only change, no data migration. Moderate blast radius (TUI rendering, navigation, preview capture).

## Current State

The data pipeline already collects multi-pane data but discards it before the display layer:

- `CachedTmuxSession` stores `pane_titles: Vec<String>` and `pane_commands: Vec<String>` for all panes
- `enrich_session_from_scraping()` scans all panes for Claude detection but collapses to a single `ClaudeSessionInfo`
- `SessionState` (display layer) has no pane fields — only name, host, claude enrichment
- `TmuxSession.pane_title` is only pane index 0
- Preview pane captures `sessions.first()` active pane only via `capture_pane_content()` (which targets `-t session` without pane specifier)
- `capture_pane_content()` can target specific panes by changing `-t` to `-t session:window.pane` — the mechanism exists
- Navigation is a flat cursor (`App.cursor: usize`) over standalone sessions + visible worktree tasks
- No expand/collapse, no sub-cursor, no tree widget in current dependencies

Key files: `src/cache.rs:71-91`, `src/tmux.rs:15-75`, `src/derive.rs:255-348`, `src/tui/list.rs:1093-1257`, `src/tui/mod.rs:522-658`

## Landscape

### Prior art

| Tool | Approach | Key insight |
|------|----------|-------------|
| **tmux choose-tree** | Tree with Right/Left expand/collapse, Enter to switch, preview pane | The canonical reference — users already know this convention |
| **nexus-tui** | Groups → Sessions tree with Alt+prefix keys, glow effects for pending | Closest analog to Orchard; uses Alt+ to avoid conflicting with main panel |
| **Orchestra** | Ratatui worktree → session two-level tree, Enter to expand/select | Confirms the model works in Ratatui |
| **lazygit/gitui** | Multi-panel flat layout with Tab to cycle panels | Shows that flat panels + tab cycling is viable for git tooling |
| **k9s** | Drill-down stack with breadcrumbs, :xray for tree view | Right model for deep hierarchies; overkill for shallow ones |
| **sesh** | Flat fuzzy-search list (no hierarchy) | Trades hierarchy visibility for speed of access |
| **broot** | Inline tree with fuzzy search filtering | Novel: type to filter, tree collapses to matches |

### Ecosystem

- **tui-tree-widget 0.24.0** — Compatible with ratatui 0.30. Clean API: `TreeItem` nesting, `TreeState` with built-in expand/collapse/select. Generic identifiers. But renders as styled Text, not table cells — loses columnar layout.
- **tui-widget-list** — Flat lists with arbitrary per-row widgets. No native hierarchy but could render pre-flattened tree with manual indentation.
- **Manual flatten-to-render** — Standard pattern: transform tree to `Vec<FlattenedNode>` with depth for indentation. Both tui-tree-widget and custom solutions use this internally.

### Navigation conventions

| Key | Action | Convention source |
|-----|--------|-------------------|
| Right / l | Expand / open | tmux choose-tree, ranger, lf |
| Left / h | Collapse / close | tmux choose-tree, ranger, lf |
| Enter / Space | Toggle or select | tui-tree-widget, Orchestra |
| j/k or ↑/↓ | Navigate within level | Universal TUI |
| Esc | Deselect / go back | k9s, general TUI |

## Strategies Evaluated

### Strategy 1: Preview-Only Pane Switcher

Keep flat table as-is. Add Tab/Shift-Tab to cycle which session+pane is shown in the preview panel. "Pane 2/3" indicator in preview header.

- **Requires:** Pane index on `capture_pane_content()`, `selected_pane: usize` in App, 2 new Message variants
- **Strengths:** ~50 lines changed. Zero rendering risk. Fits existing TEA pattern seam (like PreviewPageUp/Down)
- **Weaknesses:** Panes not visible at a glance — must Tab through to discover. No structural context (which session? which pane?). Doesn't extend to session-level actions
- **Effort:** S
- **Risk:** Very low

### Strategy 2: tui-tree-widget Migration

Replace Table with tui-tree-widget. Worktree → Session → Pane as three levels with built-in expand/collapse.

- **Requires:** New dep tui-tree-widget 0.24.0, rewrite row builder to produce TreeItems, TreeState in App, rework preview
- **Strengths:** Purpose-built widget, free expand/collapse + scroll + mouse, follows tmux choose-tree UX
- **Weaknesses:** Loses tabular column layout (TreeItem is styled Text, not table cells). Significant rendering rewrite. Column alignment requires fighting the abstraction
- **Effort:** L
- **Risk:** High — columnar data in a tree widget may look worse

### Strategy 3: Expandable Sub-Rows in Existing Table

Keep Table widget. Right/l on a worktree row inserts indented sub-rows for sessions and panes. Left/h collapses. Track `expanded: HashSet<WorktreeId>` in App.

- **Requires:** `PaneState` struct, expanded state, modified row builder, preview follows sub-row cursor
- **Strengths:** Preserves column layout for parent rows. Follows tmux choose-tree convention. Ambient awareness preserved — all worktrees visible even with some expanded. Sub-rows can have different column content (session name | pane command | claude status)
- **Weaknesses:** Custom expand/collapse logic. Sub-rows have different column semantics — visual alignment needs care. Cursor math with dynamic row insertion. Background refresh during expansion needs careful state reconciliation
- **Effort:** M
- **Risk:** Medium — cursor/selection math is the main complexity

### Strategy 4: k9s-Style Drill-Down Stack

Enter pushes a new view (sessions list, then panes list). Esc pops back. Breadcrumb at top.

- **Requires:** ViewStack, new ViewState variants, separate rendering per level, breadcrumb widget
- **Strengths:** Each level owns its full UX with appropriate columns. Scales to any depth. Clean architecture (ViewStack). No column-alignment issues
- **Weaknesses:** Loses dashboard overview on drill-down — can't see other worktrees. More keystrokes to reach a pane. More view code to maintain. Esc conflicts with existing cancel semantics
- **Effort:** M-L
- **Risk:** Medium — trades away core "at a glance" value

### Strategy 5: Inline Session/Pane Badges

Keep flat table. Expand CLAUDE column to show per-session badges inline (⚡s1:p0 ●s1:p1). Badge selection switches preview.

- **Requires:** Pane data threaded to display layer, richer claude_status_text(), badge selection state
- **Strengths:** All info visible at a glance without expanding
- **Weaknesses:** Cluttered with 3+ sessions. Badge selection UX awkward in table. Can't show pane titles/commands
- **Effort:** S-M
- **Risk:** Scales poorly

## Debate Summary

Three agents argued for Strategies 1, 3, and 4. Key arguments:

**Strategy 1's strongest case:** The dashboard's "at a glance" quality is sacrosanct. Adding expand/collapse makes it a navigator, not a dashboard. Tab fits the existing TEA pattern seam perfectly. Complexity isn't justified until proven otherwise.

**Strategy 3's strongest case:** If the data is worth collecting, it's worth showing — not hiding behind an invisible Tab cycle. The tmux choose-tree convention (Right/Left) is already in users' muscle memory. Ambient awareness is preserved because all worktrees remain visible even with some expanded. The decisive question: "does the user need to see this in context with other worktrees?" — Orchard's entire value says yes.

**Strategy 4's strongest case:** The hierarchy is real and will grow. Each level deserves its own UX with appropriate columns. ViewStack is architecturally cleaner than expansion state management. Clean testing — each view variant tested independently.

**What was decisive:** Strategy 3's ambient awareness argument. Orchard is a dashboard, not a file manager. Strategy 4 trades away the dashboard on drill-down. Strategy 1 preserves the dashboard but hides structure users need. Strategy 3 keeps the dashboard while surfacing hierarchy on demand.

**What nearly won:** Strategy 1's simplicity argument was compelling. The "ship 1 first, build 3 later" phased approach was acknowledged as pragmatic by all three agents.

## Recommendation

**Strategy 3 (Expandable Sub-Rows)** with a phased rollout:

Phase A (standalone preview pane cycling) was originally planned as a quick win but is subsumed by Phase B — once sub-rows exist with preview-follows-cursor, separate Tab cycling in preview is unnecessary.

**Phase B (#93):** Build Strategy 3 with refinements from design discussion:
- Flatten to one level (pane sub-rows directly under worktree, no intermediate session row)
- Sub-rows use same Table column count — pack tree connector + pane index into `#` cell, command into `TITLE` cell, claude indicator into `CLAUDE` cell, rest empty
- Collapse indicator (`▶3` / `▼3`) on parent rows with multiple panes
- `E` key for expand-all / collapse-all
- `capture_pane_content()` needs pane index parameter (`-t session:0.{pane}`)
- Also: move CLAUDE column to after `#`, fix duplicate numbering between standalone and worktree rows

Reconsider if:
- Product evolves toward full session management with per-session operations → adopt Strategy 4's ViewStack

## Open Questions (Resolved)

1. **Sub-row column layout:** Custom columns per row type. Session sub-rows and pane sub-rows get their own column layout, not the parent's grid. Session sub-rows show: session name, host, claude state. Pane sub-rows show: pane index, running command, whether claude is the command.

2. **Expansion persistence:** Yes — track by session name, not index. If a session still exists after background refresh, keep it expanded. If it disappeared, silently collapse. No debouncing needed.

3. **Direct pane switching:** Enter on a session sub-row switches tmux to that session. Enter on a pane sub-row switches to that session AND selects the pane (`tmux select-pane -t session:window.pane`).

4. **Per-pane Claude detection:** Keep the per-session aggregate for session sub-rows (Input/Working/Idle — the rich state from output scraping). Pane sub-rows just show whether claude is the running command (check if pane_command contains "claude") plus the command name. No per-pane output scraping needed.

5. **Key conflict:** ~~Right/Left currently cycles repo filter.~~ Remap repo/workspace cycling to Tab/Shift+Tab (they literally are tabs). Frees Right/Left and h/l for expand/collapse, matching tmux choose-tree convention.

## Sources

1. [tui-tree-widget on crates.io](https://crates.io/crates/tui-tree-widget)
2. [tui-rs-tree-widget GitHub](https://github.com/EdJoPaTo/tui-rs-tree-widget)
3. [tui-tree-widget docs.rs](https://docs.rs/tui-tree-widget/latest/tui_tree_widget/)
4. [tmux choose-tree — Waylon Walker](https://waylonwalker.com/tmux-choose-tree/)
5. [nexus-tui GitHub](https://github.com/markx3/nexus-tui)
6. [Orchestra GitHub](https://github.com/humanunsupervised/orchestra)
7. [lazygit GitHub](https://github.com/jesseduffield/lazygit)
8. [k9s CLI](https://k9scli.io/)
9. [k9s GitHub](https://github.com/derailed/k9s)
10. [broot GitHub](https://github.com/Canop/broot)
11. [sesh GitHub](https://github.com/joshmedeski/sesh)
12. [Ratatui state management patterns — DeepWiki](https://deepwiki.com/ratatui/ratatui/4.3-state-management-patterns)
13. [Ratatui third-party widgets](https://ratatui.rs/showcase/third-party-widgets/)
14. [tui-widget-list GitHub](https://github.com/preiter93/tui-widget-list)
