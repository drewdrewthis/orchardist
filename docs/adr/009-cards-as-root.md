# ADR-009: Cards as the top-level unit of OrchardState

## Status

Proposed

## Date

2026-04-23

## Context

`OrchardState` is currently a two-axis shape:

- `repos: Vec<RepoState>` → `worktrees: Vec<WorktreeState>` → `sessions: Vec<SessionState>`
- `standalone_sessions: Vec<StandaloneSessionRow>` — sessions not tied to any worktree

This shape has served orchard well through the TUI-and-JSON era: the primary use case was "what's happening under each repo," so repo-first nesting was the natural tree. Sessions without a worktree were a bolt-on (`standalone_sessions`) because they arrived late as a real concept — see ADR-007.

Three pressures have surfaced together that this shape cannot absorb cleanly:

1. **Conversations are first-class work.** Claude sessions that are not tied to an issue or worktree — the orchardist itself, research sessions, sister forks, "let me think about this" threads — are load-bearing units of work. Burying them under `standalone_sessions` signals they are second-class. In practice, half of the orchardist's and Drew's active cognition lives in these threads.

2. **Issue/PR/worktree/session are a graph, not a tree.** The current tree pretends there is always a worktree between a repo and a session. Reality: a card can be *just an issue* (unstarted), *just a PR* (post-merge review), *just a session* (research not yet attached), or any subset. A sub-issue may have no worktree yet, but is still a unit of work the orchardist must see and schedule. Forcing the tree means the TUI and JSON consumers invent ad-hoc "where does this go?" rules.

3. **Auto-flow kanban.** A card-first model makes auto-flow columns (Backlog / InProgress / Waiting / InReview / Done / AllSessions) a pure function of card link state and session telemetry, recomputed each reconcile. The current shape requires per-consumer classification logic; every consumer reinvents it.

Simultaneously, orchard already has the machinery a card-first model needs:

- **Two-tier issue linking** exists in `crates/orchard/src/join.rs:72-77`: prefer PR `closingIssuesReferences` (authoritative), fall back to branch-name regex. Cards can adopt this verbatim.
- **Session identity** is the remaining hard problem. Today sessions are matched to worktrees by live pane `cwd`, which drifts when Claude `cd`s during work — creating and destroying "cards" in any naive rollup. The fix is to pin session identity to the **cwd-at-start** recorded in the first user message of the session's `.jsonl` transcript, not the live pane cwd.
- **Consolidated state.json.** `$TMPDIR/orchard-claude-*.json` hook files are the raw per-session telemetry; today each consumer reads them individually. A single aggregator + fswatch writer to one hot `state.json` eliminates race conditions across readers and fixes the top-level `tmuxSessions` inventory gap tracked in or#341 structurally, not by patching it.
- **Federated discovery (ADR-008).** Cards carry a `host` field so federation continues to work; a remote card is just a card with `host: Some(remote)` and its links resolved against the remote's snapshot.

## Decision

**`Card` becomes the top-level unit of `OrchardState`.** Everything else — issue, PR, worktree, session — becomes an optional link on a card.

### Shape

`OrchardState` grows a new field:

```rust
pub struct OrchardState {
    pub cards: Vec<Card>,          // new: top-level unit of work
    pub repos: Vec<RepoState>,     // retained: container for repo meta + default branch + CI
    pub hosts: HashMap<String, HostState>,
    // standalone_sessions removed — sessions appear as cards with only a session link
}

pub struct Card {
    pub id: CardId,                // stable across reconciles
    pub host: Option<String>,      // None = local; Some = federated
    pub title: String,             // derived from best-available link
    pub issue: Option<IssueInfo>,
    pub pr: Option<PrState>,
    pub worktree: Option<WorktreeRef>,  // path + branch; full state on RepoState
    pub sessions: Vec<SessionState>,    // zero, one, or many
    pub column: Column,            // auto-derived, see below
    pub last_activity_at: Option<u64>,
}

pub enum Column {
    Backlog,       // issue-only, no worktree, no session
    InProgress,    // has active (working/input) session OR recent commit
    Waiting,       // session idle, PR open, CI running, OR blocked-by another card
    InReview,      // PR open, CI green, reviewers requested
    Done,          // PR merged OR issue closed within N days
    AllSessions,   // catch-all for sessions with no other link
}
```

