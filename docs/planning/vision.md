# Git Orchard — Vision & Direction

## What is this?

A terminal-native dashboard for developers who manage parallel AI coding sessions across git worktrees, local and remote machines.

## The Problem

The AI-first development workflow creates a management problem:

- **5–10 Claude sessions running in parallel**, each in its own git worktree
- **Some local, some on a remote GPU box** via SSH
- **No visibility**: Which Claude is done and waiting for input? Which is stuck? Which PR is failing CI?
- **Context switching tax**: Cycling through tmux sessions, checking GitHub, scrolling terminal output — just to figure out where things stand
- **Session fragility**: Reboots kill tmux sessions, but worktrees survive as orphans with no easy way to resume
- **Worktree sprawl**: Forgotten worktrees from completed work pile up across machines

The developer's primary mode of work is *managing* Claude sessions, not writing code directly. Claude does the implementation; the developer provides direction, reviews output, unblocks stuck agents, and decides what to work on next.

## Core Insight

Orchard is **`Ctrl+B s` with intelligence**. Tmux already gives you sessions, previews, and switching. What it doesn't give you:

- Is Claude done and waiting for me?
- Is CI failing on this branch's PR?
- Are there review comments or requested changes I need to address?
- Which issue is this session working on?
- Is this session local or on a remote box?
- What did Claude say last? (without switching to that session)

Orchard surfaces all of this in a single dashboard. You glance, you know where to spend your next five minutes, you press Enter, you're there.

## The Primary Unit: Worktrees

**Worktrees are the persistent anchor.** Everything else — tmux sessions, Claude processes, PRs — is metadata attached to worktrees.

```
Worktree (the durable workspace)
  ├── Tmux session(s) (ephemeral, auto-resurrected)
  │     ├── Claude status: working / done / waiting for input / stuck
  │     ├── Pane preview: last N lines of output
  │     └── Session count (multiple Claudes per worktree)
  ├── Branch → PR (via GitHub API)
  │     ├── CI status (passing / failing / pending)
  │     ├── Review decision (approved / changes requested)
  │     ├── Unresolved threads (CodeRabbit, human reviewers)
  │     ├── Merge conflicts
  │     └── Open comments count
  ├── Branch name → Issue number (by convention: `langwatch-2478`)
  │     └── Issue title, state (open/closed)
  └── Host (local vs @remote)
```

The dashboard starts from **worktrees that exist** (local + remote), enriched with metadata. It does NOT start from GitHub issues — issues without active worktrees are not shown. GitHub is the issue tracker; Orchard is the workforce dashboard.

## The GitHub Issue Lifecycle

Issues are the **origin of intent**. Every piece of work starts as a GitHub issue:

1. An issue is created (manually, or via Claude from a description)
2. The issue number drives naming: branch `langwatch-2478`, worktree `.worktrees/langwatch-2478`
3. A PR is opened from that branch, linking back to the issue
4. PR status (CI, reviews, conflicts) is tracked until merge
5. When the issue is closed → Orchard surfaces the worktree for cleanup (delete worktree, branch, session — locally and remotely)

This convention-based linking (issue → branch name → worktree → PR) avoids stored state. The names ARE the links.

## The Shepherd Session

Each repo has a persistent `repo_main` tmux session (e.g., `langwatch_main`) at the repo root. Auto-created on startup, always exists, pinned at the top of the dashboard.

The shepherd is where the developer talks to Claude as a **manager**:
- "What's going on across all sessions?"
- "Launch issue 123 remotely"
- "Create an issue for this bug I just found"
- "Check on the session that's been running for 2 hours"

The shepherd is NOT an Orchard feature — it's a usage pattern. Orchard just ensures the session exists and exposes data (cache files, `--json` output) that the shepherd Claude can read. The intelligence is Claude, not Orchard.

**"Let Claude do it, automate away from Claude later if needed."** Claude creates PRs with great descriptions because it has context. Claude handles edge cases in worktree creation because it can reason about them. Scripting these operations is an optimization for later, not v1.

## Display Groups

The dashboard groups worktrees by what needs attention:

| Group | Condition | Color |
|-------|-----------|-------|
| **Needs you** | Claude done/waiting for input, CI failing, review comments, changes requested, unresolved threads | Red |
| **Claude working** | Claude actively generating output — leave it alone | Green |
| **Ready to merge** | PR approved, CI passing, no conflicts, no unresolved threads | Cyan |
| **Other** | Shepherd session, sessions without PRs, miscellaneous | DarkGray |

## Claude Detection

The most important signal: **which Claude needs me?**

Current mechanism (binary — running/not running):
- Check `pane_commands` and `pane_titles` for "claude" substring

