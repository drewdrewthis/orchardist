# ADR-005: Orchard's role in the worktree lifecycle

## Status

Proposed

## Context

Tools like [worktrunk](https://github.com/max-sixty/worktrunk) manage the full worktree lifecycle: create, switch, merge, remove, with hooks for automation. Orchard currently occupies a different layer — it's a read-only dashboard that monitors worktrees, enriches them with PR/issue/session state, and provides cleanup for stale ones.

The question: should Orchard grow into a worktree lifecycle manager, or stay focused on monitoring and orchestration visibility?

Today, creating a worktree for a task involves:
1. Manually running `git worktree add` (or a wrapper like worktrunk)
2. Opening a tmux session in the new worktree
3. Launching a Claude Code session with `/launch` or `/implement`

Orchard sees the result of all three steps but doesn't initiate any of them (except creating tmux sessions on `Enter` for existing worktrees).

### Why this matters

Worktree creation is project-specific. Some teams want:
- A bare `git worktree add` and nothing else
- A tmux session auto-created in the new worktree
- Claude Code launched immediately with a GitHub issue context
- Hooks that run setup scripts (install deps, copy `.env` files, symlink build caches)

There's no single "right" workflow. Worktrunk solves this with a configurable hook system. Orchard would need something similar if it took on creation.

## Decision

**Orchard stays focused on monitoring and visibility.** It does not manage the worktree lifecycle (create, merge). It may delegate to external tools for creation in the future, but does not implement its own lifecycle commands.

Rationale:

1. **Different products, different strengths.** Orchard's value is the unified dashboard — seeing everything at a glance across repos, sessions, PRs, and Claude state. Worktree lifecycle management is a solved problem (git itself, worktrunk, custom scripts). Duplicating it adds maintenance burden without unique value.

2. **Creation workflows are project-specific.** A Rust project needs `target/` symlinks. A Node project needs `node_modules/`. A monorepo needs selective sparse checkout. Encoding all of this in Orchard's core would fight against YAGNI and KISS.

3. **Composability over completeness.** Orchard already works with any worktree creation method — it discovers worktrees from git, not from its own state. Users can pair it with worktrunk, plain git, or custom scripts. This is a feature, not a gap.

### What Orchard *may* do in the future

- **Trigger creation via configurable command.** A keybinding in the TUI that shells out to a user-defined command (e.g., `wt switch -c`, a custom script, or `git worktree add`). Orchard provides the UI affordance; the user provides the implementation. This would require a config option like `worktree_create_command` in `.orchard.json`.

- **Surface build cache guidance.** Worktrees that don't share `target/` or `node_modules/` waste disk and compile time. Orchard could detect this and suggest symlinks or `.worktreeinclude`-style sharing during `orchard init`.

- **LLM-generated worktree summaries.** Enriching dashboard rows with AI-generated descriptions of what each worktree is doing (based on diffs or commit messages). Deferred due to cost concerns and TUI layout challenges — needs a detail pane or on-demand expansion, not inline text.

## Consequences

- Orchard remains a thin, focused tool: monitor, display, cleanup
- Users choose their own worktree creation workflow
- No dependency on or competition with worktrunk
- Future creation support, if added, will be delegation (configurable shell-out), not reimplementation
- Build cache sharing and LLM summaries are tracked as separate future considerations

## Alternatives considered

**Implement full lifecycle (create, switch, merge, remove)** — rejected. This is worktrunk's domain. Reimplementing it would be significant scope creep for marginal benefit, since Orchard's users already have worktree creation workflows.

**Depend on worktrunk as a library/CLI** — rejected. Orchard shells out to `git worktree` directly. Adding a worktrunk dependency would couple Orchard to worktrunk's opinions about naming, paths, and hooks without clear benefit. Users who want worktrunk can use both tools side by side.

**Ignore the question entirely** — rejected. The boundary between "dashboard" and "lifecycle manager" should be an explicit decision, not an accident of what hasn't been built yet.
