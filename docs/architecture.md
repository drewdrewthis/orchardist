# Orchard Architecture

## Overview

Orchard is a development command center for managing git worktrees, tmux sessions, GitHub issues/PRs, and Claude Code sessions across multiple repositories. It surfaces as:

- a **TUI** dashboard (Rust + Ratatui)
- a **GUI** (SvelteKit + Houdini)
- a **GraphQL daemon** (Go, on `localhost:7777`) that federates state across local machine and remote SSH peers
- a **CLI** (`orchard <verb>`) that exposes operations as standalone scripts

The TUI and GUI are read-and-write consumers of the daemon. The daemon and CLI are sibling actors over the same external truth (tmux server, git repo, GitHub API, claude jsonl, filesystem); neither persists its own state.

> **The repo's design rules live in [RULES.md](../RULES.md) (top-level).** This doc describes the architecture; RULES.md says what good looks like inside it.

## Ecosystem model

Orchard is **four ecosystems**, each with its own identity, contract, and ownership boundary:

```
                       External truth
        (tmux server, git, gh, claude jsonl, filesystem)
                       ▲              ▲
                       │              │
                  ┌────┴────┐    ┌────┴────┐
                  │ scripts/│    │         │
                  │ (canon- │    │         │
                  │ ical    │    │         │
                  │ ops)    │    │         │
                  └────┬────┘    │         │
                       │         │         │
                       │    exec │         │ in-process
                       │         │         │ (queries)
                  ┌────▼────┐  ┌─▼─────────▼─┐
                  │   CLI   │  │   Daemon    │
                  │ (Rust;  │  │ (Go; GraphQL│
                  │ stand-  │  │ on :7777)   │
                  │ alone)  │  │             │
                  └─────────┘  └──────┬──────┘
                                      │ GraphQL
                                      │
                              ┌───────┴───────┐
                              │               │
                         ┌────▼───┐      ┌────▼────┐
                         │  GUI   │      │  TUI    │
                         │(Svelte)│      │(Ratatui)│
                         └────────┘      └─────────┘
```

### Layer responsibilities

| Layer | Owns | Consumes |
|---|---|---|
| `scripts/` | Canonical operations (tmux send-keys, worktree create, etc.). Each script is independently executable with `--json` output. Picks the right language for the job (bash/python/rust/go). | External truth directly. |
| **CLI** (`orchard <verb>`) | User-facing command surface. **Standalone — works without the daemon.** Thin Rust wrappers that exec scripts. | `scripts/`, external truth via scripts. |
| **Daemon** (`internal/server/`, becoming `daemon/`) | GraphQL service at `localhost:7777`. **Queries** resolve in-process from cached projections of external truth. **Mutations** exec the corresponding `scripts/<op>` and project its `--json` output as the response. **Daemon-self commands** (start/stop/status/introspect) live under `orchard daemon <verb>`. | `scripts/` (mutations), external truth (queries, polling), `orchard daemon ...` sub-CLI (self-management). |
| **GUI** (`crates/orchard-gui/`) | Consumer. SvelteKit + Houdini normalized cache. | **Daemon only**, via GraphQL queries/mutations/subscriptions. Never execs scripts, never imports CLI crates, never touches external truth directly. |
| **TUI** (`crates/orchard/`) | Consumer. Rust + Ratatui. | **Daemon only**, via GraphQL. Same constraints as GUI. |

### Dependency invariants (enforced by RULES.md L1–L10)

- **CLI is the foundation.** Standalone. Knows nothing about the daemon.
- **Daemon stands sibling to CLI.** Both operate over external truth independently. Neither needs the other to function; they coordinate via the shared `scripts/` library, not via cross-process state.
- **GUI and TUI stand above the daemon.** They never reach past it. Anything they need is a daemon query, mutation, or subscription.
- **No persisted daemon state.** Restart re-observes from scratch. Caches are rebuildable.
- **Mutation pattern: write via daemon mutation, see the change via subscription (or via mutation response).** No "cache invalidate" logic in the daemon — GraphQL's mutation-response + subscription contract handles it.

