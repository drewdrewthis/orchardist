# Cache Architecture

> Authoritative specification: `docs/adr/001-cache-architecture.md`. This document is a concise planning overview.

## Core Insight

There is no user-managed state. Everything is derived from external services (GitHub, git, tmux). The app is a **read-only dashboard** that caches service data for instant rendering.

## Correct Architecture

Each data source owns its own cache. Orchard reads all caches on startup, renders instantly, then refreshes each source in the background.

### Data Sources

| Source | What it provides | Cache file |
|--------|-----------------|------------|
| GitHub Issues | issue number, title, state (open/closed), labels | `~/.cache/orchard/{owner}_{repo}_issues.json` |
| GitHub PRs | PR number, state, review decision, checks, conflicts, unresolved threads | `~/.cache/orchard/{owner}_{repo}_prs.json` |
| Git Worktrees | worktree paths, branches, bare/conflict status | `~/.cache/orchard/{owner}_{repo}_worktrees.json` |
| Tmux Sessions (local) | session names, paths, pane titles, commands | `~/.cache/orchard/tmux_sessions.json` |
| Tmux Sessions (remote) | same, polled via SSH per host | `~/.cache/orchard/{host}_tmux_sessions.json` |
| Remote Worktrees | same as worktrees but via SSH | `~/.cache/orchard/{owner}_{repo}_remote_worktrees.json` |

Each cache file includes a `last_refreshed` ISO-8601 timestamp in its metadata. Failed API calls never overwrite an existing cache file with empty data — the previous cache is left in place.

### Derived View (not stored)

Display groups are **computed at render time** by joining the cached data. The join is worktree-first: start from what exists, enrich with metadata.

**Join chain (worktree-first):**

1. Start from **worktrees** (local + remote) — the things that actually exist. Skip bare worktrees.
2. For each worktree, match its **branch name to a PR** (via `{owner}_{repo}_prs.json`, matching `branch` field).
3. For each PR, extract: CI status, review decision, unresolved threads, conflicts, open comments.
4. For each worktree **path**, match to **tmux sessions** by working directory (`tmux_sessions.json` for local; `{host}_tmux_sessions.json` for remote).
5. For each matched session, **detect Claude status** from pane titles and commands. Optionally capture pane content for richer state detection (working/done/waiting).
6. Optionally, match **branch name to issue number** by naming convention (e.g., `webapp-2478` → issue #2478). Look up issue title and state from `{owner}_{repo}_issues.json`.
7. **Derive display group** from the joined data:
   - `needs_attention` — Claude done/waiting for input, PR has changes requested, merge conflicts, failing CI, or unresolved threads.
   - `claude_working` — Claude actively running and generating output (leave it alone).
   - `ready_to_merge` — PR approved, checks passing, no conflicts, no unresolved threads.
   - `other` — Shepherd session, worktrees without PRs, miscellaneous.
8. **Worktrees with no PR or session still show** — they're real workspaces. They appear in the "other" group, not invisible.

**What is NOT shown:**
- Unstarted GitHub issues (issues with no worktree, no PR, no session). GitHub is the issue tracker; Orchard shows active workspaces.
- Closed issues (their worktrees surface in the cleanup view instead).

### Startup Flow

```
1. Read all cache files (instant — just file reads)
2. Join worktrees → PRs → sessions → derive display → render TUI immediately
3. Background: refresh each source in parallel
   - git worktree list     → write worktrees cache
   - tmux list-sessions    → write local sessions cache
   - SSH remote poll       → write remote worktrees + {host} sessions cache
   - gh pr list            → write PRs cache
   - gh issue list         → write issues cache (for title/state enrichment)
   Each write only occurs on success with actual data; failures leave the previous cache in place.
4. On each source update: re-join, re-derive, re-render
```

### Multi-Repo

The config declares repos to manage:
```json
// ~/.orchard/config.json
{
  "repos": [
    { "slug": "acme/webapp", "path": "/home/user/workspace/webapp", "remote": { "host": "ubuntu@10.0.0.1", "path": "/home/ubuntu/webapp-workspace" } },
    { "slug": "drewdrewthis/orchardist", "path": "/home/user/workspace/orchardist" }
  ]
}
```

Each repo's sources are refreshed independently. The TUI shows worktrees from ALL repos, grouped by display status.

### Session Manifest (lightweight persistence)

A small file tracking which worktrees had active sessions at last refresh:
```json
// ~/.cache/orchard/session_manifest.json
{
  "sessions": [
    { "name": "webapp_webapp-2478", "path": "/path/to/worktree", "had_claude": true },
    { "name": "webapp_main", "path": "/path/to/repo", "had_claude": false }
  ]
}
```

Written on each refresh cycle. Read on startup for session resurrection decisions. Only worktrees in the manifest get their sessions recreated after a reboot — prevents 50-session chaos.

### Desktop Notifications

State transitions detected during refresh trigger macOS notifications:
- Claude: working → done/idle → notify
- CI: passing → failing → notify
- PR: no comments → new comments/changes requested → notify

Compare previous derived state with current derived state. Fire on transitions, not current state. Respect cooldown to avoid spam.

### What Gets Removed

- `state.json` — replaced by per-source caches.
- `AppState` and `Task` structs in `src/state.rs` — replaced by cache-derived join.
- `TaskStatus` enum — replaced by derived `DisplayGroup`.
- `issue_sync.rs` — replaced by GitHub caches.
- Issue-first join chain — replaced by worktree-first join.

### What Stays

- `events.jsonl` — structured event log (observability, not state)
- `status.txt` — tmux status bar segment (written from derived data)
- The TUI rendering, key handlers, popup model
- Remote worktree support, session switching
- Cleanup view for stale worktrees
- Per-repo `.git/orchard.json` for backwards-compatible remote host config
