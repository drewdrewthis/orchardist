---
name: install-orchard
description: "Install and set up Orchard — guided setup for new users. Use when someone says 'install orchard', 'get started', 'set up orchard', 'how do I use this', or is clearly new to the project."
user-invocable: true
argument-hint: ""
---

# Install Orchard

Interactive setup for new Orchard users. Adapts based on experience level. Agent-agnostic — Orchard works with any coding agent, not just Claude Code.

## Step 1: Welcome & Environment Check

Greet the user and check prerequisites by running these commands:

```bash
git --version
tmux -V
gh --version
node --version
```

Report what's installed and what's missing. If anything is missing, explain how to install it before continuing:
- **git**: system package manager or https://git-scm.com
- **tmux**: `brew install tmux` (macOS), `apt install tmux` (Linux)
- **gh**: `brew install gh` (macOS), or https://cli.github.com
- **node/npm**: `brew install node` (macOS), or https://nodejs.org

Don't proceed until all four are available.

## Step 2: Tmux Familiarity

Ask the user:

> **How familiar are you with tmux?**
> - **I use it daily** — skip the intro
> - **I've used it but I'm rusty** — quick refresher
> - **Never used it** — explain the basics

If they're new to tmux, give a brief orientation:
- tmux keeps terminal sessions alive in the background
- Orchard creates one tmux session per worktree — each is an isolated workspace
- Key concepts: sessions (named workspaces), windows (tabs), panes (splits)
- They don't need to master tmux — Orchard manages sessions for them
- The main thing they'll do: `tmux attach -t <session>` to jump into a workspace

## Step 3: Install Orchard

Install via npm (downloads the pre-built binary automatically):

```bash
npm install -g git-orchard
```

Verify it works:

```bash
orchard --help
```

**Alternative — build from source** (if npm isn't available or they prefer it):

```bash
git clone https://github.com/drewdrewthis/git-orchard-rs.git
cd git-orchard-rs
cargo build --release
ln -sf "$(pwd)/target/release/orchard" ~/.local/bin/orchard
```

## Step 4: Configure a Repo

Ask which repo they want to manage with Orchard (it can be this one or any other).

Explain the two config files:
- **`.orchard.json`** — committed to the repo, shared team config
- **`.git/orchard.json`** — local-only, personal overrides (SSH hosts, etc.)

Create a minimal `.orchard.json` if one doesn't exist:

```json
{
  "repo": "owner/repo-name"
}
```

Then run `orchard` in that repo to verify the TUI loads.

## Step 5: Telegram Setup (The Wow Moment)

Explain what this enables: a Telegram bot that lets the orchardist — a persistent background session running your coding agent — message you with status updates, ask questions, and report on your worktrees.

Walk them through it:

**1. Create a bot with @BotFather on Telegram**
- Open https://t.me/BotFather, send `/newbot`
- Pick a display name and a username ending in `bot`
- Copy the token (looks like `123456789:AAHfiqk...`)

**2. Install the Telegram plugin (Claude Code users)**
```
/plugin install telegram@claude-plugins-official
```

**3. Configure with the token**
```
/telegram:configure <paste-token-here>
```

This writes the token to `~/.claude/channels/telegram/.env`.

**4. Relaunch with the channel flag**
```bash
claude --channels plugin:telegram@claude-plugins-official
```

**5. Pair your account**
- DM your new bot on Telegram — it replies with a 6-character code
- In Claude Code: `/telegram:access pair <code>`

**6. Lock it down**
- `/telegram:access policy allowlist` — so only you can reach the bot

Tell the user: once paired, the orchardist can send you messages on Telegram when work completes, PRs need attention, or CI fails.

## Step 6: Resuming Sessions (Claude Code Users)

If the user is running Claude Code, explain the `--continue` flag:

```bash
# Resume the most recent session
claude --continue

# Resume a specific session
claude --continue "orchard setup"
```

To keep Telegram connected on resume:
```bash
claude --continue --channels plugin:telegram@claude-plugins-official
```

This means they can close their terminal, reboot, and pick up exactly where they left off — the orchardist session stays alive in tmux, and `--continue` reconnects them to it.

## Step 7: What's Next

Based on what they want to do, point them to:

- **Manage worktrees**: just run `orchard` — the TUI shows everything
- **Create a worktree for an issue**: create manually with `git worktree add`
- **See all sessions**: the Orchard TUI dashboard
- **JSON output for scripting**: `orchard --json`
- **Architecture deep-dive**: read `docs/architecture.md`

Ask if they have questions. The goal is they leave with a working Orchard install, a configured repo, and optionally Telegram notifications running.