### Why this shape

The original architecture grew the daemon as a **categorically-organized** Go tree (`internal/server/{providers,resolvers,loaders,graphql}/`) with the TUI and CLI bypassing it for any write. That produced three failure modes:

1. **No module-level ownership** — surgical edits to one provider/resolver pair drift; nobody owns "the tmux concept" end-to-end.
2. **Duplicate logic across consumers** — every client re-implemented `tmux send-keys` style operations.
3. **Daemon was read-only in practice** — its mutation surface was thin because writes happened in client code.

The ecosystem model + script-as-canonical pattern collapses all three: one home per operation, one consumer-facing write API (daemon mutations), one module per domain end-to-end. See [ADR-023](adr/023-repo-constitution.md).

## Daemon module domains

The daemon owns the following domains. Each domain becomes a module under `daemon/<name>/` (see [RULES.md R1, R2](../RULES.md)). Today they live under `internal/server/providers/<name>/`; migration is tracked in #613.

| Domain | Owns | Current path | Status |
|---|---|---|---|
| **tmux** | Sessions, windows, panes, clients across tmux servers (local + federated). Send-keys mutation. Pane content streaming. | `internal/server/providers/tmux/` | Live. Audit + migration pending. Known smell: `Snapshot()` hot path (#612). |
| **git** | Worktree set, branch state, ahead/behind, remote heads. Mutations: worktree create / remove / move. | `internal/server/providers/git/` | Live. Audit + migration pending. |
| **gh** | Repos, issues, pull requests, PR enrichment (mergeable, status checks, labels). Mutations: PR review / label / comment (gap). | `internal/server/providers/gh/` | Live. Audit + migration pending. Known smell: single+batch enrichment divergence (#615). |
| **claude** | Claude Code sessions (running REPLs), conversation jsonl, project on disk, active account. **Collapses today's `claudeprojects/` + `claudeinstance/` + `claudeaccount/`** — they're three faces of one domain. Mutations: send-text-to-pane (today), start/stop session (gap). | `internal/server/providers/{claudeprojects,claudeinstance,claudeaccount}/` | Live. ADR-022 pane-first refactor landed; module collapse + audit pending. |
| **ps** | Process metadata (cwd, pid, parent, command) for currently-running processes. Read-only. | `internal/server/providers/ps/` | Live. Audit + migration pending. |
| **host** | Host identity, federation peer info. **Collapses today's `host/` + `hostservice/`** — adjacent concerns. | `internal/server/providers/{host,hostservice}/` | Live. Audit + migration pending. |
| **contracts** | Contracts engine (durable delivery primitive — see [references/contracts.md](https://github.com/drewdrewthis/orchard-codex)). Currently exposed via MCP, not GraphQL. | `internal/server/providers/contracts/` | Live. Mutation surface in daemon GraphQL is sparse — audit may surface gaps. |
| **peerproxy** | Federation control plane: proxy queries to peer daemons, route based on host. | `internal/server/providers/peerproxy/` | Live. Audit + migration pending. |
| **worktree** | Composite domain: joins git worktree + tmux sessions/panes + claude instances + gh PR into the `Worktree.*` resolver chain. Cross-module consumer; depends on tmux + git + claude + gh services. | `internal/server/resolvers/worktree_*.go` | Today: scattered across resolver files. After migration: new `daemon/worktree/` module owning these resolvers, consuming sibling-module services. |
| **repodiscovery** | Discovers orchard-managed repositories on disk. | `internal/server/providers/repodiscovery/` | Live. Audit + migration pending. |
| **config** | Daemon configuration. Read-only at runtime (config writes are CLI-only per existing rule). | `internal/server/providers/config/` | Live. Likely thin; audit may collapse into `host` or `daemon-self`. |

**Domains explicitly NOT in scope for the module-refactor:**

- **chat** (`internal/server/providers/chat/`) — being deleted (#616). Skip.
- **gqlgen-generated code** (`internal/server/graphql/`) — moves wholesale to `daemon/graphql/` in Phase 0; not a domain.
- **loaders aggregate** (`internal/server/loaders/`) — splits per domain; the remaining cross-module composer lives at `daemon/loaders.go` (thin).
- **server.go** — moves to `daemon/server.go`; the composer that wires modules into the aggregate Resolver.

Each domain gets its own refactor PR following [RULES.md](../RULES.md) and producing a per-module `AUDIT.md`. See #613 for the swarm dispatch plan.

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

- **JSON mode** (`orchard-tui --json`, `orchard-tui sessions --json`): always live —
  performs the same synchronous refresh as `orchard-tui refresh` (SSH probes,
  remote worktree + tmux fetches, local git/tmux re-stat, GitHub issue/PR
  refresh) before serialising. Never returns cached results. Produces a
  versioned `JsonOutput` for scripting. The latency is bounded by the slowest
  reachable host's SSH round-trip plus the GitHub API; unreachable hosts are
  bounded by reachability-probe timeouts. The freshness contract belongs to
  `--json`: `git worktree remove` and `tmux kill-session` are observable in
  the next `orchard-tui --json` invocation, not pending a background refresh.
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

## Workspace crates

```text
crates/
├── orchard/                # The orchard-tui binary + library (src layout below)
├── orchard-dispatcher/     # The user-facing `orchard` binary; routes verbs
│                           #   to orchard-{tui,daemon,worktree,chat} per ADR-013.
├── orchard-worktree/       # CLI for worktree mutations: clap wrapper on
│                           #   worktree-core (orchard new/rm/prune/ls/path).
├── worktree-core/          # Pure git worktree operations (list/create/destroy/prune/parse).
│                           #   Backs orchard-tui dialogs + orchard-worktree CLI.
└── orchard-gui/src-tauri/  # Tauri-based GUI shell (preview)
```

`worktree-core` is the single source of truth for worktree mutation primitives.
The orchard-tui binary and `orchard-worktree` CLI both depend on it directly.
`orchard-dispatcher` is a thin (~150 LOC) router with no orchard dependencies
— it just execs the right helper binary. See ADR-013 for the dispatcher
architecture.

## Module Structure (orchard crate)

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
├── global_config.rs       # Global config (~/.orchard/config.json)
├── cache.rs               # Generic cache read/write helpers
├── cache_sources.rs       # Orchestrates multi-source cache refresh
│
├── json_output.rs         # JsonOutput mapping from OrchardState (versioned)
├── heal.rs                # Self-repair: diagnose() → HealReport → apply_fixes()
├── setup_remote.rs        # Remote host provisioning (orchard-tui setup-remote)
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
| Change git worktree create/destroy/list logic | `crates/worktree-core/` |

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

1. **`orchard-tui webhook-serve`** is a separate process that receives GitHub
   webhooks via HTTP, validates HMAC-SHA256 signatures, and appends normalized
   JSONL lines to `~/.local/state/git-orchard/events.jsonl`.

2. **`orchard-tui watch`** (the daemon) tails `events.jsonl` between poll
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
local orchard proxies read-path discovery through `ssh host orchard-tui --json`
rather than shelling out to raw `git worktree list --porcelain` + `tmux
list-sessions` and re-running the join pipeline locally.

```
                     Local orchard
                          │
             ┌────────────┼───────────────┐
             │                            │
     local sources                 ssh host orchard-tui --json
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

### Transport: `ssh host orchard-tui --json`

No new RPC surface — just SSH + the existing `--json` mode. The
`OrchardProxyAdapter` (`crates/orchard/src/remote_adapter.rs`) holds a
`OnceLock<Result<JsonOutput, _>>` so one call to `list_worktrees()` plus one
call to `list_sessions()` on the same adapter instance shares a **single**
SSH round-trip. Reachability probes run in background services only
(`orchard-tui refresh`, `orchard-tui watch`) and never block a dashboard read;
OrchardProxy probes use `orchard-tui --version` bounded by `PROBE_TIMEOUT = 3s`.

### Dashboard never blocks

`orchard-tui --json` and TUI render are both cache-only. They read
`~/.cache/orchard/` and any in-memory state populated by previous
refreshes, build the merged `OrchardState`, and return. Target latency
< 100ms. All network I/O — SSH probes, remote `orchard-tui --json` fetches,
local `git`/`tmux` enumeration — is owned by background services:

- `orchard-tui refresh` — one-shot. Probes hosts, fetches from OrchardProxy
  remotes, writes caches, exits. Run this when the user wants fresh
  data.
- `orchard-tui watch` — long-running daemon. Same work on a schedule +
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
`orchard-tui --json` invocation, `load_cached_snapshots()` pre-populates
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
- **ADR-008**: Federated discovery — remote `orchard-tui --json` is the wire protocol for read-path enrichment; failures surface explicitly (no silent legacy fallback); dashboard reads are cache-only
- **ADR-010**: `orchard-tui --json` (and `orchard-tui sessions --json`) is the live read; the TUI keeps the cached fast path. Reverses ADR-008's cache-only `--json` clause.
- **ADR-013**: Orchard CLI ecosystem — one user-facing `orchard` binary as a thin dispatcher; `orchard-tui`, `orchard-daemon`, `orchard-worktree` as helper binaries; `crates/worktree-core/` is the shared library backing worktree mutation in TUI + CLI. Hybrid grammar (namespaced verbs + bare-verb shortcuts for the worktree primary unit).
- **ADR-014**: Global config location — `~/.orchard/config.json` (dotdir) instead of `~/.config/orchard/config.json` (XDG). Matches every other dotdir tool in the stack (`~/.aws`, `~/.kube`, `~/.ssh`, `~/.cargo`, `~/.claude`). Clean break — daemon and CLI emit a migration hint when the legacy path is detected.
- **ADR-015**: Minimal config schema.
- **ADR-016**: GraphQL is the wire protocol — daemon serves `/graphql`; clients (TUI, GUI, CLI, mobile) call it; no client-side `git`/`gh`/`tmux` exec.
- **ADR-017**: Layer responsibilities — daemon owns state/joins/mutations; clients render only; no client-side joins or caching layers.
- **ADR-018**: Daemon owns mutations — worktree, tmux, config writes flow through GraphQL mutations; mobile and remote clients unblock.
- **ADR-019**: Houdini cache — no client-side cache layers; rely on Houdini's normalized cache and subscription invalidation.
- **ADR-020**: Tailwind-first styling — no new scoped CSS in orchard-gui; opportunistic migration on touched components.
- **ADR-021**: Federation peers hot-reload — fsnotify watcher + `Provider.ApplyPeers` diff applier lets operators add/remove `peers[]` entries without restarting the daemon; prerequisite: each peer VM must run `boxd proxy new graphql --vm=<name> --port=7777` and have `orchard-daemon` listening on `127.0.0.1:7777`.

### Legacy vs target state

The sections above this ADR list describe **legacy** orchard-tui shape: cache
files joined client-side, source modules that shell out to `git`/`gh`/`tmux`,
`--json` as a per-invocation refresh. ADRs 016–020 define the **target** shape:
daemon owns state, joins, and mutations; clients call `/graphql`; Houdini's
normalized cache holds query results; no module-level Maps or scoped CSS in
new code. Migration is incremental and lives across many PRs — when both
sections describe the same behaviour, the ADR (target) wins. Build new
features against the target; only touch the legacy path when fixing bugs in
it or when a target equivalent does not yet exist.
