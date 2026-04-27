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

- **JSON mode** (`orchard --json`, `orchard sessions --json`): always live —
  performs the same synchronous refresh as `orchard refresh` (SSH probes,
  remote worktree + tmux fetches, local git/tmux re-stat, GitHub issue/PR
  refresh) before serialising. Never returns cached results. Produces a
  versioned `JsonOutput` for scripting. The latency is bounded by the slowest
  reachable host's SSH round-trip plus the GitHub API; unreachable hosts are
  bounded by reachability-probe timeouts. The freshness contract belongs to
  `--json`: `git worktree remove` and `tmux kill-session` are observable in
  the next `orchard --json` invocation, not pending a background refresh.
  See ADR-010 for the design rationale.

Both modes produce an `OrchardState` — the single unified data model.

### TUI Event Architecture (TEA Pattern)

The TUI follows The Elm Architecture (TEA) — a unidirectional data flow:

1. **`handle_event(key/mouse) → Option<Message>`** — pure mapping from input to intent
2. **`update(msg) → UpdateResult`** — all state mutation happens here
3. **`render(frame)`** — stateless view function, reads App state

The `Message` enum in `tui/message.rs` defines every possible user intent (navigation,
actions, dialog interactions). This separates input handling from state mutation,
making the event loop testable and predictable. Mouse events (click, scroll) are
mapped to the same `Message` variants as their keyboard equivalents.

## Module Structure

```
src/
├── main.rs                # Entry point: CLI args, mode dispatch
├── lib.rs                 # Crate root: module declarations
│
├── orchard_state.rs       # OrchardState and sub-types (unified data model)
├── build_state.rs         # Pure compositor: joins source data → OrchardState
├── state.rs               # Persistent task state (AppState, Task)
├── session.rs             # Session domain types: TmuxSessionInfo, ClaudeSessionInfo,
│                          #   EnrichedSession, StandaloneConfig, ListEntry
├── session_discovery.rs   # Tmux session discovery and task reconciliation
├── derive.rs              # Display group derivation logic
├── types.rs               # Shared type definitions (OrchardConfig, RemoteConfig)
│
├── config.rs              # Per-repo config loader (.orchard.json + .git/orchard.json)
├── global_config.rs       # Global config (~/.config/orchard/config.json)
├── cache.rs               # Generic cache read/write helpers
├── cache_sources.rs       # Orchestrates multi-source cache refresh
│
├── json_output.rs         # JsonOutput mapping from OrchardState (versioned)
├── heal.rs                # Self-repair: diagnose() → HealReport → apply_fixes()
├── setup_remote.rs        # Remote host provisioning (orchard setup-remote)
├── transfer.rs            # Worktree transfer between local and remote machines
│
├── navigation.rs          # Cursor and selection navigation logic
├── priority.rs            # Priority flag persistence
├── events.rs              # Structured event logging (events.jsonl)
├── status.rs              # Tmux status bar segment writer
├── shell.rs               # Shell integration (rc files, tmux keybindings)
├── browser.rs             # Open URLs in browser
│
├── claude_state.rs        # Claude Code hook state file parsing
├── git.rs                 # Git operations (worktree create/delete, branch ops)
├── github.rs              # GitHub API helpers (issue/PR queries)
├── remote.rs              # Remote operations over SSH
├── tmux.rs                # Tmux session management (create, switch, kill)
│
├── logger.rs              # File-based logging
├── notify.rs              # Desktop notifications
├── paths.rs               # Path manipulation (tildify, truncate_left)
│
├── sources/               # Shell layer: fetching + caching (one file per source)
│   ├── mod.rs             # Re-exports, shared helpers
│   ├── github.rs          # Issues, PRs, check runs via gh CLI / GraphQL
│   ├── worktrees.rs       # Local git worktree list
│   ├── tmux.rs            # Tmux session listing (local + remote)
│   ├── claude.rs          # Claude Code hook state files
│   └── hosts.rs           # SSH reachability probes
│
└── tui/                   # TUI rendering and interaction (TEA pattern)
    ├── mod.rs             # App struct, event loop, refresh orchestration
    ├── list.rs            # Task list view rendering
    ├── dialogs.rs         # Cleanup, new worktree, transfer, heal dialogs
    ├── message.rs         # Message enum (TEA: event → message → update)
    ├── state.rs           # View state enums (ViewState, FilterMode, Phase)
    ├── theme.rs           # Centralized semantic color theme
    └── widgets.rs         # Reusable badge/status widgets
```

