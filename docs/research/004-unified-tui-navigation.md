# 004: Unified TUI Navigation Model

**Status:** Recommendation ready
**Date:** 2026-03-31
**Question:** What unified navigation model should git-orchard's TUI adopt, addressing 6 accumulated issues (#24, #65, #90, #92, #93, recency sorting) cohesively?

## Problem Frame

```
QUESTION:  How should navigation evolve to support fast switching, hierarchy
           browsing, and dashboard overview across repos > worktrees > sessions?

CONSTRAINTS:
- Rust + ratatui, TEA architecture (Message enum, handle_event/update/render)
- Dashboard overview is the core product differentiator
- Keyboard-first (vim-style), mouse is secondary
- ~15 rows total (3 repos × 5 worktrees) — small dataset
- Single-developer project — maintenance surface matters
- No user-editable state; derive from service caches

DECISION TYPE: Mostly reversible (UI layer), but navigation model becomes the
               core UX contract. Getting it wrong means rework of all 6 issues.

SUCCESS CRITERIA:
- Reach any session in ≤3 keystrokes
- "What was I working on?" answered at a glance (top of list)
- Repo context switching is explicit, not lost in scroll
- Expandable detail doesn't break list flow
- Each change is independently shippable and valuable
```

## Open Issues Addressed

| Issue | Problem | Phase |
|-------|---------|-------|
| (new) | Recent worktrees should sort to top | Phase 1 |
| #90 | Workspace tab bar for repos | Phase 2 |
| #24 | Left/right for repos, up/down for worktrees | Phase 2 (Tab/Shift-Tab) |
| #65 | Expandable row detail view | Phase 3 |
| #93 | Expandable sub-rows for multi-pane sessions | Phase 3 |
| #92 | Leader-key input model with instant search | Phase 4 |

## Current State

- **Cursor:** Single flat `usize` over standalone sessions + worktree rows
- **Sort:** repo config order → `DisplayGroup` (Ord derive) → issue number ascending
- **Input:** `/` activates modal search, ~15 single-key action bindings
- **Repo switching:** Left/Right cycles repo filter
- **Sessions:** Multiple sessions aggregated into single CLAUDE column; only `sessions.first()` used for Enter/preview
- **No expand/collapse, no sub-cursor, no tree widget**
- **Mouse:** Full click/double-click/scroll support

Key files: `src/derive.rs:204-237` (sort), `src/tui/mod.rs:555-581` (keybindings), `src/tui/list.rs:903-1109` (rendering), `src/navigation.rs` (digit-jump only).

## Prior Research

- **001-tui-pane-navigation.md**: Recommends expandable sub-rows (Strategy 3) with Right/Left
- **002-tui-search-input-model.md**: Recommends leader-key prefix (bare keys filter, Space+key for actions)
- **003-tui-navigation-models.md**: Comprehensive landscape survey — every successful session manager converges on fuzzy search + frecency ranking

## Strategies Evaluated

### A. Recency Sort Only
Add `last_activity_ts` to `WorktreeRow` from tmux `session_activity`. Sort within display groups by descending recency. ~50 lines.

| | |
|---|---|
| **Strengths** | Ships today, solves the stated ask |
| **Weaknesses** | Addresses 1 of 6 issues. Creates sort instability (rows move on every refresh) |
| **Effort** | S |
| **Risk** | Very low |

### B. Phased Layered Navigation (recommended)
Four independently-shippable phases, reorderable. Each leaves the system in a valid state.

| | |
|---|---|
| **Strengths** | Addresses all 6 issues. Each phase is valuable alone. Key bindings compose without conflicts. Design work already done in docs 001-003. |
| **Weaknesses** | Total scope is L. Phase 3 (sub-rows) has cursor math complexity. Phases can rot if not shipped continuously. |
| **Effort** | L total (S+S+M+M per phase) |
| **Risk** | Medium — mitigated by phase independence |

### C. Fuzzy Popup Overlay (sesh-style)
Ctrl+Space opens frecency-ranked fuzzy finder overlay. Dashboard untouched underneath.

| | |
|---|---|
| **Strengths** | Additive (zero risk to dashboard). Sub-second switching. Every session manager validates this pattern. |
| **Weaknesses** | Doesn't address sub-rows (#65, #93) or tab bar (#90). Creates a second navigation paradigm. Popup is a mode (exits dashboard context). |
| **Effort** | M |
| **Risk** | Low |

## Quorum Debate

Three agents argued A, B, and C independently.

**A's best argument:** "The user asked for 'recent at top.' Ship that. The 6 issues aren't a mandate — they're an accumulated backlog. Research docs are sunk cost."

**B's best argument:** "The 6 issues are one design problem filed as 6 tickets. Phase 1 is a strict superset of A. The phases can be reordered (1→2→3→4 or 1→4→3→2) and you can stop at any point with a coherent system. The key binding composition is already solved in the research docs."

**C's best argument:** "Every successful session manager converges on fuzzy search. The research literally says 'the fastest navigation is no navigation.' C is additive — zero risk to existing dashboard. It doesn't close the door on B's sub-rows later."

**What was decisive:** B's agent demonstrated that Phase 1 subsumes A (strict superset — frecency ≥ recency), and that C's fuzzy search is Phase 4 of B delivered as an in-place filter rather than a popup (preserving dashboard context instead of occluding it). The reordering argument (1→2→3→4 not mandatory) dissolved the "too much scope" objection.

