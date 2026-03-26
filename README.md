# git-orchard 🌲🌳🌴

A command center for managing git worktrees, tmux sessions, GitHub PRs, and Claude Code sessions across multiple repositories.

Built with [Rust](https://www.rust-lang.org/) + [Ratatui](https://ratatui.rs/).

![License](https://img.shields.io/badge/license-MIT-blue)

## What it does

Orchard gives you a single dashboard showing everything happening across your repos: which worktrees have PRs, what state they're in, which Claude sessions are working/idle/waiting for input, and what needs your attention.

```
┌ TASKS — langwatch/langwatch ─────────────────────────────────────────────┐
│  # ISSUE  TITLE                        BRANCH           STATUS    CLAUDE │
│ ──── shepherd ─────────────────────────────────────────────────────────── │
│  1        langwatch                    main             no PR     ● idle │
│ ──── needs attention ─────────────────────────────────────────────────── │
│  2 #2507  Add ability to rename code   …rename-code     #2552 ✖ failing │
│  3 #2600  Structured logging           …clickhouse      #2606 ✖ failing │
│ ──── other ───────────────────────────────────────────────────────────── │
│  4 #2669  Clean up dead config         …agent-bloat     #2671 ○ review  │
└──────────────────────────────────────────────────────────────────────────┘
```

## Features

- **Unified dashboard** — worktrees, PRs, issues, tmux sessions, and Claude state in one view
- **Multi-repo** — switch between projects with left/right arrows
- **Claude Code integration** — see which sessions are working, idle, or waiting for input via hooks
- **Smart grouping** — shepherd (main sessions), needs attention, claude working, ready to merge, other
- **Priority toggle** — flag worktrees as priority to keep them at the top
- **PR status** — review decisions, CI checks, merge conflicts, unresolved threads
- **Issue state** — closed issues flagged for cleanup
- **Cleanup** — select and delete worktrees with merged PRs or closed issues
- **Click-to-switch notifications** — desktop notifications when Claude finishes; click to jump to the session
- **Remote worktrees** — manage worktrees on remote machines via SSH, with reachability indicators
- **JSON output** — `orchard --json` returns complete, fresh data for scripting
- **Auto-refresh** — background refresh with two-phase loading (fast locals, then slow remotes)

## Install

### From source

```bash
cargo install --git https://github.com/drewdrewthis/git-orchard-rs
```

### Setup

```bash
orchard init
```

This will:
1. Add the `orchard` shell function to your rc file (launches inside tmux)
2. Set up a tmux keybinding (default: `Ctrl-o`)
3. Optionally add orchard status to your tmux status bar
4. Install Claude Code hooks for session state detection

## Usage

```
orchard                    Interactive dashboard
orchard cleanup            Jump straight to cleanup view
orchard init               Setup wizard
orchard --json             Full state as JSON (always fresh, never cached)
```

### Keybindings

| Key | Action |
|-----|--------|
| `↑/↓` or `j/k` | Navigate worktrees |
| `←/→` | Switch between repos |
| `Enter` | Switch to tmux session (creates one if needed) |
| `1-9` | Jump to worktree by number |
| `o` | Open PR in browser |
| `i` | Open issue in browser |
| `p` | Toggle priority flag |
| `d` | Delete worktree |
| `c` | Cleanup stale worktrees |
| `f` | Cycle filter mode |
| `/` | Search |
| `r` | Refresh |
| `R` | Reconnect (SSH) |
| `?` | Help |
| `q` | Quit |

## Configuration

### Global config

`~/.config/orchard/config.json` — register multiple repos:

```json
{
  "repos": [
    {
      "slug": "owner/repo",
      "path": "/path/to/repo",
      "remotes": [
        {
          "name": "gpu",
          "host": "user@server",
          "path": "/remote/path",
          "shell": "ssh"
        }
      ]
    }
  ]
}
```

If no global config exists, orchard auto-detects the current repo via `gh repo view`.

### Per-repo config

`.orchard.json` in the repo root (committable, team-shared):

```json
{
  "ci": {
    "ignore": ["codecov/patch", "deploy-preview"],
    "required": ["test", "build"]
  }
}
```

`.git/orchard.json` for local-only overrides (remotes, personal preferences).

See [ADR-003](docs/adr/003-per-repo-config.md) for the full design.

## Architecture

Orchard follows a **Functional Core, Imperative Shell** pattern:

- **Source modules** fetch data from git, GitHub, tmux, SSH, and Claude hooks
- **`build_state()`** is a pure function that joins all sources into a single `OrchardState`
- **TUI** and **`--json`** both consume the same `OrchardState`

See [docs/architecture.md](docs/architecture.md) for the full architecture guide.

## Claude Code Integration

Orchard detects Claude session state via hooks (not terminal scraping):

```bash
orchard init  # installs hooks automatically
```

The hooks write structured JSON on every Claude event (tool use, stop, notification). Orchard reads these for accurate working/idle/input detection, plus context window usage and cost tracking.

## Requirements

- Git
- tmux
- [GitHub CLI](https://cli.github.com/) (`gh`) — for PR/issue data
- [terminal-notifier](https://github.com/julienXX/terminal-notifier) — for click-to-switch notifications (optional, falls back to osascript)

## License

MIT