`RepoState` retains `repos[].worktrees` for the physical worktree inventory (needed for git ops, path resolution, layout). Cards *reference* worktrees by path; they do not duplicate the worktree struct.

### Projection, not replacement

Cards are added **additively**. `repos[].worktrees[]` stays as-is. Cards are a projection computed by the reconciler from existing entities:

- Every open issue without a linked PR → one card (Backlog)
- Every PR → one card, merged with its linked issue (via `closingIssuesReferences`, fallback to branch regex — the existing join in `join.rs:72-77`)
- Every worktree whose branch doesn't resolve to either → one card
- Every session whose `cwd-at-start` doesn't belong to any of the above → one card (AllSessions)

This lets the existing TUI and JSON consumers keep working during migration and lets new consumers (card kanban TUI, kanban-code adapter, federated card dashboards) read from `cards` directly.

### Session identity: `cwd-at-start`

A session's identity is its name + the `cwd` recorded in the first user message of its `.jsonl` transcript. **Not** the live tmux pane `cwd`, which drifts when Claude `cd`s mid-session and causes cards to be created/destroyed spuriously.

Resolution rule for session → card linkage:

1. Read `~/.claude/projects/<slug>/<uuid>.jsonl`, first user message, `cwd` field.
2. Match that path against repo worktrees. If it resolves under a worktree, link the session to that worktree's card.
3. If it doesn't resolve (orchardist, research sessions, etc.), the session stands alone as its own card in `AllSessions`.

### Consolidated `state.json` writer

