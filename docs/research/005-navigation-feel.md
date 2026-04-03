# 005: How Should Navigation Feel?

**Date:** 2026-03-31
**Status:** Complete
**Type:** UX exploration — not a roadmap

## The Question

Orchard keeps accumulating navigation-adjacent issues. "Recent at top." Tab bars. Leader keys. Expandable rows. These aren't independent feature requests — they're symptoms of one underlying friction. What is that friction, and what does the research say about resolving it?

## The Friction: Orchard Tries to Be Two Things at Once

Orchard serves two purposes:

1. **Monitoring**: "What's happening across my work?" — scan status, see what Claude is doing, notice which PRs need attention
2. **Switching**: "Take me to the auth refactor" — find a target, go there, get back to work

These are cognitively different tasks. Monitoring benefits from **information density** — show me everything at once, let me scan. Switching benefits from **speed and minimal cognitive load** — show me the minimum I need to pick, then get out of my way.

The current TUI treats both identically: a flat list of 15 rows with rich metadata, navigated by j/k. This serves monitoring reasonably well (you can see everything) but makes switching feel heavy (you have to visually process 15 information-dense rows to find the one you want).

This is the friction. Every navigation issue the user has filed is an attempt to make switching faster without losing the monitoring value.

---

## What the Science Says

### 1. The user doesn't think in repos > worktrees > sessions

When a developer thinks "I need to get back to that auth refactor," their recall path is **associative**, not hierarchical. Parnin & Rugaber's study of 10,000 programming sessions found that developers use **multiple modalities** to recall suspended work: the name, what they were doing, what code they were editing, what error they saw, and where it was on screen. They don't think "repo X, then worktree Y, then session Z" — they think "that auth thing" and reach for the most salient cue.

Altmann & Trafton's memory-for-goals model explains why: suspended task goals decay in memory and must be **primed by environmental cues**. Trafton's follow-up found that **blatant cues** work but subtle ones don't — a red arrow pointing at the last action helped resumption, while a small highlight didn't.

**Implication:** The hierarchy (repo > worktree > session) is an implementation detail, not a user mental model. Navigation should match how users recall — by task identity, by recency, by visual salience — not by data structure.

### 2. Spatial stability beats recency sorting

Cockburn's longitudinal research on window switching found that **spatially stable layouts are significantly faster** than recency-ordered layouts. Users build spatial memory ("the auth worktree is always third in the second group") through repeated exposure. Recency sorting destroys this memory on every refresh.

Sears & Shneiderman's split-menu research found the compromise: place the 2-3 most frequently used items in a **prominent position** while keeping all other items in stable order. Lee & Yoon quantified this — the split approach wins when 31-89% of selections target a small item set.

NNGroup's synthesis is direct: "Adaptive interfaces that rearrange elements destroy spatial memory." The one exception: **duplicating** items (e.g., a "Recent" section at the top while maintaining the original position below).

**Implication:** Don't re-sort the list by recency. Instead, **visually highlight** the most recent item within its stable position. If a "recent" section is needed, it should be additive — a summary at the top that doesn't disturb the spatial layout below.

### 3. Your groups are already near-optimal for scanning

Cowan's revised working memory research puts the true capacity at **3-5 chunks** (not Miller's famous 7). Orchard's 3 groups of ~5 items maps almost perfectly to this limit: users can hold 3 group identities in working memory, and each group is at the upper edge of comfortable capacity.

Nielsen's eye-tracking research found that with strong visual hierarchy (distinct headers, consistent row formatting), users develop a **layer-cake scanning pattern** — they scan group headers first, identify the relevant group, then scan within it. This is the most efficient pattern for Orchard's structure.

Gestalt research confirms that **proximity is the strongest grouping signal** — stronger than color, stronger than shape. The whitespace between your groups does more for scanability than any amount of color coding.

**Implication:** The current group structure is cognitively sound. Don't flatten it (you'd lose the chunking benefit) and don't add more hierarchy levels (Larson & Czerwinski showed that depth harms performance). Strengthen the groups visually — make headers more distinct, ensure clear spacing — rather than restructuring them.

### 4. Color is the only preattentive channel that matters in a terminal

Healey's research on preattentive processing established that color/hue is processed in under 200ms without conscious effort. A single red item among blue items "pops out" instantly regardless of list size. In a monospace terminal with no font weight or size variation, **color is the primary preattentive channel available**.

But there's a constraint: overloading color (too many categories, too many hues) destroys the pop-out effect. When everything is colorful, nothing stands out.

**Implication:** Reserve color for the single most important distinction in each context. For monitoring, that's status (what needs attention). For switching, that's "where was I just now" (the most recent active worktree). One bold visual signal per row, not five.

### 5. Switching should be under 100ms to feel like direct manipulation