**What C got right:** The popup-as-fast-path idea shouldn't be discarded. It can be Phase 4's *implementation choice* — whether search narrows inline or opens a popup is a rendering decision, not an architectural one. The data model (frecency scoring, nucleo matching) is identical either way.

## Recommendation: Phased Layered Navigation (B)

Four phases, delivery order **1 → 2 → 3 → 4** (but 1→2 can swap with 1→3 if sub-rows feel more urgent than tab bar):

### Phase 1: Frecency Sort (S — days)

Add `last_activity_ts: Option<i64>` to `WorktreeRow`, sourced from tmux `session_activity` (Unix epoch). Change `derive_all_repos` sort comparator: within each display group, sort by descending `last_activity_ts` (nulls last), breaking ties by issue number.

**Ships:** Recency issue closed. Most-recently-touched worktrees float to top within their display group. No key binding changes.

**Key decisions:**
- Pure recency (not frecency) for Phase 1. Frequency weighting adds complexity for marginal benefit at 15-row scale. Can upgrade later if needed.
- Null timestamp (no active session) sorts after all timestamped rows within the same display group.
- `RepoMain` rows remain pinned first regardless of recency (display group order preserved).

### Phase 2: Tab Bar for Repos (S — days)

Replace Left/Right repo cycling with a ratatui `Tabs` widget at the top. Tab/Shift-Tab (or number keys) to switch repo filter. Left/Right keys are now freed.

**Ships:** #90 (tab bar) and #24 (repo switching) closed. Left/Right available for Phase 3.

**Key binding changes:**
- `Left`/`Right` → unbound (freed for Phase 3)
- `Tab`/`Shift-Tab` → repo cycling (was: unused)
- Number keys in tab bar context → jump to repo by position

### Phase 3: Expandable Sub-Rows (M — ~1 week)

Right/l on a worktree row expands indented sub-rows for its sessions (and optionally panes). Left/h collapses. Track `expanded: HashSet<String>` (keyed by worktree path) in `App`.

**Ships:** #65 (expandable detail) and #93 (session sub-rows) closed.

**Key decisions:**
- Sub-rows have different column content: session name | host | claude status | cost
- Cursor can land on sub-rows; Enter on a sub-row attaches that specific session
- Preview pane follows sub-row selection
- Background refresh preserves expansion state (match by worktree path)
- Sort changes (Phase 1) collapse expanded rows to avoid disorienting jumps

### Phase 4: Instant Search (M — ~1 week)

Leader-key model from doc 002: bare keys filter immediately (nucleo fuzzy matching), Space+key for actions. `/` as alternative activation for muscle memory.

**Ships:** #92 (leader-key search) closed. Navigation speed reaches ≤3 keystrokes for any target.

**Key binding changes:**
- Bare printable keys → filter input (was: action shortcuts)
- `Space+o` → open PR (was: `o`)
- `Space+q` → quit (was: `q`)
- `Escape` → clear filter (was: quit, moved to Space+q)
- `/` → also activates filter (backwards compat)

**Implementation choice:** Whether filtering narrows the existing table inline (broot-style) or opens a popup overlay (sesh-style) is a rendering decision. Both use the same frecency + nucleo matching. Recommend inline first (preserves dashboard), with popup as a future option if inline feels cramped.

### Key Binding Migration Map

```
Key        Today           After Ph.2      After Ph.3      After Ph.4
───────────────────────────────────────────────────────────────────────
Left       PrevRepo        (freed)         Collapse row    Collapse row
Right      NextRepo        (freed)         Expand row      Expand row
Tab        (unused)        NextRepo        NextRepo        NextRepo
Shift-Tab  (unused)        PrevRepo        PrevRepo        PrevRepo
/          StartSearch     StartSearch     StartSearch     StartFilter
o          OpenPR          OpenPR          OpenPR          Space+o
q          Quit            Quit            Quit            Space+q
a-z        (varies)        (varies)        (varies)        Filter input
Space      (unused)        (unused)        (unused)        Leader prefix
```

## Conditions Where Other Approaches Win

- **A wins if:** bandwidth is so constrained that even Phase 2 can't ship within a month. Ship recency sort and stop.
- **C (popup) wins if:** user research shows the dashboard is primarily a monitoring tool, not a navigation surface — users prefer modal search to inline filtering. In that case, build the popup instead of Phase 4's inline filter.
- **Abort Phase 3 if:** session sub-rows are rarely used in practice. Phase 1+2+4 is a coherent system without them.

## Next Steps

1. Create GitHub issues for Phases 1-4, linked as sub-issues to a parent "Unified Navigation" epic
2. `/plan` Phase 1 (frecency sort) — smallest, most immediately valuable
3. Ship Phase 1, then decide whether Phase 2 (tab bar) or Phase 3 (sub-rows) is more urgent

## Sources

- [001-tui-pane-navigation.md](001-tui-pane-navigation.md) — expandable sub-rows recommendation
- [002-tui-search-input-model.md](002-tui-search-input-model.md) — leader-key prefix recommendation
- [003-tui-navigation-models.md](003-tui-navigation-models.md) — landscape survey (38 sources)
