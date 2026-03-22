# ADR-001: Per-Source Cache Architecture

## Status
Proposed

## Date
2026-03-20

## Context

Orchard is evolving from a single-repo worktree manager into a terminal-native task and session command center. Tasks (GitHub issues) are the primary unit of work; git worktrees and tmux sessions are resources attached to tasks.

The current `state.json` (at `~/.local/state/git-orchard/state.json`) attempts to serve two conflicting roles simultaneously: an in-process cache for live data and a user-editable state store. This dual role causes several problems:

- **Status drift.** Stored `TaskStatus` (e.g. `InProgress`) diverges from actual live state (the tmux session died, the PR merged) with no automatic reconciliation.
- **Accidental mutations.** Key bindings like `s` (start) and `d` (done) write permanent status changes. A mispress requires manual JSON surgery to correct.
- **Fragile recovery.** If the file is corrupted or deleted, task state is lost; there is nothing to fall back to because the ground truth lives in external services.
- **Single-repo scoping.** `AppState` loads relative to CWD, making it impossible to show tasks from multiple repos in one view.
- **Mixed abstraction.** `TaskStatus`, `issue_sync.rs`, and `merge_worktrees_into_tasks` are all bridging logic that exists solely because status is stored rather than derived.

The existing types in `src/state.rs` make this concrete: `AppState` holds a flat `Vec<Task>`, each `Task` carries a `TaskStatus` enum and directly stored fields (`worktree`, `sessions`, `pr`, `remote_host`) that duplicate live data from git, tmux, and GitHub.

## Decision

Replace `state.json` with a set of per-source cache files. Each external data source owns exactly one cache file. The display model (task rows, status groups) is derived at render time by joining the caches — nothing is stored that can be recomputed from live services.

### Data Sources and Cache Files

| Source | Data provided | Cache file |
|--------|--------------|------------|
| GitHub Issues | issue number, title, open/closed state, labels | `~/.cache/orchard/{owner}_{repo}_issues.json` |
| GitHub PRs | PR number, state, review decision, CI checks, merge conflicts, unresolved threads | `~/.cache/orchard/{owner}_{repo}_prs.json` |
| Git Worktrees | worktree paths, branch names, bare/conflict status | `~/.cache/orchard/{owner}_{repo}_worktrees.json` |
| Tmux Sessions | session names, paths, pane titles, running commands | `~/.cache/orchard/tmux_sessions.json` (local), `~/.cache/orchard/{host}_tmux_sessions.json` (per remote host) |
| Remote Worktrees | same as Git Worktrees but polled over SSH | `~/.cache/orchard/{owner}_{repo}_remote_worktrees.json` |

The `{owner}_{repo}` prefix scopes local caches per repository, enabling multi-repo operation from a single TUI instance. Tmux session caches are split by host: local sessions go in `tmux_sessions.json`; remote sessions are polled via SSH and stored in `{host}_tmux_sessions.json`, one file per configured remote host. Each cache file records a `last_refreshed` ISO-8601 timestamp in its metadata.

### Derived View (computed, not stored)

Display groups (`needs_attention`, `claude_working`, `ready_to_merge`, `other`) are computed at render time by joining the source caches. The join is worktree-first: start from what exists, enrich with metadata. Gaps produce empty fields rather than errors.

Join chain:

