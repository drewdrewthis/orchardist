# 003: TUI Navigation Models for Hierarchical Session Management

**Status:** Research complete
**Date:** 2026-03-31
**Question:** What navigation patterns exist in the wild for tools like Orchard (repos > worktrees > sessions), and which are appropriate for a "quickly switch to a workspace" primary action?

## 1. Tmux Session Managers

### sesh (Go, by Josh Medeski)
**Pattern: Flat fuzzy-search list with recency ranking**

- Lists active tmux sessions + zoxide directories in a single flat list
- Pipes through external fuzzy finder (fzf, television, gum) or built-in picker
- Multi-modal source filtering via Ctrl keybinds: Ctrl-t (tmux), Ctrl-g (configs), Ctrl-x (zoxide), Ctrl-f (filesystem)
- `sesh last` toggles between two most recent sessions (no picker needed)
- Preview via `sesh preview {}` in fzf integration
- Automatic session naming from git repo/remote/directory
- **Key insight:** The fastest navigation is no navigation. Recency + fuzzy search beats hierarchical browsing for "switch to workspace" workflows.
- **Source:** [sesh GitHub](https://github.com/joshmedeski/sesh), [Smart tmux sessions with sesh](https://www.joshmedeski.com/posts/smart-tmux-sessions-with-sesh/)

### tmux-sessionx (tmux plugin, by omerxx)
**Pattern: Modal fuzzy popup with mode switching**

- Launches as fzf-tmux popup via `<prefix>+O`
- Mode-based architecture: Session (default), Window (Ctrl-w), Tree (Ctrl-t), Config (Ctrl-x), Expand (Ctrl-e), Tmuxinator (Ctrl-/)
- Current session hidden by default (smart: you never want to "switch" to where you already are)
- Typing a non-existent name + Enter creates a new session
- Optional preview pane (toggle with `?`) showing directory contents
- Ctrl-b for "back" between modes
- **Key insight:** Modes accessed via Ctrl-key chords, not modal state. Each mode is a different data source, not a different UI state.
- **Source:** [tmux-sessionx GitHub](https://github.com/omerxx/tmux-sessionx)

### gession (Go, by verte-zerg)
**Pattern: Two-mode TUI with fuzzy search**

- Normal mode: manage existing tmux sessions
- Prime mode (`--prime`): list directories for session creation
- Built-in fuzzy search, no external dependencies
- **Key insight:** Separating "manage existing" from "create new" as explicit modes reduces accidental creation.
- **Source:** [gession GitHub](https://github.com/verte-zerg/gession)

### tmux choose-tree (built-in tmux)
**Pattern: Hierarchical tree with Right/Left expand/collapse**

- Sessions > Windows > Panes as three-level tree
- Right/l expands, Left/h collapses
- Preview pane shows selected item content
- Enter switches to selection
- **Key insight:** The canonical reference for tmux hierarchy navigation. Users already know these conventions.
- **Source:** [tmux choose-tree](https://waylonwalker.com/tmux-choose-tree/)

### t-smart-tmux-session-manager / tmux-session-wizard
**Pattern: fzf popup with zoxide integration**

- Bound to `<prefix>+T`, triggers fzf-tmux popup
- Shows existing sessions followed by zoxide directories
- Creates session if it doesn't exist, switches if it does
- **Key insight:** The "create-or-switch" pattern eliminates a decision point. User never has to think "does this exist?" — they just pick it.
- **Source:** [t-smart-tmux-session-manager GitHub](https://github.com/joshmedeski/t-smart-tmux-session-manager), [tmux-session-wizard GitHub](https://github.com/27medkamal/tmux-session-wizard)

### Common Pattern Across All Session Managers

Every successful tmux session manager converges on the same core UX:
1. **Fuzzy search is the primary navigation** (not tree browsing)
2. **Recency/frecency ranking** puts likely targets at top
3. **Single action** to select (Enter switches/creates)
4. **Minimal chrome** — popup/overlay, not a full application
5. **Sub-second interaction** — invoke, type 2-3 chars, Enter, done

## 2. Git Worktree TUI Tools

### lazyworktree (Go, BubbleTea)
**Pattern: Pane-based TUI with command palette**

- Worktree pane (primary list) + Agent sessions pane
- Command palette via `?` for action discovery
- `e` opens metadata editor per worktree
- Auto-opens worktrees in tmux windows/panes
- Per-worktree hooks via `.wt` files
- Markdown notes and task tracking per worktree
- **Key insight:** Closest competitor to Orchard. Uses pane-based layout (not tree), command palette for discoverability, and worktree-as-primary-unit model.
- **Source:** [lazyworktree GitHub](https://github.com/chmouel/lazyworktree)

### agent-deck (TypeScript)
**Pattern: Status-driven flat list with fuzzy search and filters**

- Flat session list with color-coded status symbols (running/waiting/idle/error)
- Status filters: `!` running, `@` waiting, `#` idle, `$` error
- Fuzzy search via `/`, global search via `G`
- Fork (`f`), restart (`r`), delete (`d`), move to group (`M`)
- Sessions grouped by project
- tmux status bar integration with numbered shortcuts (Ctrl-b, then 1-6)
- **Key insight:** Status indicators + filter-by-status is more useful than hierarchy for "what needs my attention" workflows. The primary question isn't "where is it in the tree?" but "what state is it in?"
- **Source:** [agent-deck GitHub](https://github.com/asheshgoplani/agent-deck)

### Claude Launcher TUI (by Prakhar)
**Pattern: Fuzzy search + bookmarks with project grouping**

- Fuzzy search across project names and last user message
- Bookmarked sessions (starred) float to top
- Enter to resume, `g` for Ghostty, `s` to bookmark, `x` to remove bookmark
- Ctrl+F to fork conversation
- Sessions group by project within tmux
- **Key insight:** Bookmarks/pinning as a complement to recency sorting. Users have 2-3 "current" projects that should always be top regardless of recency.
- **Source:** [Claude Launcher blog post](https://prakhar.codes/blog/i-built-a-tui-to-stop-losing-claude-sessions)

### rsworktree, wtm, Git-Worktree-Visualizer
- Simpler tools focused on single-repo worktree management
- None handle multi-repo dashboards
- **Key insight:** Multi-repo is Orchard's differentiator. No other worktree TUI does this well.
- **Source:** [rsworktree](https://github.com/ozankasikci/rust-git-worktree), [wtm](https://github.com/tefla/wtm-worktree-manager), [Git-Worktree-Visualizer](https://github.com/PeterHdd/Git-Worktree-Visualizer)

## 3. Hierarchical TUI List Patterns

### lazygit — Multi-panel flat layout
**Pattern: Side-by-side panels with Tab cycling**

- 5 panels always visible: Status, Files, Branches, Commits, Stash
- Tab cycles focus between panels
- Each panel is a flat list (files optionally tree view)
- h/j/k/l or arrows for navigation
- File tree view toggleable (flat list or hierarchical)
- Command log shows all git commands executed (transparency)
- **Key lesson from 5 years:** Command transparency ("showing what git commands run") became one of the most loved features. Users want to understand what's happening behind the UI.
- **Anti-pattern discovered:** Delayed feedback mechanisms. Issue boards weren't enough; feedback surveys revealed unexpected priorities.
- **Source:** [lazygit GitHub](https://github.com/jesseduffield/lazygit), [Lazygit 5 Years On](https://jesseduffield.com/Lazygit-5-Years-On/), [lazygit tree view toggle](https://github.com/jesseduffield/lazygit/issues/3554)

### k9s — Drill-down stack with breadcrumbs
**Pattern: Push/pop view stack**

- `:resource` command navigates to resource type view
- Enter drills down into a resource
- Esc pops back to previous view
- `:xray` shows hierarchical dependency view
- Ctrl-a lists available resource types
- `/` for in-view search
- **Key insight:** Drill-down works for deep, wide hierarchies (Kubernetes has dozens of resource types). Overkill for shallow hierarchies (repos > worktrees > sessions is only 3 levels).
- **Anti-pattern for Orchard:** Loses dashboard overview on drill-down. You can't see "all repos at a glance" if you've drilled into one repo's worktrees.
- **Source:** [k9s](https://k9scli.io/), [k9s navigation guide](https://thinhdanggroup.github.io/k9s-cli/)

### gitui — Tab-based top-level with flat panels
**Pattern: Numbered tabs + panel focus**

- 5 tabs: Status, Logs, Files, Stashing, Stashes
- Number keys (1-5) or Tab/Shift-Tab to switch tabs
- Each tab has its own panel layout
- **Key insight:** Tabs work for orthogonal views (status vs logs vs files). Less useful when the views are hierarchically related (repos > worktrees).
- **Source:** [gitui GitHub](https://github.com/gitui-org/gitui), [gitui review](https://zolmok.org/gitui/)

### broot — Tree with inline fuzzy search
**Pattern: Type-to-filter tree**

- Tree view of directory structure
- Typing immediately filters the tree (fuzzy)
- Best match auto-selected
- Composite queries: `!rs&c/pattern` (not Rust files containing pattern)
- Never blocks — keystroke interrupts current search
- Stops searching when enough matches found (adaptive)
- **Key insight:** Filtering AS navigation, not filtering THEN navigation. The tree collapses to show only matches, preserving hierarchical context around them. This is the novel middle ground between "flat fuzzy list" and "browsable tree."
- **Source:** [broot GitHub](https://github.com/Canop/broot), [broot navigation docs](https://dystroy.org/broot/navigation/)

### Atuin — Recency/frecency-scored search
**Pattern: Fuzzy search with context-aware filtering**

- Full-screen TUI search with Up/Down selection
- Filter modes switchable via Ctrl-r: global, host, session, directory, workspace (git repo)
- Search modes: prefix, fulltext, fuzzy, skim, daemon-fuzzy
- Frecency scoring: `recency_score * recency_multiplier + frequency_score * frequency_multiplier`
- Enter runs command, Tab inserts for editing
- Alt+# replays nth visible result
- **Key insight:** Context-aware filtering (directory, session, host) lets the same search interface serve different needs without separate views. The "workspace" filter (git repo scoped) is directly relevant to Orchard's multi-repo model.
- **Source:** [Atuin](https://atuin.sh/), [Atuin config docs](https://docs.atuin.sh/cli/configuration/config/)

### Summary: Flat vs Tree vs Tabs vs Drill-Down

| Approach | Best For | Weakness | Examples |
|----------|----------|----------|----------|
| **Flat list + fuzzy** | Speed of access, shallow data | Loses hierarchical context | sesh, fzf, atuin |
| **Tree with expand/collapse** | Ambient awareness + hierarchy | More keystrokes, cursor math complexity | tmux choose-tree, broot |
| **Tabs** | Orthogonal views | Poor for hierarchically related data | gitui, lazygit panels |
| **Drill-down stack** | Deep/wide hierarchies | Loses overview on drill-down | k9s |
| **Type-to-filter tree** | Best of both worlds | Novel UX, may surprise users | broot |

## 4. Ratatui Ecosystem Widgets

### Tree Views

| Crate | Compat | Notes |
|-------|--------|-------|
| **tui-tree-widget** (EdJoPaTo) | ratatui 0.30 | Most mature. TreeItem nesting, TreeState with built-in expand/collapse/select. Generic identifiers. Renders as styled Text, NOT table cells — loses columnar layout. v0.24.0 (Jan 2026). |
| **ratatui-tree-widget** (woxjro) | ratatui 0.30 | Alternative implementation, less documented. |
| **ratatui-cheese** | ratatui 0.30 | BubbleTea-inspired widgets including tree, list, spinner, help, paginator. |

**Key limitation:** tui-tree-widget renders TreeItems as styled Text lines, not table cells. Orchard's columnar layout (branch | status | claude | PR) would require either (a) fighting the abstraction to align columns, or (b) building custom expandable rows in Table.

### Fuzzy Finders

| Crate | Notes |
|-------|-------|
| **nucleo** (helix-editor) | High-performance fuzzy matcher. Background threadpool matching with snapshot API. Used by helix, television. The gold standard for Rust fuzzy matching. |
| **nucleo-picker** | Generic picker TUI built on nucleo. Customizable keybindings via EventSource trait. |
| **nucleo-ui** | Simple TUI wrapper around nucleo. |
| **television-fuzzy** | Television's fuzzy matching layer, extractable as crate. Based on nucleo + ratatui. Community interest in extracting as standalone widget (GitHub issue #501). |
| **skim** | Rust fzf alternative, usable as library. |
| **fuzzy-search** | BK Trees, Levenshtein, SymSpell algorithms. |

**No standalone ratatui fuzzy finder widget exists yet.** The ratatui forum has a [vote thread](https://forum.ratatui.rs/t/vote-to-get-a-fuzzy-finder-widget/198) requesting one be extracted from television. For now, nucleo + custom rendering is the approach.

### Tab Bars

Ratatui has a built-in `Tabs` widget:
- `Tabs::new(vec!["Tab1", "Tab2"]).select(idx).highlight_style(Style::default().yellow())`
- Customizable dividers, padding, styling
- No built-in key handling (you wire Tab/Shift-Tab or number keys yourself)
- **Source:** [Ratatui Tabs docs](https://ratatui.rs/examples/widgets/tabs/)

### Other Relevant Widgets

- **tui-scrollview** — Already used in Orchard for the preview pane
- **tui-widget-list** — Flat lists with arbitrary per-row widgets. No native hierarchy but could render pre-flattened tree with manual indentation.
- **ratatui built-in Table** — What Orchard currently uses. Supports row selection, styling, column constraints.

## 5. Leader Key / Chord Input Models

### Helix Editor — Trie-based keymap
**Pattern: Prefix tree with pending/matched/cancelled states**

- Keymaps stored as trie (prefix tree): sequences like `gg` and `gd` share the `g` prefix
- `KeymapResult` enum: `Matched(cmd)`, `MatchedSequence(cmds)`, `Pending(node)`, `NotFound`, `Cancelled(keys)`
- When a key is ambiguous (could be prefix of longer sequence), returns `Pending` and waits for next key
- Minor modes (nested keymaps via prefix key, e.g., `g` for "goto mode")
- Sticky modes persist across commands until Esc (e.g., match mode)
- `IndexMap` preserves insertion order for UI hint display
- **Key insight:** The trie naturally handles disambiguation. No timeouts needed — the structure itself knows whether a key could be the start of a longer sequence.
- **Source:** [Helix keymap system (DeepWiki)](https://deepwiki.com/helix-editor/helix/2.5-keymap-system-and-input-handling)

### OpenCode TUI — Leader key + command palette
**Pattern: Ctrl+x leader prefix + Ctrl+p command palette**

- `Ctrl+x` is the default leader key (avoids terminal sequence conflicts)
- Commands register reactively with automatic cleanup on dialog close
- Command palette (Ctrl+p) provides searchable, categorized command list
- Slash commands (`/connect`, `/init`) for text-based command entry
- Dialog stack for modal interactions (`dialog.replace()`, `dialog.clear()`)
- **Key insight:** Leader key + command palette is dual-access: muscle memory (leader) + discoverability (palette). Both invoke the same command registry.
- **Source:** [OpenCode TUI (DeepWiki)](https://deepwiki.com/sst/opencode/6.4-tui-input-and-commands)

### dhamidi/leader — Shell leader key launcher
**Pattern: Nested JSON keymap with breadcrumb navigation**

- Backslash activates leader mode
- Nested keymaps of arbitrary depth in `.leaderrc`
- Ctrl+B / Up / Left / Backspace for "back" navigation through levels
- Per-directory config overrides (project-specific keymaps)
- `leader bind --global d date` for programmatic binding creation
- **Key insight:** Leader key works best with visual feedback showing available next keys. Without that, it's just memorization.
- **Source:** [leader GitHub](https://github.com/dhamidi/leader)

### Yazi — 8-layer keymap architecture
**Pattern: Layer-based command routing with mode-aware filtering**

- 8 keymap layers: app, manager, tasks, input, select, help, completion, and more
- Each layer has prepend_keymap (higher priority) and append_keymap (lower priority)
- Executor dispatches commands to layers based on `cmd.layer` enum
- Input layer checks mode (Normal/Insert/Replace) and filters valid commands per mode
- **Key insight:** Layers provide clean separation without the confusion of vim-style modes. The layer is determined by context (which widget has focus), not by an invisible mode toggle.
- **Source:** [Yazi keymap docs](https://yazi-rs.github.io/docs/configuration/keymap/), [Yazi architecture (DeepWiki)](https://deepwiki.com/sxyazi/yazi/2.1-file-manager-(yazi))

### Summary: Input Model Comparison

| Model | Discoverability | Keystrokes | Mode Confusion Risk | Implementation Complexity |
|-------|----------------|------------|---------------------|--------------------------|
| **Direct keys** (lazygit) | Low (need docs) | 1 | None | Low |
| **Leader prefix** (Space+x) | Medium (shows hint on leader) | 2 | None | Medium |
| **Trie keymap** (helix) | High (pending state shows options) | Variable | Low (visual feedback) | High |
| **Modal** (vim Normal/Insert) | Low (invisible state) | 1 | High | Medium |
| **Command palette** (Ctrl+p) | High (searchable) | 3+ | None | Medium |
| **Layer-based** (yazi) | Medium (context-determined) | 1 | Low (layer = focus) | High |

## 6. Anti-Patterns

### Pogo-Stick Navigation
**Problem:** Users bounce repeatedly between a list view and detail views, losing context each time.
**Cause:** Information needed for decision-making is only available after drilling down.
**Fix:** Show differentiating data directly on the list (inline expand, badges, preview panes). Overlays for temporary exploration without losing list context.
**Relevance to Orchard:** If switching between repos requires drilling down, the user loses the dashboard overview. Expandable sub-rows (Strategy 3 from doc 001) avoid this.
**Source:** [NN/g pogo-sticking article](https://www.nngroup.com/articles/pogo-sticking/)

### Mode Confusion
**Problem:** User doesn't know what mode they're in, so keystrokes produce unexpected results.
**Cause:** Invisible state (no visual indicator), multiple modes with overlapping key meanings.
**Fix:** Visual mode indicators (cursor shape, status bar label), minimize number of modes, prefer context-determined layers over toggled modes.
**Relevance to Orchard:** The leader-key model (doc 002) avoids modes entirely. If Orchard does adopt modes (e.g., filter mode vs command mode), clear visual indicators are mandatory.
**Source:** [Vim modal editing confusion](https://medium.com/@ianjoyner/the-problem-with-vi-vim-is-they-are-modal-99536f609aea)

### Hotkey Memorization Wall
**Problem:** Tool requires learning dozens of hotkeys before being useful. Tig requires ~100 key combinations.
**Cause:** Keyboard-only interface with no discoverability mechanism.
**Fix:** Display hotkeys contextually next to their actions (Terminal.shop pattern). Command palette as escape hatch. Consistent vocabulary across views (a=add, d=delete everywhere).
**Relevance to Orchard:** Already partially addressed by showing key hints in the footer. Command palette or `?` help would complete the pattern.
**Source:** [TUI Design blog](https://jensroemer.com/writing/tui-design/)

### Losing Dashboard Overview on Drill-Down
**Problem:** Navigating into details replaces the overview, losing the "at a glance" value.
**Cause:** k9s-style push/pop view stacks, or full-screen modal dialogs.
**Fix:** Expandable inline rows, side panels, overlays that preserve the parent list context.
**Relevance to Orchard:** Core to Orchard's identity. The dashboard overview is the product. Any navigation that hides it is an anti-pattern for this tool.

### Premature Hierarchy
**Problem:** Imposing tree structure when the user's mental model is flat (or vice versa).
**Cause:** Assuming the data structure should drive the UI structure.
**Fix:** Let the primary action drive the design. If the user's goal is "switch to workspace X" and they know the name, flat fuzzy search is faster. If the goal is "see what's happening across all my projects," the hierarchical dashboard is better. Support both.
**Relevance to Orchard:** Orchard serves both use cases. The dashboard (hierarchical) is the default view. Fuzzy search (flat) should be the fast path when the user knows what they want.

### Status Indicator Overload
**Problem:** Too many colors, symbols, and badges make the list unreadable.
**Cause:** Trying to show every piece of metadata inline.
**Fix:** Progressive disclosure: show the most important status inline (active/idle), expand for details. Use consistent, limited color palette.
**Relevance to Orchard:** Already a risk with repo color + claude status + PR status + session count. The preview pane offloads detail well.

## 7. Synthesis: What This Means for Orchard

### The two primary actions and their optimal patterns:

1. **"What's happening across my projects?"** (dashboard browsing)
   - Hierarchical list with expand/collapse (repos > worktrees > sessions)
   - Inline status indicators (claude state, PR status)
   - Preview pane for detail-on-demand
   - Expandable sub-rows preserve overview context
   - This is what Orchard already does well

2. **"Switch to workspace X"** (quick switching)
   - Fuzzy search with frecency ranking
   - Type-to-filter that narrows the hierarchical view (broot-style)
   - Enter immediately switches (create-or-switch like sesh)
   - This is what Orchard needs to add (doc 002's leader-key search model)

### Recommended navigation model:

- **Default state:** Hierarchical flat table with expandable sub-rows (doc 001, Strategy 3)
- **Typing:** Immediately filters the list (doc 002, leader key model — bare keys filter, Space+key for actions)
- **Expand/collapse:** Right/Left or h/l for sub-row expansion (tmux choose-tree convention)
- **Frecency sorting:** Within each repo section, sort worktrees by recency of session activity
- **Bookmarks/pinning:** Allow pinning worktrees to top of their repo section
- **Preview pane:** Already exists; extend to follow sub-row selection (doc 001, Phase A)
- **Command palette:** `?` or Space+? to show all available actions with search
- **Key hints:** Contextual footer showing available keys for current selection type

### What NOT to do:
- Don't replace the dashboard with drill-down views (k9s pattern)
- Don't use invisible modal state (vim pattern)
- Don't require memorizing hotkeys without discoverability (tig pattern)
- Don't flatten the hierarchy entirely (sesh pattern loses Orchard's multi-repo overview value)
- Don't over-badge rows with status indicators (progressive disclosure via expand/preview instead)

## Sources

1. [sesh GitHub](https://github.com/joshmedeski/sesh)
2. [Smart tmux sessions with sesh](https://www.joshmedeski.com/posts/smart-tmux-sessions-with-sesh/)
3. [tmux-sessionx GitHub](https://github.com/omerxx/tmux-sessionx)
4. [gession GitHub](https://github.com/verte-zerg/gession)
5. [t-smart-tmux-session-manager GitHub](https://github.com/joshmedeski/t-smart-tmux-session-manager)
6. [tmux-session-wizard GitHub](https://github.com/27medkamal/tmux-session-wizard)
7. [tmux choose-tree](https://waylonwalker.com/tmux-choose-tree/)
8. [lazyworktree GitHub](https://github.com/chmouel/lazyworktree)
9. [agent-deck GitHub](https://github.com/asheshgoplani/agent-deck)
10. [Claude Launcher TUI blog](https://prakhar.codes/blog/i-built-a-tui-to-stop-losing-claude-sessions)
11. [lazygit GitHub](https://github.com/jesseduffield/lazygit)
12. [Lazygit 5 Years On](https://jesseduffield.com/Lazygit-5-Years-On/)
13. [lazygit tree view issue](https://github.com/jesseduffield/lazygit/issues/3554)
14. [k9s](https://k9scli.io/)
15. [k9s navigation guide](https://thinhdanggroup.github.io/k9s-cli/)
16. [gitui GitHub](https://github.com/gitui-org/gitui)
17. [broot GitHub](https://github.com/Canop/broot)
18. [broot navigation docs](https://dystroy.org/broot/navigation/)
19. [Atuin](https://atuin.sh/)
20. [Atuin config docs](https://docs.atuin.sh/cli/configuration/config/)
21. [tui-tree-widget crate](https://crates.io/crates/tui-tree-widget)
22. [tui-rs-tree-widget GitHub](https://github.com/EdJoPaTo/tui-rs-tree-widget)
23. [ratatui-tree-widget GitHub](https://github.com/woxjro/ratatui-tree-widget)
24. [nucleo GitHub](https://github.com/helix-editor/nucleo)
25. [nucleo-picker crate](https://crates.io/crates/nucleo-picker)
26. [television GitHub](https://github.com/alexpasmantier/television)
27. [Ratatui fuzzy finder vote](https://forum.ratatui.rs/t/vote-to-get-a-fuzzy-finder-widget/198)
28. [Ratatui Tabs docs](https://ratatui.rs/examples/widgets/tabs/)
29. [Helix keymap system (DeepWiki)](https://deepwiki.com/helix-editor/helix/2.5-keymap-system-and-input-handling)
30. [OpenCode TUI (DeepWiki)](https://deepwiki.com/sst/opencode/6.4-tui-input-and-commands)
31. [leader GitHub](https://github.com/dhamidi/leader)
32. [Yazi keymap docs](https://yazi-rs.github.io/docs/configuration/keymap/)
33. [Yazi architecture (DeepWiki)](https://deepwiki.com/sxyazi/yazi/2.1-file-manager-(yazi))
34. [NN/g pogo-sticking](https://www.nngroup.com/articles/pogo-sticking/)
35. [TUI Design blog](https://jensroemer.com/writing/tui-design/)
36. [Ranger Wikipedia](https://en.wikipedia.org/wiki/Ranger_(file_manager))
37. [Ratatui third-party widgets](https://ratatui.rs/showcase/third-party-widgets/)
38. [television-fuzzy crate](https://crates.io/crates/television-fuzzy)