### What goes where

| I need to... | Look in... |
|---|---|
| Understand the full data model | `orchard_state.rs` |
| See how data sources are joined | `build_state.rs` |
| Understand session types | `session.rs` (TmuxSessionInfo, EnrichedSession, ListEntry) |
| Fix a GitHub API issue | `sources/github.rs` |
| Change how worktrees are detected | `sources/worktrees.rs` |
| Modify the TUI layout | `tui/list.rs` |
| Understand TUI event flow | `tui/message.rs` (TEA pattern) |
| Change colors/styling | `tui/theme.rs` |
| Change JSON output format | `json_output.rs` |
| Add per-repo config options | `config.rs` |
| Add global config options | `global_config.rs` |
| Fix self-healing/repair | `heal.rs` |
| Fix worktree transfer | `transfer.rs` |

## Data Model

`OrchardState` is the single source of truth. It contains everything orchard
knows, fully joined and enriched.

```
OrchardState
├── repos: Vec<RepoState>
│   ├── slug: "owner/repo"
│   └── worktrees: Vec<WorktreeState>
│       ├── path, branch, is_bare, host, is_main_worktree
│       ├── issue: Option<IssueInfo>      # number, title, state
│       ├── pr: Option<PrState>           # number, state, checks, review, conflicts
│       ├── sessions: Vec<SessionState>   # name, host, claude enrichment
│       └── display_group: DisplayGroup   # derived from joined data
├── standalone_sessions: Vec<StandaloneSessionRow>  # non-worktree sessions (e.g. shepherd)
└── hosts: HashMap<String, HostState>     # reachability per remote host
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

## Event-Driven Watch (Hybrid Poll + Webhook)

The watch daemon uses a hybrid model: periodic polling as the baseline with
webhook-triggered refreshes for near-instant reactivity.

### How it works

1. **`orchard webhook-serve`** is a separate process that receives GitHub
   webhooks via HTTP, validates HMAC-SHA256 signatures, and appends normalized
   JSONL lines to `~/.local/state/git-orchard/events.jsonl`.

2. **`orchard watch`** (the daemon) tails `events.jsonl` between poll
   iterations. When new webhook lines appear, the daemon triggers an immediate
   full refresh — bypassing the 60-second poll interval.

3. Both processes share **only the local `events.jsonl` file**. There is no IPC
   socket, no shared memory, no coordination protocol. The file is the queue.

### Why file-as-queue over unix-socket IPC

- **Persistence**: events survive daemon restarts. A daemon starting fresh
  skips historical lines (tail-from-end), but if needed, the full log is there.
- **Multi-consumer**: multiple processes can tail the same file independently,
  each tracking their own offset.
- **Simpler operational model**: `cat`, `tail -f`, `jq` all work. No protocol
  to debug.

### Two-shape coexistence

`events.jsonl` contains two kinds of lines:

| Shape | Discriminator | Writer |
|-------|--------------|--------|
| Webhook events | `"source": "webhook"` + `"kind"` field | `webhook-serve` |
| Task/session events | `"event"` field (e.g. `"task.created"`) | watch daemon, task logger |

Consumers distinguish webhook lines by the presence of `"source": "webhook"`.

### Tailer behaviour

The tailer tracks its read offset by file size (not mtime — mtime resolution
is 1 second on macOS/NFS, which would miss sub-second writes). It:

- Starts from end-of-file on cold start (no historical replay)
- Advances offset only past complete newline-terminated lines
- Resets to offset 0 when the file shrinks (rotation detected)
- Skips non-webhook and malformed lines without losing progress
- Falls back to poll-only when `events.jsonl` is missing or unreadable

## Caching

Per ADR-001: each source writes its own cache file. Cache files are JSON with
a `last_refreshed` timestamp. The filesystem IS the cache — no additional
caching layer.

`build_state()` is a pure function over cache file contents. It's called:
- On TUI startup (from existing cache files — instant)
- After each refresh phase completes
- On `--json` (after fresh fetch, never from stale cache)

## Federated Discovery (Read Path)

Each machine that runs orchard is the **authority on its own worktrees and
sessions**. When a remote is configured with `"type": "orchard-proxy"`, the
local orchard proxies read-path discovery through `ssh host orchard --json`
rather than shelling out to raw `git worktree list --porcelain` + `tmux
list-sessions` and re-running the join pipeline locally.

```
                     Local orchard
                          │
             ┌────────────┼───────────────┐
             │                            │
     local sources                 ssh host orchard --json
     (git, tmux, gh)                      │
             │                            │    remote orchard
             │                            │    already joined:
             │                            │     ├─ PR/issue
             │                            │     ├─ claude state
             │                            │     └─ display_group
             ▼                            ▼
       local OrchardState  +   remote JsonOutput snapshot
                     └────── merge ──────┘
                              │
                              ▼
                        unified OrchardState
                              │
                   ┌──────────┴──────────┐
                   ▼                     ▼
                  TUI               --json output
