# ADR-004: Unified data model with compositor

## Status

Accepted — supersedes ADR-002's "no service layers" stance (see rationale below)

## Context

Orchard currently has two parallel data pipelines:

1. **Legacy collector** — produces `Vec<Worktree>` used by `--json` and old TUI views. Fetches data sequentially, enriches in-place.
2. **Cache-based pipeline** — writes per-source cache files, then `derive_from_all_caches` joins them into `Vec<TaskRow>` for the new TUI.

This creates problems:
- `--json` output is incomplete (missing issue state, Claude state, CI details)
- The TUI flickers when data isn't ready (empty → populated)
- Remote worktrees disappear during refresh instead of going dim
- Two code paths to maintain, neither producing the full picture
- No single place to reason about "what does orchard know right now?"

### Relationship to ADR-002

ADR-002 rejected OOP service layers and DI. That was correct at the time — the codebase was small and the service abstraction wasn't justified. The conditions ADR-002 listed for revisiting have been met:
- Multiple modules now exceed 500 lines
- Test mocks are needed (the service layer is untestable without them)
- The dual-pipeline problem requires a clean join point

This ADR keeps ADR-002's spirit (no unnecessary abstraction) while introducing the minimum structure needed: **free functions organized by domain, a generic cache wrapper, and a pure compositor function**. No trait objects, no DI containers, no viral type parameters.

## Decision

### Single data model: `OrchardState`

One struct that represents everything orchard knows.

```
OrchardState
├── repos: Vec<RepoState>
│   ├── slug: String
│   ├── config: RepoLocalConfig
│   └── worktrees: Vec<WorktreeState>
│       ├── path, branch, is_bare, host
│       ├── issue: Option<IssueState>     // number, title, state (open/closed)
│       ├── pr: Option<PrState>           // number, state, checks, review, conflicts
│       ├── sessions: Vec<SessionState>   // tmux name, claude state, cost, context %
│       └── display_group: DisplayGroup   // derived at join time, not stored
└── hosts: HashMap<String, HostState>     // reachability per remote host (global, shared across repos)
```

Note: `is_stale` is not stored — it's derivable from `pr.state` and `issue.state` at render time.

### Data sources as modules with free functions

Each data domain is a module with fetch functions. No service objects, no traits. Consistent with ADR-002's preference for free functions.

| Module | Functions | Per-source cache file |
|--------|----------|----------------------|
| `sources::github` | `fetch_issues()`, `fetch_prs()` — single optimized GraphQL query | `{owner}_{repo}_issues.json`, `{owner}_{repo}_prs.json` |
| `sources::worktrees` | `fetch_local()`, `fetch_remote()` | `{owner}_{repo}_worktrees.json`, `{owner}_{repo}_remote_worktrees.json` |
| `sources::tmux` | `fetch_sessions()` — local and remote | `tmux_sessions.json`, `{host}_tmux_sessions.json` |
| `sources::claude` | `read_state_files()` — reads `/tmp/orchard-claude-*.json` directly | No cache file — hook state files ARE the data |
| `sources::hosts` | `probe_reachability()` | In-memory only (ephemeral) |

Caching is handled by the existing per-source cache files from ADR-001. No new cache abstraction needed — the file system IS the cache. Each fetch function reads the cache file, checks `last_refreshed`, and either returns cached data or re-fetches.

### Compositor: a pure function

The compositor is a plain function, not a struct. It reads all per-source caches and joins them into `OrchardState`. This is pure computation over local data.

```rust
/// Reads all per-source caches and joins into a complete OrchardState.
/// This is the single join point — source modules don't know about each other.
fn build_state(config: &GlobalConfig) -> OrchardState {
    // Read from cache files (local I/O, fast)
    let github_data = sources::github::read_cached(config);
    let local_wts = sources::worktrees::read_cached_local(config);
    let remote_wts = sources::worktrees::read_cached_remote(config);
    let sessions = sources::tmux::read_cached(config);
    let claude_states = sources::claude::read_state_files();
    let hosts = sources::hosts::last_known();

    // Join: worktree-first, enrich with PR/issue/session data
    join_all(config, github_data, local_wts, remote_wts, sessions, claude_states, hosts)
}
```

### Join logic (the hard part)

The join is worktree-first with these rules:

1. **Start from non-bare worktrees** (local + remote, tagged by host)
2. **PR matching**: branch name → PR branch. **Skip default branches** (main, master, develop) — they don't represent PR work.
3. **Issue linking**: PR body closing keywords (`Closes #N`) first, then branch name convention (`issue{N}/...`) as fallback.
4. **Session matching**: tmux session working directory == worktree path (exact match). Secondary: session name contains branch slug.
5. **Claude state**: tmux session name → `/tmp/orchard-claude-{session}.json` file lookup.
6. **Display group derivation**: computed from the joined data (same logic as current `derive_display_group`).

Edge cases:
- **Detached HEAD**: no branch → no PR/issue match. Shows as worktree only.
- **Session in wrong directory**: if user `cd`'d elsewhere, session won't match any worktree. Orphaned sessions appear in a separate section.
- **Same branch across repos**: scoped by repo slug — `acme/webapp` and `drewdrewthis/orchard` can both have `main` without collision.

### Two-phase refresh

Instead of per-source TTLs that cascade unpredictably, refresh uses two phases:

1. **Phase 1 (fast, <1s)**: local worktrees, tmux sessions, claude state files. All local I/O.
2. **Phase 2 (slow, 2-30s)**: GitHub API (issues + PRs in one query), remote worktrees (SSH), host probes.

After each phase completes, `build_state` re-joins and the TUI re-renders. Two renders max per refresh cycle, not six staggered ones.

On startup: `build_state` from existing cache files (instant), then kick off both phases.

### `orchard --json`

A versioned `JsonOutput` struct maps from `OrchardState`. This decouples the internal model from the public API.

```rust
#[derive(Serialize)]
struct JsonOutput {
    version: u32,  // bumped on breaking changes
    repos: Vec<JsonRepo>,
    hosts: HashMap<String, JsonHostState>,
}
```

Internal refactors to `OrchardState` don't break scripts. The mapping layer is thin and explicit.

### No display state file

Per ADR-001: no intermediate computed state on disk. On startup, `build_state` reads per-source caches (local file I/O, <10ms) and joins them. This gives instant rendering from cache without a separate derived-state file that can diverge.

The TUI holds the current `OrchardState` in memory. During refresh, previous state is retained for any data source that hasn't updated yet. Remote worktrees that can't be reached keep their previous data but are marked with `host.reachable = false` (rendered dimmed).

## Consequences

- One data model, one code path, one truth
- `--json` output is complete, versioned, and stable for scripting
- TUI renders instantly from cached data, refreshes in two clean phases
- Remote disconnection is graceful (dim, not vanish)
- No service objects or DI — free functions + existing cache files
- The legacy collector and `derive_from_all_caches` are replaced by `build_state`
- Migration: `build_state` initially wraps existing fetch/cache functions, then we optimize

## Alternatives considered

**Keep two pipelines, sync them** — rejected because the root cause is having two representations.

**Service objects with traits and DI** — rejected per ADR-002's spirit. Free functions + cache files achieve the same SRP without the abstraction overhead.

**Per-source TTLs with staggered refresh** — rejected because it creates cascading re-renders. Two-phase refresh is simpler and more predictable.

**Persist `OrchardState` to disk** — rejected per ADR-001 (no computed state on disk). Re-joining from source caches on startup is fast enough.
