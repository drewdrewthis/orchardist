# ADR-007: Session data model

## Status

Accepted

## Date

2026-03-29

## Context

Orchard needs to represent tmux sessions enriched with Claude Code state information. Sessions can be local or on remote hosts via SSH.

The previous approach embedded session data directly in worktree types. This worked for the common case — a session attached to a worktree — but made standalone sessions impossible to represent. A "shepherd" session that orchestrates work across repos has no worktree to attach to, and neither do monitoring or utility sessions.

Additional complication: Claude enrichment data (status, cost, context window usage, model) comes from hook state files written to `/tmp/`, not from tmux itself. The enrichment layer must be kept separate from the transport layer to avoid coupling tmux discovery to Claude-specific logic.

## Decision

Define a layered session type system in `src/session.rs`.

### Type hierarchy

**`TmuxSessionInfo`** — pure tmux data only:
- `host: Host` (enum: `Local` | `Remote(String)`)
- `name: String`
- `status: SessionStatus` (enum: `Running { attached: bool }` | `Dead`)

**`ClaudeSessionInfo`** — Claude Code enrichment data:
- `status: ClaudeState` (working, idle, waiting for input, etc.)
- `cost_usd: Option<f64>`
- `context_window_pct: Option<f64>`
- `model: Option<String>`

**`EnrichedSession`** — composition of tmux + optional Claude data:
- `tmux: TmuxSessionInfo`
- `claude: Option<ClaudeSessionInfo>`

**`StandaloneConfig`** — configuration for non-worktree sessions, defined in global config under the `tmux_sessions` array:
- `name: String`
- `command: String`
- `cwd: String`
- `start_on_launch: bool`

**`StandaloneSessionRow`** — a standalone session matched to its config:
- `session: EnrichedSession`
- `config: StandaloneConfig`

**`ListEntry`** — the TUI list's display unit:
- `Worktree(WorktreeRow)` — a worktree with its enriched sessions
- `Standalone(StandaloneSessionRow)` — a standalone session

### Join behavior

The composition pattern (`EnrichedSession` = `TmuxSessionInfo` + `Option<ClaudeSessionInfo>`) keeps the tmux discovery layer ignorant of Claude. Claude enrichment is joined at `build_state` time by matching tmux session names to hook state files in `/tmp/orchard-claude-{session}.json`.

`OrchardState.standalone_sessions: Vec<StandaloneSessionRow>` holds sessions that don't belong to any worktree. These are displayed in their own section of the TUI, below the worktree list.

`ListEntry` gives the TUI a single type to iterate over for rendering, handling both worktrees and standalone sessions uniformly without conditional dispatch at the call site.

## Consequences

**Positive:**
- Clean separation between tmux transport layer and Claude enrichment — neither knows about the other until join time.
- Standalone sessions (shepherd, monitoring, utilities) are first-class citizens, not afterthoughts.
- `ListEntry` enum gives the TUI a uniform iteration type for both worktrees and standalone sessions.
- Adding new enrichment sources (e.g. a different AI assistant) means adding an optional field on `EnrichedSession`, not restructuring the hierarchy.

**Negative:**
- More types to navigate — session representation spans `session.rs`, `orchard_state.rs` (`SessionState`, `ClaudeEnrichment`), and `sources/tmux.rs`.
- Two parallel session type hierarchies exist: `session.rs` types for discovery and display, and `orchard_state.rs` types for the unified state model. This is an acknowledged seam, not an oversight.

## Alternatives considered

**Single flat `Session` struct with all fields** — rejected because most fields are optional depending on context, producing a wide struct with many `None` values and a confusing API surface.

**Trait-based polymorphism (`trait Session` with `WorktreeSession` and `StandaloneSession` impls)** — rejected per ADR-002's guidance to avoid traits for non-polymorphic cases. The variant behavior here is display, not behavior — an enum handles it cleanly.

**Embedding Claude data directly in `TmuxSessionInfo`** — rejected because it couples the tmux discovery layer to Claude-specific concerns, making the tmux module harder to test and reason about in isolation.