1. Start from **worktrees** (local + remote) — the things that actually exist. Skip bare worktrees.
2. For each worktree, match its branch name to a **PR** (via `{owner}_{repo}_prs.json`).
3. For each PR, extract: CI status, review decision, unresolved threads, conflicts.
4. For each worktree path, match to **tmux sessions** by working directory (local or `{host}` sessions).
5. For each matched session, detect **Claude status** from pane titles, commands, and optionally pane content.
6. Optionally, match branch name to **issue number** by naming convention (e.g., `langwatch-2478` → issue #2478).
7. Derive display group from joined data:
   - `needs_attention` — Claude done/waiting for input, PR has changes requested, merge conflicts, failing CI, or unresolved threads.
   - `claude_working` — Claude actively running (leave it alone).
   - `ready_to_merge` — PR approved, checks passing, no conflicts, no unresolved threads.
   - `other` — Shepherd session, worktrees without PRs, miscellaneous.
8. **Worktrees with no PR or session still show** — in the "other" group, not hidden.

Unstarted GitHub issues (no worktree, no PR) are NOT shown in the main view. GitHub is the issue tracker; Orchard shows active workspaces. Closed issues trigger the cleanup view for worktree removal.

No intermediate computed state is written to disk.

### Startup Flow

```
1. Read all cache files (instant — file reads only, no network)
2. Join & derive → render TUI immediately with last-known data
3. Background: refresh each source in parallel
   - gh issue list        → write issues cache (open issues only)
   - gh pr list           → write PRs cache (with linkedIssues)
   - git worktree list    → write worktrees cache
   - tmux list-sessions   → write local sessions cache
   - SSH remote poll      → write remote worktrees cache + {host} sessions cache
   Each write only occurs on success with actual data; failures leave the previous cache file in place.
4. On each source update: re-join, re-derive, re-render, write updated cache
```

Stale cache renders fine on startup (last-known state). Background refresh converges to current state within seconds.

### Multi-Repo Configuration

A global config at `~/.config/orchard/config.json` declares all managed repos:

```json
{
  "repos": [
    {
      "slug": "owner/repo-a",
      "path": "/Users/USER/workspace/repo-a",
      "remote": { "host": "ubuntu@10.0.0.1", "path": "/home/ubuntu/repo-a-workspace" }
    },
    {
      "slug": "owner/repo-b",
      "path": "/Users/USER/workspace/repo-b"
    }
  ]
}
```

Each repo's sources are refreshed independently. The TUI aggregates tasks from all repos into a single view grouped by display status.

### What Gets Removed

- `state.json` — replaced by per-source cache files.
- `AppState` and `Task` structs in `src/state.rs` — the monolithic task record that tried to own all fields.
- `TaskStatus` enum (`Backlog`, `Ready`, `InProgress`, `InReview`, `Done`) — replaced by a derived `DisplayGroup` computed from live source data.
- `issue_sync.rs` — replaced by a GitHub Issues cache source with its own refresh logic.
- `merge_worktrees_into_tasks` — replaced by the render-time join described above.

### What Stays

- `events.jsonl` — structured event log for observability; not state, not replaced.
- `status.txt` — tmux status bar segment, written from derived render data.
- TUI rendering, key handlers (`enter`, `o`, `c`, `q`), popup model, and wrapper script.
- Remote worktree support and session switching.
- Per-repo `.git/orchard.json` for backwards-compatible remote host config.

## Consequences

### Positive

- **No status drift.** Every render reflects what external services actually report. There is nothing stored that can disagree with reality.
- **Safe by default.** No key press can permanently corrupt task state; the worst a user can do is navigate.
- **Instant startup.** Cache reads are local file I/O, so the TUI renders immediately regardless of network or GitHub availability.
- **Multi-repo native.** Cache files are scoped per repo by design; adding a new repo adds a new set of cache files with no schema migration.
- **Simpler code.** Removing `TaskStatus`, `issue_sync.rs`, and `merge_worktrees_into_tasks` eliminates a significant layer of bridging logic.
- **Independently evolvable sources.** Each cache source can be refreshed, retried, or replaced without touching the others.

### Negative

- **Significant refactor.** `state.rs`, `issue_sync.rs`, and parts of `main.rs` and the TUI must be rewritten. Existing tests for state serialization are invalidated.
- **Cold start shows stale data.** On first run (empty caches) or after clearing caches, the TUI shows nothing until the first background refresh completes. This is a worse first-run experience than the current state where tasks are explicitly persisted.
- **Cache staleness is visible.** If the user runs Orchard while offline or GitHub is down, the TUI shows whatever the last successful refresh captured. Users must understand that the display may lag behind reality.
- **No user-managed ordering.** Removing manual priority (`p` key) means the task order is whatever GitHub's API returns. Users who relied on local reordering lose that capability.

### Risks

- **Cache directory permissions.** If `~/.cache/orchard/` is not writable, all caches fail silently. Mitigation: check and warn on startup; degrade gracefully to in-memory-only refresh results.
- **Concurrent refresh races.** Multiple source refreshes writing cache files simultaneously could produce a briefly inconsistent joined view. Mitigation: each source owns exactly one file; joins read atomically by loading all files before joining; no cross-file transactions needed.
- **Breaking existing state.** Users with tasks persisted in `state.json` lose that data on migration. Mitigation: document the migration in release notes; the data is reconstructible from GitHub.
- **SSH availability for remote caches.** Remote worktree polling depends on SSH connectivity. Mitigation: remote cache is optional; a failed SSH poll leaves the previous cache in place and does not block rendering.