A single process (existing orchard daemon or a new thin writer — scope of sub-issue #3) owns writing `~/.cache/orchard/state.json`:

- Watches `$TMPDIR/orchard-claude-*.json` with fswatch for per-session hook updates.
- Runs the gh/cache refresh pipeline in the background (already exists as `refresh_parallel.rs`).
- Produces a single hot `state.json` that all readers (TUI, `orchard-tui --json`, sister sessions, kanban-code) consume.

This is a **writer + readers over a hot file**, not a daemon with an RPC interface. Atomic tmp→rename write on each reconcile. Readers always see a consistent snapshot. Fixes or#341 structurally: there is no "per-worktree sessions" vs. "top-level sessions" divergence because both are projections of the same hot file.

### Auto-flow columns

Column is a pure function of card links + session telemetry, recomputed each reconcile:

| Condition | Column |
|-----------|--------|
| Issue open, no PR, no worktree, no session | Backlog |
| Any session in `working` or `input` state | InProgress |
| Worktree exists with recent commits, no session | InProgress |
| PR open, CI pending, no active session | Waiting |
| Card's issue has unresolved `blocked_by` | Waiting |
| PR open, CI green, reviewers requested or `ready-for-review` | InReview |
| PR merged within N days OR issue closed within N days | Done |
| Session with no issue/PR/worktree link | AllSessions |

(These are the initial rules; exact thresholds are implementation details for sub-issue #4.)

### Federated cards

Cards carry `host: Option<String>`. A remote orchard's `--json` already carries issue/PR/session data (ADR-008); federating cards means projecting the remote's entities into cards locally, same as local. The `merge_remote.rs` seam is where this happens.

Out of scope for this ADR: cross-host card linkage (e.g., an issue on one host with a worktree on another). First release assumes host-local links.

## Consequences

### Positive

- **Non-issue-tied conversations are first-class.** The orchardist, sister forks, research threads — all appear as cards in `AllSessions`, not as second-class `standalone_sessions`.
- **Graph, not tree.** Consumers iterate cards; links are optional. No more "where does this go?" conditional logic at call sites.
- **Auto-flow kanban is trivial.** Column is a derived function, not a hand-maintained state. Every reconcile recomputes it.
- **or#341 fixed structurally.** Consolidated `state.json` writer eliminates the top-level-vs-worktree session inventory divergence; there is only one inventory, projected two ways.
- **Session identity is stable.** `cwd-at-start` pin means cards don't appear/disappear when Claude `cd`s mid-session.
- **Federated cards ride ADR-008.** No new wire-protocol work; cards project from existing `JsonOutput`.
- **Flagship for or#258.** "Orchard v2: card-first model" is the visible shape change that delivers the "orchardist as first-class OSS citizen" pitch — there's something to point at.
- **kanban-code adapter path.** A card shape is trivially consumable by kanban-code; the adapter becomes an optional shim (sub-issue #8).

### Negative

- **Two top-level axes during migration.** `cards` and `repos` both exist. Consumers must pick one; for one to two releases both are authoritative in different ways (repos for git ops, cards for work flow). Documented migration guide per sub-issue #1.
- **Card identity stability is new surface.** `CardId` must be stable across reconciles so the TUI and downstream kanban tools don't flicker. Implementation detail for sub-issue #2.
- **Projection cost.** Reconciler now does card derivation on top of existing joins. On large states this is a concrete CPU cost, though still O(n) in worktrees + sessions + issues.
- **Column rules are opinionated.** Teams with different workflows may disagree with the Backlog/Waiting/InReview thresholds. Sub-issue #4 must expose hooks or config.
- **`AllSessions` is a catch-all.** Sessions that *should* link to a card but fail the resolution (path typo, stale jsonl, orchardist itself) end up here. Needs a diagnostic (`orchard-tui doctor` hook) to surface "unlinked sessions with plausible candidates."

### Neutral

- **Wire schema grows.** `JsonOutput` adds a `cards` field. Backward-compatible (old clients ignore it) but the schema versioning contract in ADR-008 now carries one more field.
- **TUI re-design, not re-write.** The existing worktree-sort code (`sort_key`) becomes one of several card-projection strategies. The card TUI is additive (sub-issue #5), not a replacement; the worktree view stays until adoption is proven.

## Alternatives considered

### A. Keep repo-first, add `conversations: Vec<Session>` at top level

Rejected. This is what `standalone_sessions` already is — and it's the signal that inspired this ADR. Renaming the field doesn't fix the underlying two-axis confusion; consumers still have to union `repos[].worktrees[].sessions[]` with a second list. Auto-flow columns still require per-consumer logic.

### B. Replace `repos[].worktrees[]` with `cards[]` entirely

Rejected for this ADR. `repos` is still the right container for git meta (default branch, main CI, layout). Worktrees as physical objects (paths, ahead/behind, layout) belong under `repos`. Only the *work-unit* axis needs to be cards. Keeping `repos[].worktrees[]` means git-ops code paths don't change.

### C. Do nothing; add `tmuxSessions` union for or#341 only

Rejected as a scope decision. or#341 is a symptom: the deeper issue is that the data model has no place for first-class conversations. A union patch for `tmuxSessions` fixes one reader (`orchard-tui --json`) but leaves the TUI and auto-flow columns still ad-hoc. If we're editing the top-level state, we take the projection upgrade now.

### D. Full card store (persistent DB, not derived)

Rejected. Cards are cheap to derive and stable across reconciles as long as link shapes are stable. A persistent store introduces migration pain, divergence between the store and reality, and a sync loop. Pure projection is the KISS answer.

### E. Separate "orchardist card store" service

Rejected. Another daemon, another lifecycle, another failure mode. The existing orchard binary is the right home. See ADR-008 on why orchard avoids new daemons.

## Related

- ADR-004 — unified data model (the shape this ADR extends)
- ADR-007 — session data model (the `EnrichedSession` / `standalone_sessions` design that inspired this refactor)
- ADR-008 — federated discovery (cards inherit `host` and the `JsonOutput` schema contract)
- Issue #258 — Ship the Orchardist OSS (cards are the visible shape change delivering this vision)
- Issue #341 — `tmuxSessions` inventory gap (fixed structurally by the consolidated `state.json` writer)

## Structural invariants

The following invariants are load-bearing for the decisions above. Sub-issues that implement them should add machine-checked tests:

- **Cards are derived, never stored.** Every `Card` in `OrchardState.cards` is the output of the reconciler's projection. There is no write path that constructs a `Card` outside the reconciler.
- **Session identity is `cwd-at-start`.** Session-to-card resolution MUST use the first user message's `cwd` from the `.jsonl` transcript, not live pane `cwd`. A test pins this: a session whose pane has `cd`d away from its start cwd still resolves to the same card.
- **One writer for `state.json`.** Only the consolidated writer writes `~/.cache/orchard/state.json`. Multiple writers are a race condition. A test pins this at the filesystem layer (flock or equivalent).
- **`repos[].worktrees[]` is physical, `cards[]` is logical.** Card worktree links reference paths; they do not duplicate the worktree struct. Refactors that try to stuff card metadata back into `WorktreeState` should fail review.
