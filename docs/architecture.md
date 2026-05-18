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
                       ▲                       ▲
                       │ writes via script     │ daemon polls /
                       │                       │ observes
                       │                       │
                  ┌────┴────┐                  │
                  │scripts/ │                  │
                  │canonical│                  │
                  │   ops   │                  │
                  └────▲────┘                  │
                       │                       │
              ┌────────┴────────┐              │
              │ exec            │ exec         │
              │                 │              │
         ┌────┴────┐       ┌────┴──────────────┴─┐
         │   CLI   │       │       Daemon        │
         │ (Rust)  │       │ (Go; GraphQL :7777) │
         │standalone│      │                     │
         │         │       │  • queries:         │
         │exec     │       │      in-process     │
         │scripts  │       │  • mutations:       │
         │directly │       │      exec scripts   │
         │         │       │  • subscriptions:   │
         │         │       │      emit deltas    │
         └─────────┘       └──────────┬──────────┘
                                      │ GraphQL
                                      │
                              ┌───────┴───────┐
                              │               │
                         ┌────▼───┐      ┌────▼────┐
                         │  GUI   │      │  TUI    │
                         │(Svelte)│      │(Ratatui)│
                         └────────┘      └─────────┘
```

**Daemon edges, explicit:**
- **Queries** — in-process Go. The daemon polls / observes external truth, caches projections, and serves field resolvers from those caches. No script exec on the read path (L4).
- **Mutations** — exec the matching `scripts/<op>` and project its `--json` output as the GraphQL response (L5). The script writes to external truth; the daemon's next poll picks up the change; subscribers see deltas; the mutation response also carries the affected node so the client cache updates immediately (L8).
- **Subscriptions** — in-process Go, emitting deltas as the cache notices external-truth changes (R16, S7).

Both CLI and daemon end up at `scripts/` for writes. Scripts are the single source of truth for "how to do this operation"; CLI and daemon are wrappers with different surfaces (CLI = user-facing args; daemon = GraphQL mutation resolver).

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

The daemon owns the following domains. Each domain is a flat module under [`daemon/<name>/`](../daemon/) (see [RULES.md R1, R2](../RULES.md)). The directory skeleton + per-domain schema partials + per-domain READMEs are checked in; the Go code migration from `internal/server/providers/<name>/` and `internal/server/resolvers/` is tracked in #613.

> **Skeleton status:** `daemon/` exists with one directory per domain (13 total). Each contains a `schema.graphql` partial (S15a) and a `README.md` linking the domain to its current source location and the relevant [RULES.md](../RULES.md) citations. **Phase-0 gqlgen composition spike VERIFIED 2026-05-18** — the 13 partials compose to ~24K lines of generated code on first try and are idempotent on rerun. Go files (`service.go`, `provider.go`, `resolver_*.go`, `loaders.go`, `mutations.go`) are not yet authored — that is the per-domain migration work in #613. The proven `gqlgen.yml` composition shape lives at `/tmp/gqlgen-shape.txt` for the swarm. See [`daemon/README.md`](../daemon/README.md) for the canonical per-domain layout.

**Flat, not nested.** Earlier drafts grouped related domains under parent directories (`daemon/claude/{jsonls,instance,account}/`). After review the decision is to keep modules flat — `daemon/claude-jsonls/`, `daemon/claude-account/`, etc. Hyphenated names group by prefix without forcing a nested taxonomy that pretends modules with different sources / cadences / failure modes are "really one thing." When two modules genuinely become indistinguishable, they merge; until then they stay separate.

### What is NOT a domain

Three categories of things that look like domains but aren't:

- **GraphQL join types** (e.g. today's `Worktree` with its `.tmuxSessions` / `.claudeInstance` / `.pr` fields) are not domains. They are types owned by the most-responsible domain (`Worktree` lives in `git`; its cross-domain fields resolve via sibling domain services through GraphQL field resolution). The cross-domain join IS the GraphQL graph; we don't need a separate domain for it.
- **Startup tasks / one-shot work** (e.g. discovering repos at boot) are not domains. They are bootstrap routines that populate a domain's data and then go away. The data they populate belongs to whichever domain owns the storage (repos → `git`).
- **Daemon plumbing** (federation transport, the `node(id)` id-prefix dispatcher, the gqlgen runtime) is not a domain. It is the daemon's shell — see "Daemon shell" below.

### Domain table

13 flat domains. Each becomes `daemon/<name>/` with the canonical layout (`service.go` / `provider.go` / `adapter.go` / `resolver_*.go` per type / `loaders.go` / `mutations.go` / `subscriptions.go` / `schema.graphql` — omit files that don't apply).

The 9-domain initial draft (#618 first commit) was split per devils-advocate review: `claude-jsonls` carried 3 lifecycles, `daemon-self` was a god-module.

| Domain | Sources | Consumes (per service contract) | Current path | Schema partial |
|---|---|---|---|---|
| **[git](../daemon/git/)** | Local git repos: worktrees, branches, refs, status, ahead/behind, remote heads. Owns `Repo` (local view), `Worktree`. Mutations: worktree create/rm/mv, fetch/pull/push. Repo discovery folds in. | — | `internal/server/providers/git/` + `internal/server/providers/config/` (Repo node provider, misnamed) + `internal/server/providers/repodiscovery/` | [`schema.graphql`](../daemon/git/schema.graphql) |
| **[gh](../daemon/gh/)** | GitHub API: pull requests, issues, workflow runs. Owns `PullRequest`, `Issue`, `WorkflowRun`, `Label`. Mutations: PR review/label/comment, issue create. Owns the `Query.gh(query, variables): JSON` pass-through per S16b. | — | `internal/server/providers/gh/` | [`schema.graphql`](../daemon/gh/schema.graphql) |
| **[tmux](../daemon/tmux/)** | tmux server: sessions, windows, panes, clients. Owns `TmuxSession`, `TmuxWindow`, `TmuxPane`, `TmuxClient`. Mutations: send-keys, kill-pane, new-window. | `ps` (pane process), `claude-instance` (TmuxPane.claudeInstance back-edge) | `internal/server/providers/tmux/` | [`schema.graphql`](../daemon/tmux/schema.graphql) |
| **[ps](../daemon/ps/)** | OS process table (pid, ppid, cwd, command, args). Owns `Process`. Read-only. | — | `internal/server/providers/ps/` | [`schema.graphql`](../daemon/ps/schema.graphql) |
| **[host-identity](../daemon/host-identity/)** | Machine identity + resource load (CPU/mem/disk/loadavg, 5s TTL). Owns `Host`, `ResourceLoad`. | — | `internal/server/providers/host/` | [`schema.graphql`](../daemon/host-identity/schema.graphql) |
| **[host-services](../daemon/host-services/)** | launchd/systemd unit watchlist (config-driven). Owns `HostService`, `HostServiceState`. | — | `internal/server/providers/hostservice/` | [`schema.graphql`](../daemon/host-services/schema.graphql) |
| **[claude-jsonls](../daemon/claude-jsonls/)** | Raw Claude Code JSONL parsing. Owns `Conversation` only. | — | `internal/server/providers/claudeprojects/` | [`schema.graphql`](../daemon/claude-jsonls/schema.graphql) |
| **[claude-instance](../daemon/claude-instance/)** | Live Claude REPL — JOIN of jsonl tail freshness + matching pane process. Owns `ClaudeInstance`, `InstanceState`. | `claude-jsonls`, `tmux`, `ps` | `internal/server/providers/claudeinstance/` | [`schema.graphql`](../daemon/claude-instance/schema.graphql) |
| **[claude-account](../daemon/claude-account/)** | `claude auth status` + `ccusage` shellout. Owns `ClaudeAccount`. | — | `internal/server/providers/claudeaccount/` | [`schema.graphql`](../daemon/claude-account/schema.graphql) |
| **[contracts](../daemon/contracts/)** | Agent delivery commitments parsed from Contracts-plugin records (ride on the same JSONL stream as conversations). State machine: 9 statuses. Owns `Contract`, `ContractStatus`, `ContractQuestion`. | `claude-jsonls` | `internal/server/providers/contracts/` | [`schema.graphql`](../daemon/contracts/schema.graphql) |
| **[daemon-self](../daemon/daemon-self/)** | Daemon liveness, version, schema bytes, node-id dispatcher entrypoint. Owns `Health`. | — | `internal/server/server.go` + `internal/server/resolvers/node.resolvers.go` | [`schema.graphql`](../daemon/daemon-self/schema.graphql) |
| **[daemon-meta](../daemon/daemon-meta/)** | Provider freshness counters + per-field provenance envelope. Owns `DaemonState`, `ProviderHealth`, `Meta`. Mutations: `daemonReload` (L5 carve-out for daemon-internal state). | — | `internal/server/resolvers/schema.resolvers.go` (scattered) | [`schema.graphql`](../daemon/daemon-meta/schema.graphql) |
| **[views](../daemon/views/)** | Composite views that delegate (per S14) to per-type resolvers for round-trip economy. Owns `WorkView`. | (every domain that contributes a field) | `internal/server/resolvers/schema.resolvers.go` (scattered) | [`schema.graphql`](../daemon/views/schema.graphql) |

### Daemon shell (not domains)

These live in `daemon/` at the top level, not as domains. They are infrastructure that *enables* domains, not data the daemon serves.

| Shell concern | Owns | Current path | Notes |
|---|---|---|---|
| `daemon/server.go` | HTTP / WebSocket / handler wiring. Composes per-domain resolvers into the aggregate `Resolver`. Origin gating (`checkGUIOrigin`). gqlgen schema composition (globs `daemon/*/schema.graphql` into one schema). | `internal/server/server.go` | |
| `daemon/transport/` (federation) | Peer-daemon proxy: turns a remote orchard daemon into a backend like git/tmux/ps. WebSocket subprotocol negotiation. Peer list comes from `daemon-self`'s loaded settings. `LocalInvalidator` for cross-process cache invalidation. **Not a domain — it's the transport layer.** | `internal/server/providers/peerproxy/` | Provider name is a misnomer; this is daemon plumbing. Moves to shell. |
| `daemon/loaders.go` (composer) | Thin aggregate that holds per-domain loaders + a few cross-domain loaders that can't cleanly belong to one domain (e.g. `loadPanesByCwd` consumes tmux+ps). Cross-domain loaders are named and owned here explicitly. | `internal/server/loaders/loaders.go` (30KB, today undifferentiated) | Per-domain loader code moves into `daemon/<name>/loaders.go`; cross-domain loaders stay at the shell with named ownership. |
| `daemon/node.go` | The `node(id: ID!)` id-prefix dispatcher. **Not a domain — it's a registry.** Each domain registers its prefix (`Host:`, `TmuxPane:`, `Conversation:`, ...) and the lookup function. `daemon/node.go` is the registry + the `Query.node` resolver. | `internal/server/resolvers/node.resolvers.go` | Current 535-line file with 14 hard-coded prefix branches becomes a tiny registry; each domain owns its prefix and lookup. |
| `daemon/graphql/` | gqlgen-generated code. Not authored. | `internal/server/graphql/` | Wholesale move in Phase 0. |

### Schema partials

Each domain owns `daemon/<name>/schema.graphql`. The root [`daemon/schema.graphql`](../daemon/schema.graphql) declares the shared `Node` interface, the `Time` and `JSON` scalars, and the empty `Query` / `Mutation` / `Subscription` shells. Domain partials use `extend type Query`, `extend type Mutation`, `extend type Subscription` to add their fields. gqlgen globs all partials into one composed schema at build time — there is no monolithic schema file to edit.

The schema is the contract (RULES.md S11). The partial is the contract per domain (S15a). The partials checked in at `daemon/*/schema.graphql` carve up today's monolithic `schema.graphql` along the 13-domain boundary; the domain owns the types and fields it declares.

Cross-domain types are governed by RULES.md [S15b](../RULES.md): when domain A adds a field to a type owned by domain B, both the `extend type` AND the resolver live in A. A imports B's service interface (R5), not B's provider.

### Mutation ownership convention

Each domain owns its mutations in `daemon/<name>/mutations.go`. The aggregate `mutationResolver` in `daemon/server.go` composes them. Per L5, every mutation execs a `scripts/<op>` and projects its `--json` output. No domain implements mutation logic in Go beyond input validation + script-exec wrapping.

`daemon-self` mutations (e.g. `Mutation.daemonReload`, manual cache rebuild) live in `daemon/daemon-self/mutations.go`. These are the rare exception to "every mutation execs a script" — they affect daemon-internal state, not external truth.

### Domains explicitly NOT in scope for the module-refactor

- **chat** (`internal/server/providers/chat/`) — being deleted (#616). Skip.
- **gqlgen-generated code** (`internal/server/graphql/`) — daemon shell concern; moves wholesale in Phase 0.

### Dependency graph (north star, not current state)

```
   Leaves (no domain deps — read external truth directly):
     git, gh, tmux, ps, host-identity, host-services,
     claude-jsonls, claude-account, daemon-self

   Join domains (CONSUME 2+ leaves):
     claude-instance  →  claude-jsonls, tmux, ps
     contracts        →  claude-jsonls
     views (WorkView) →  delegates to every contributing leaf per S14
     daemon-meta      →  reads each provider's freshness counters

   Cross-domain `extend type` back-edges (S15b — declared in CONSUMER):
     git extends Worktree.{tmuxPanes, tmuxSession}    (consumer: git)
     git extends Worktree.claudeInstances             (consumer: git)
     git extends Worktree.{pr, issue}                 (consumer: git)
     git extends Worktree.processes                   (consumer: git)
     tmux extends TmuxPane.process                    (consumer: tmux)
     tmux extends TmuxPane.claudeInstance             (consumer: tmux)
     ps extends Host.processes                        (consumer: ps)
     host-services extends Host.hostServices          (consumer: host-services)
```

Per [S15b](../RULES.md): when domain A adds a field to a type owned by domain B, the `extend type` declaration AND resolver live in A. A imports B's service interface (R5) — never B's provider.

`claude-instance` is the highest-coupling domain in the north star (consumes 3). Every other consumer-of-leaves consumes exactly 1. This is intentional: the JOIN that derives a ClaudeInstance from jsonl + tmux + ps is where the cross-domain work earns its keep.

**Current code is wrong vs this target in known places** — these become explicit fixes during each domain's migration:

- `git` imports `config` (= `repo`) — fold `config`/`repodiscovery` into `git`
- contracts and claude-instance currently bundled into `claudeprojects` — split per the 13-domain target
- `peerproxy` imports `ps` — fix during transport extraction; peerproxy is daemon shell, not data

Each domain gets its own refactor PR following [RULES.md](../RULES.md). See #613 for the swarm dispatch plan.

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