Nielsen, Card, and Seow established three response time thresholds:
- **< 100ms**: Feels instantaneous. Direct manipulation — "I moved it."
- **100ms - 1s**: Flow preserved but directness lost — "I told it to move."
- **> 1s**: Attention drifts. This is where tmux choose-tree's preview lag lands, and why it feels heavy.

Gloria Mark's interruption research found it takes **23 minutes** to return to the original task after a full interruption. The design goal: make workspace switching so fast it registers as a micro-interruption (seconds), not a context switch (minutes).

**Implication:** The round-trip (invoke orchard → identify target → switch → arrive in workspace) must feel like a single gesture. Every interaction step, every visual scan, every animation adds perceived weight. The fastest tools (Spotlight, Raycast, sesh+fzf) compress this to: invoke → type 2-3 chars → Enter → gone.

### 6. Persistent lists create psychological debt

Tab hoarding research shows that every visible item in a persistent list represents a micro-decision ("Is this the one? No. Next.") and a sense of obligation ("I should deal with that"). Arc's Spaces feel calming because switching contexts **hides** the other contexts entirely. Chrome tab groups feel less effective because collapsed groups are still visible, still psychologically present.

Leroy's attention-residue research adds nuance: the density itself isn't the problem — it's the **processing overhead**. Showing branch names, PR numbers, status indicators, and Claude state on each row creates 15 × N data points that the visual system must parse even when the user "knows" what they want.

**Implication:** Orchard's monitoring view (all 15 rows with full metadata) is valuable for monitoring. But using it for switching forces the user to process the full monitoring view just to pick a target. The tool should offer a way to switch that **doesn't require scanning the full dashboard**.

---

## The Core Tension, Restated

The research converges on a fundamental tension in Orchard's design:

**Monitoring wants stability and density.** Keep items in stable positions so users build spatial memory. Show rich metadata so status is visible at a glance. Use color-coded groups and distinct headers for layer-cake scanning.

**Switching wants speed and minimalism.** Compress intent-to-action to 2-3 keystrokes. Show the minimum information needed to identify a target. Get out of the way immediately.

Every great tool that serves both purposes resolves this tension the same way: **two interaction modes, one data model.**

- VS Code: the file explorer is the monitoring view (persistent, spatial, rich). Cmd+P is the switching view (ephemeral, fuzzy, minimal).
- Arc: the sidebar is monitoring (tabs, status, spatial). Cmd+T is switching (search, ephemeral).
- i3/Aerospace: the workspace layout is monitoring (spatial, persistent). Super+N is switching (muscle memory, instant).
- Grafana: the dashboard is monitoring (panels, graphs, dense). The search bar is switching (type a name, go there).

None of these tools try to make the monitoring view also be fast for switching. They accept that these are different cognitive tasks and give each its own interface.

---

## What This Means for Orchard

### The dashboard is not the problem

The current list view — 3 groups, ~5 items each, with status indicators and display group headers — is a good monitoring interface. The group structure matches working memory limits. The visual hierarchy supports layer-cake scanning. The display groups surface what needs attention. Keep it.

### The dashboard is not the solution to switching

Using j/k to navigate a 15-row information-dense list is not how a power user should switch workspaces. It's O(n) visual scanning when the task calls for O(1) recall-based selection.

### What "recent at top" actually means

When the user says "have recent at the top," they're not asking for a sort order change. They're asking: **"when I open orchard, the thing I need should be immediately obvious without scanning."** Recency is one signal for "the thing I need." But the research says re-sorting by recency destroys spatial memory and makes monitoring worse.