Target mechanism (richer states):
- **Working**: Pane content is changing (output flowing)
- **Done/idle**: Claude's prompt pattern visible in last few lines of pane, no recent output
- **Waiting for input**: Question mark or y/n prompt detected in pane content
- **Errored**: Claude process exited or crash pattern detected

This is heuristic and ~80% accurate. That's dramatically better than no signal. The cost of a false positive is 10 seconds of checking; the cost of no signal is manually cycling through 7 sessions.

Long-term: advocate for Claude Code to expose a machine-readable status signal (status file, tmux title protocol).

## Desktop Notifications

**Highest-leverage feature for the manager workflow.** Drew doesn't have to poll the dashboard — the dashboard tells him:

- Claude transitioned from "working" to "done" → macOS notification
- CI failed on a branch → notification
- Review comments added to a PR → notification

Triggered by state transitions detected during cache refresh, not current state (to avoid spam).

## Session Persistence

Tmux sessions are ephemeral. Worktrees are durable. On reboot:

1. Scan all worktrees (local + remote)
2. For worktrees that had sessions previously (tracked via lightweight manifest), recreate sessions
3. Don't auto-resurrect ALL worktrees (50 sessions is chaos) — only recently-active ones
4. For worktrees where Claude was the last thing running, consider restarting Claude with `claude --continue`

**Open question:** Try `tmux-resurrect` plugin first — it may solve 80% of this with zero custom code.

## Cleanup

When an issue is closed or a PR is merged, the associated worktree is stale. Orchard's cleanup view:

- Surfaces stale worktrees (merged/closed PRs, closed issues)
- Lets the user select which to clean up
- Deletes: worktree, branch, tmux session — locally and remotely
- Already implemented and working

## Remote Support

Worktrees and sessions can be on a remote machine (GPU box via SSH):

- SSH connectivity probing with reachability indicators
- Unreachable remote rows dimmed with stale indicator
- Remote pane content cached via SSH for preview without manual SSH
- One-key reconnect when hosts come back online
- Enter guard: warning instead of hanging when remote is unreachable

## Architecture

### Per-source caches, derived display (ADR-001)

No user-managed state. Each external data source owns a cache file under `~/.cache/orchard/`. Display groups are derived at render time by joining caches.

### Join chain (worktree-first)

1. Start from **worktrees** (local + remote) — the things that actually exist
2. For each worktree, match branch name to a PR (via GitHub PR cache)
3. For each PR, get CI status, review decision, unresolved threads, conflicts
4. For each worktree path, match to tmux sessions (local + remote session caches)
5. For each session, detect Claude status via pane inspection
6. Optionally, match branch name to issue number by naming convention
7. Derive display group from the joined data
8. Worktrees with no PR/session still show (they're real — just in "other" group)

### What Orchard is NOT

- **Not an issue tracker.** GitHub is the issue tracker. Orchard doesn't show unstarted issues.
- **Not a task board.** GitHub Projects is the board. Orchard shows active workspaces.
- **Not an automation platform.** Claude is the automation. Orchard observes and switches.
- **Not a CI dashboard.** CI status enriches worktree rows, but Orchard isn't where you debug CI.

## Persona

### AI-First Solo Developer (primary)

Works across 1–5 repos. Uses GitHub issues for planning. Delegates ALL implementation to Claude agents. Manages 5–10 parallel Claude sessions across local and remote machines. Needs visibility into what's running, what's done, what's blocked — and the ability to jump into any conversation instantly.

The developer's primary tool is Claude, not an editor. Orchard is the switchboard for managing the AI workforce.

## Open Questions

- **Claude status API:** Can Claude Code expose a machine-readable status signal? A status file, socket, or tmux title convention would make detection reliable instead of heuristic.
- **Session resurrection scope:** How many sessions should auto-resurrect on reboot? All recent? Only those with unsaved work?
- **tmux-resurrect:** Does the tmux-resurrect plugin solve session persistence well enough that Orchard doesn't need to?
- **Notification UX:** How aggressive should notifications be? Every Claude completion, or only when something needs attention?
- **Multi-repo view:** Show all repos in one view, or per-repo with a repo switcher?

## Inspirations

- **tmux `Ctrl+B s`** — The starting point. Orchard is this, but with intelligence.
- **Grafana** — Read-only dashboards derived from external data sources. Same philosophy.
- **Kanban boards** — Status-grouped views. Needs attention → in progress → done.
- **Lazygit** — Proof that complex workflows work great in TUI.

## What This Doc Is For

This is the living document for product direction. Not implementation specs (those go in `specs/features/`), not architecture decisions (those go in ADRs). This is for:

- Articulating *why* we're building what we're building
- Capturing the corrected product model as understanding deepens
- Keeping the big picture visible as the project evolves
