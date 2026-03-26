# Orchard Architecture

## Overview

Orchard is a TUI dashboard for managing git worktrees, tmux sessions, and their
associated GitHub issues and PRs. It aggregates data from multiple external
sources into a single unified view.

## Guiding Principles

### Functional Core, Imperative Shell

All business logic lives in **pure functions** that take data in and return data
out. No IO, no side effects, no external calls. These functions are trivially
testable by constructing inputs.

The **shell** is the thin outer layer that calls external commands (`git`, `gh`,
`tmux`, SSH), reads/writes cache files, and passes results to the core. The
shell is kept as thin as possible — it fetches data, the core computes meaning.

### Modules Are Service Boundaries

Rust modules provide encapsulation without needing service objects or traits.
Each module exposes a public API (functions + types), hides internals, and owns
one responsibility. You don't need a `GitHubService` struct when `mod github`
with `pub fn fetch_issues()` does the same job.

Traits are reserved for **genuinely polymorphic behavior** — cases where
multiple implementations exist at runtime. Not for testability alone.

### SRP at Every Level

- **Module**: one data domain (e.g., `sources::github` owns GitHub API calls)
- **File**: one concern within that domain (e.g., `github.rs` ≤ 300 lines)
- **Function**: one operation (e.g., `fetch_issues` fetches issues, nothing else)

Files over 500 lines are a smell. Split them.

### Documentation as Architecture

Every module, public function, and public type has `///` doc comments. These
aren't optional — they're part of the architecture. Someone reading the crate
docs (`cargo doc --open`) should understand the system without reading the code.

Module-level docs (`//!` at the top of each file) explain:
- What this module is responsible for
- What it depends on
- How it fits into the overall data flow

Doc comment examples are **compiled and tested** by `cargo test`. This means
documentation stays correct or the build breaks.

## Data Flow

```
External Sources              Cache Files              Core Logic
─────────────────      ─────────────────────      ──────────────
gh api (GraphQL)  ──→  {owner}_{repo}_prs.json
gh api (GraphQL)  ──→  {owner}_{repo}_issues.json
git worktree list ──→  {owner}_{repo}_worktrees.json       ┐
ssh + git         ──→  {owner}_{repo}_remote_wts.json      │
tmux list-sessions──→  tmux_sessions.json                   ├──→ build_state() ──→ OrchardState
ssh + tmux        ──→  {host}_tmux_sessions.json            │         │
claude hooks      ──→  /tmp/orchard-claude-*.json           │         ├──→ TUI (renders)
ssh probe         ──→  (in-memory only)                     ┘         └──→ --json (fresh fetch, no cache)
```

### Two modes

- **TUI mode**: reads from cache for instant startup, refreshes in background.
  Two-phase refresh: fast locals first (git, tmux, claude files), then slow
  remotes (GitHub API, SSH). Re-renders after each phase.

- **JSON mode**: fetches fresh data from all sources. Never returns cached
  results. Produces a versioned `JsonOutput` for scripting.

Both modes produce an `OrchardState` — the single unified data model.

## Module Structure

```
src/
├── main.rs              # Entry point: CLI args, mode dispatch
├── state.rs             # OrchardState and all sub-types (data models)
├── build_state.rs       # Pure compositor: joins source data → OrchardState
│
├── sources/             # Shell layer: fetching + caching (one file per source)
│   ├── mod.rs           # Re-exports, shared helpers
│   ├── github.rs        # Issues, PRs, check runs via gh CLI / GraphQL
│   ├── worktrees.rs     # Local git worktree list
│   ├── remote.rs        # Remote worktrees via SSH
│   ├── tmux.rs          # Tmux session listing (local + remote)
│   ├── claude.rs        # Claude Code hook state files
│   └── hosts.rs         # SSH reachability probes
│
├── tui/                 # TUI rendering and interaction
│   ├── mod.rs           # App struct, event loop, refresh orchestration
│   ├── list.rs          # Task list view rendering
│   ├── dialogs.rs       # Cleanup, new session, transfer dialogs
│   ├── state.rs         # View state enums
│   └── widgets.rs       # Reusable badge/status widgets
│
├── json.rs              # JsonOutput mapping from OrchardState (versioned)
│
├── config/              # Configuration loading
│   ├── global.rs        # ~/.config/orchard/config.json + CWD auto-discovery
│   └── repo.rs          # .orchard.json + .git/orchard.json (two-layer merge)
│
└── util/                # Shared utilities
    ├── paths.rs         # Path manipulation (tildify, truncate_left)
    ├── logger.rs        # File-based logging
    └── notify.rs        # Desktop notifications
```

### What goes where

| I need to... | Look in... |
|---|---|
| Understand the full data model | `state.rs` |
| See how data sources are joined | `build_state.rs` |
| Fix a GitHub API issue | `sources/github.rs` |
| Change how worktrees are detected | `sources/worktrees.rs` |
| Modify the TUI layout | `tui/list.rs` |
| Change JSON output format | `json.rs` |
| Add per-repo config options | `config/repo.rs` |

## Data Model

`OrchardState` is the single source of truth. It contains everything orchard
knows, fully joined and enriched.

```
OrchardState
├── repos: Vec<RepoState>
│   ├── slug: "owner/repo"
│   ├── config: RepoLocalConfig (CI filters, etc.)
│   └── worktrees: Vec<WorktreeState>
│       ├── path, branch, is_bare, host
│       ├── issue: Option<IssueInfo>      # number, title, state
│       ├── pr: Option<PrInfo>            # number, state, checks, review
│       ├── sessions: Vec<SessionInfo>    # tmux name, claude state, cost
│       └── display_group: DisplayGroup   # derived from joined data
└── hosts: HashMap<String, HostInfo>      # reachability per remote host
```

Display-only fields (like `display_group`) are computed by `build_state`, not
stored in caches. `is_stale` (PR merged, issue closed) is derived at render
time from `pr.state` and `issue.state`.

## Join Logic

`build_state()` joins data sources in this order:

1. **Start from non-bare worktrees** (local + remote, tagged by host)
2. **Match PR**: worktree branch → PR head branch. Skip default branches.
3. **Link issue**: PR closing keywords first (`Closes #N`), then branch
   convention (`issue{N}/...`) as fallback.
4. **Match sessions**: tmux session working directory == worktree path.
5. **Match Claude state**: tmux session name → hook state file lookup.
6. **Derive display group**: from the joined data (shepherd, needs attention,
   claude working, ready to merge, other).

## Caching

Per ADR-001: each source writes its own cache file. Cache files are JSON with
a `last_refreshed` timestamp. The filesystem IS the cache — no additional
caching layer.

`build_state()` is a pure function over cache file contents. It's called:
- On TUI startup (from existing cache files — instant)
- After each refresh phase completes
- On `--json` (after fresh fetch, never from stale cache)

## Testing Strategy

| Layer | How to test |
|---|---|
| **Core** (`build_state`, join logic) | Unit tests with constructed data. No IO. |
| **Sources** (fetch + parse) | Integration tests against real `git`/`tmux` in temp dirs. Parse functions unit-tested with fixture data. |
| **TUI** (rendering) | `TestBackend` smoke tests for layout correctness. |
| **JSON** (output format) | Snapshot tests against expected JSON structure. |
| **End-to-end** | `assert_cmd` tests against the compiled binary. |

## ADRs

- **ADR-001**: Cache architecture — per-source files, no computed state on disk
- **ADR-002**: No OOP service layers (superseded by ADR-004 for scope)
- **ADR-003**: Per-repo config — `.orchard.json` + `.git/orchard.json`
- **ADR-004**: Unified data model with functional core, imperative shell