The right response isn't a sort. It's a **blatant visual cue** (Trafton's term) on the most recently active workspace — visible without scanning, without disrupting the spatial layout.

### The missing piece: an ephemeral switching mode

Orchard needs something that plays the role of Cmd+P / Raycast / sesh+fzf — a fast, ephemeral way to say "take me to X" without processing the full dashboard.

This could be:
- Type-to-filter that collapses the list to matches (broot-style)
- A fuzzy popup overlay (sesh-style)
- Direct keybindings to the top N workspaces (i3-style — the digit keys already do this)

The specific mechanism matters less than the principle: **switching should bypass the dashboard, not navigate through it.**

---

## Design Principles

These aren't features. They're criteria for evaluating any future navigation decision:

1. **The dashboard is for monitoring; don't optimize it for switching.** Keep the list stable, dense, and spatially consistent. It's a status display, not a picker.

2. **Switching bypasses the dashboard.** The fastest path to a workspace should not require scanning 15 rows. Provide an O(1) or O(log n) path: fuzzy search, digit keys, or "last" toggle.

3. **Spatial stability over recency sorting.** Items stay in predictable positions. Recency is surfaced through visual cues (highlight, indicator), not reordering.

4. **One bold signal per row.** Color is the only preattentive channel in a terminal. Use it for the single most important distinction. Don't overload it.

5. **Blatant, not subtle.** The most recent / most urgent workspace should be visually loud. Research shows subtle cues are no better than no cues at all.

6. **3 groups of ~5 is the right structure.** This matches working memory limits. Don't flatten it and don't deepen it.

7. **Sub-100ms for switching.** The round-trip from intent to arrival must feel like a gesture, not a task. Every step of UI interaction adds perceived weight.

8. **Hierarchy is implementation, not UX.** Users think "that auth thing," not "repo X, worktree Y, session Z." Navigation should match recall patterns: by name, by recency, by task identity.

---

## Sources

### Cognitive Science
- Altmann & Trafton, [Memory for Goals](https://onlinelibrary.wiley.com/doi/abs/10.1207/s15516709cog2601_2) (2002)
- Trafton, ["Huh, What Was I Doing?"](https://journals.sagepub.com/doi/abs/10.1177/154193120504900354) (2005) — blatant cues
- Leroy, ["Why Is It So Hard to Do My Work?"](https://www.sciencedirect.com/science/article/abs/pii/S0749597809000399) (2009) — attention residue
- Cowan, [The Magical Number 4](https://pmc.ncbi.nlm.nih.gov/articles/PMC2864034/) (2001) — working memory capacity
- Mark, [The Cost of Interrupted Work](https://ics.uci.edu/~gmark/chi08-mark.pdf) (2008) — 23 minutes to resume

### Developer Cognition
- Parnin & Rugaber, [Resumption Strategies](https://link.springer.com/article/10.1007/s11219-010-9104-9) (2011) — 10k programming sessions
- LaToza & Venolia, [Maintaining Mental Models](https://www.microsoft.com/en-us/research/publication/maintaining-mental-models-a-study-of-developer-work-habits/) (2006)

### Spatial Memory & Interface Design
- Cockburn, [Improving Window Switching Interfaces](https://link.springer.com/chapter/10.1007/978-3-642-03658-3_25) (2009) — spatial stability
- Tak et al., [SCOTZ](https://link.springer.com/chapter/10.1007/978-3-642-23774-4_27) (2011) — spatially consistent zones
- Sears & Shneiderman, [Split Menus](https://www.researchgate.net/publication/2365205_Split_menus_Effectively_using_selection_frequency_to_organize_menus) (1994)
- NNGroup, [Spatial Memory in UX](https://www.nngroup.com/articles/spatial-memory/)

### Visual Processing & Scanning
- Healey, [Perception in Visualization](https://www.csc2.ncsu.edu/faculty/healey/PP/) — preattentive processing
- NNGroup, [F-Pattern Revisited](https://www.nngroup.com/articles/f-shaped-pattern-reading-web-content/) — layer-cake scanning
- Pirolli & Card, [Information Foraging](https://psycnet.apa.org/record/1999-11924-001) (1999)
- NNGroup, [Proximity Principle](https://www.nngroup.com/articles/gestalt-proximity/)

### Information Architecture
- Shneiderman, [The Eyes Have It](https://www.cs.umd.edu/~ben/papers/Shneiderman1996eyes.pdf) (1996) — overview, zoom, filter, details
- Larson & Czerwinski, [Web Page Design](https://www.microsoft.com/en-us/research/wp-content/uploads/2002/01/HFandtheWebChapter.pdf) (1998) — shallow beats deep
- Beaudouin-Lafon, [Instrumental Interaction](https://dl.acm.org/doi/10.1145/332040.332473) (2000)

### Response Time & Flow
- Nielsen, [Response Time Limits](https://www.nngroup.com/articles/response-times-3-important-limits/) (1993)
- Hick's Law, [Laws of UX](https://lawsofux.com/hicks-law/)

### Tool UX Analysis
- [Alfred vs Raycast](https://joshcollinsworth.com/blog/alfred-raycast) — latency sensitivity
- [Command Palette Patterns](https://medium.com/design-bootcamp/command-palette-ux-patterns-1-d6b6e68f30c1) — Suska
- [Arc Browser UX](https://medium.com/design-bootcamp/arc-browser-rethinking-the-web-through-a-designers-lens-f3922ef2133e) — Spaces model
- [Tab Hoarding Psychology](https://maxfoc.us/blog/how-to-overcome-tab-hoarding/) — persistent list debt
- [Dashboard Cognitive Guidelines](https://uxmag.com/articles/four-cognitive-design-guidelines-for-effective-information-dashboards)
- [Sesh](https://www.joshmedeski.com/posts/smart-tmux-sessions-with-sesh/) — session switching UX
- [tmux choose-tree](https://waylonwalker.com/tmux-choose-tree/) — hierarchy navigation