```

### Wire protocol

`JsonOutput` is the protocol. It's a versioned, already-joined snapshot —
`JsonRepo` / `JsonWorktree` / `JsonSession` / `JsonStandaloneSession` carry
`pr`, `issue`, `claude`, `check_state`, and `display_group` fields that the
**remote** orchard computed. The local `merge_remote_snapshot()`
(`crates/orchard/src/merge_remote.rs`) folds these into the local
`OrchardState` **without** calling `derive_all_repos` or any PR/issue/claude
join function over remote worktrees. That is the invariant — local code
trusts remote enrichment.

`JsonOutput.version: u32` is checked on every ingest. Unknown versions
raise `AdapterError::ParseFailure` — the error surfaces to the caller,
a `remote_adapter.proxy_failure` event is written to `events.jsonl`,
and the last-known snapshot stays on disk so the dashboard continues
to show its cached contents.

### Transport: `ssh host orchard --json`

No new RPC surface — just SSH + the existing `--json` mode. The
`OrchardProxyAdapter` (`crates/orchard/src/remote_adapter.rs`) holds a
`OnceLock<Result<JsonOutput, _>>` so one call to `list_worktrees()` plus one
call to `list_sessions()` on the same adapter instance shares a **single**
SSH round-trip. Reachability probes run in background services only
(`orchard refresh`, `orchard watch`) and never block a dashboard read;
OrchardProxy probes use `orchard --version` bounded by `PROBE_TIMEOUT = 3s`.

### Dashboard never blocks

`orchard --json` and TUI render are both cache-only. They read
`~/.cache/orchard/` and any in-memory state populated by previous
refreshes, build the merged `OrchardState`, and return. Target latency
< 100ms. All network I/O — SSH probes, remote `orchard --json` fetches,
local `git`/`tmux` enumeration — is owned by background services:

- `orchard refresh` — one-shot. Probes hosts, fetches from OrchardProxy
  remotes, writes caches, exits. Run this when the user wants fresh
  data.
- `orchard watch` — long-running daemon. Same work on a schedule +
  event triggers from `events.jsonl`.

An unreachable host cannot delay a dashboard read, regardless of probe
timeouts.

### Failure handling (no silent fallback)

On any failure — missing binary (exit 127), SSH failure (exit 255),
malformed JSON, version skew — the adapter returns the error and writes
a `remote_adapter.proxy_failure` event with the host and reason. There
is no implicit fallback to legacy shell-discovery. The last-known
`{host}_orchard_snapshot.json` remains on disk and in the merged state,
so the dashboard shows stale data rather than nothing.

If a user wants legacy shell-discovery for a specific host, they
reconfigure that remote as `"type": "remmy"` — explicit opt-out per host.

### Per-host cache

`~/.cache/orchard/{safe_host}_orchard_snapshot.json` persists the raw
remote `JsonOutput` on every successful fetch (atomic tmp→rename write in
`orchard_snapshot::write_snapshot`). On TUI cold start and on every
`orchard --json` invocation, `load_cached_snapshots()` pre-populates
`OrchardState` — no SSH required. Files with an unrecognised `version`
are treated as absent and overwritten by the next refresh.

### What stays on the legacy path

**Mutations** — create remote worktree, kill remote session, transfer
worktree — continue to flow through the existing `RemmyAdapter` /
`BoxdSharedAdapter` / `BoxdForkAdapter` code paths. This PR federates read
discovery only. See ADR-008 for the decision rationale.

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
- **ADR-006**: TEA pattern for TUI event handling
- **ADR-007**: Session data model (TmuxSessionInfo → EnrichedSession composition)
- **ADR-008**: Federated discovery — remote `orchard --json` is the wire protocol for read-path enrichment; failures surface explicitly (no silent legacy fallback); dashboard reads are cache-only
