# ADR-004: Unified service architecture with display cache

## Status

Proposed

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

## Decision

### Single data model: `OrchardState`

One struct that represents everything orchard knows. Both `--json` and the TUI consume this directly.

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
│       ├── display_group: DisplayGroup
│       └── is_stale: bool               // PR merged, issue closed, etc.
└── hosts: Vec<HostState>                 // reachability per remote host
```

### Service layer (SRP)

Each service owns one data domain. Each has a `fetch` method that returns its typed data and manages its own caching policy.

| Service | Responsibility | Cache policy |
|---------|---------------|-------------|
| `GitHubService` | Issues, PRs, check runs — single optimized GraphQL query | 60s TTL, background refresh |
| `LocalWorktreeService` | `git worktree list` for local repos | 30s TTL, fast |
| `RemoteWorktreeService` | SSH + `git worktree list` on remote hosts | 120s TTL, skip when unreachable |
| `TmuxService` | Local and remote tmux session listing + pane content | 10s TTL, fast |
| `ClaudeStateService` | `/tmp/orchard-claude-*.json` hook state files | No cache — read on demand (files ARE the cache) |
| `HostProbeService` | SSH reachability checks | 120s TTL |

### Compositor

The `Compositor` calls all services, joins results by worktree path and branch name, and produces `OrchardState`. This is the single join point — services don't know about each other.

```rust
impl Compositor {
    fn build_state(&self, config: &GlobalConfig) -> OrchardState {
        let github = self.github.fetch(config);
        let local_wts = self.local_worktrees.fetch(config);
        let remote_wts = self.remote_worktrees.fetch(config);
        let sessions = self.tmux.fetch(config);
        let claude = self.claude_state.fetch();
        let hosts = self.host_probe.fetch(config);

        // Join everything into OrchardState
        join_all(config, github, local_wts, remote_wts, sessions, claude, hosts)
    }
}
```

### Display cache

The TUI holds the last `OrchardState` and renders from it immediately on startup (from disk cache). Background refresh produces a new `OrchardState` which replaces the old one. During refresh:

- Data that hasn't been refreshed yet retains its previous value
- Remote data that can't be reached is marked `stale: true` (rendered dimmed, not removed)
- The UI never shows an empty state after the first load

The display cache is persisted to `~/.cache/orchard/display_state.json` so even a fresh `orchard` launch shows something immediately.

### `orchard --json`

Serializes `OrchardState` directly. Same data the TUI uses. Includes both derived fields (`display_group`, `is_stale`) and raw data (full PR details, check runs, issue state).

## Consequences

- One data model, one code path, one truth
- `--json` output is complete and useful for scripting
- TUI never flickers — always has something to show
- Remote disconnection is graceful (dim, not vanish)
- Services are independently testable via traits
- The legacy collector and `derive_from_all_caches` are replaced by the Compositor
- Migration: the Compositor initially wraps existing fetch functions, then we can optimize (e.g., single GraphQL query for issues+PRs+checks)

## Alternatives considered

**Keep two pipelines, sync them** — rejected because the root cause is having two representations. Syncing them is more work than unifying.

**GraphQL-style query planner** — considered but overkill. The resolver pattern gives us the same benefits (services don't know about each other, compositor joins) without the query language overhead.

**Event sourcing / streaming** — considered for real-time updates but adds complexity. Poll-based refresh with display cache is simpler and good enough for 10-60s refresh cycles.
