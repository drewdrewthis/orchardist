# Orchardist is a tmux app, not a TUI

**Status:** Reframe, captured 2026-05-19. Not an ADR yet — vocabulary shift in flight.

## The reframe

The "orchardist TUI" is a misnomer. A TUI is a self-contained terminal app with its own render loop, input handling, and screen buffer (ratatui, htop, k9s). What we actually have is **tmux as the substrate**: panes are the windows, tmux owns input/focus/layout, and "the app" is the orchardist composing and navigating that workspace.

The ratatui binary in `crates/orchard/` is a real TUI — but it is **one pane** of the tmux app, not the app itself.

## What orchardist actually is

- **Substrate:** tmux. Panes, windows, sessions. tmux owns focus, splits, status line, keybindings.
- **Brain:** the Go daemon (`internal/server/`) on `127.0.0.1:7777`. Owns state. GraphQL is the protocol (ADR-016, 017, 018).
- **Composition surface:** worktrees → tmux sessions → panes, with Claude instances running inside.
- **Frontends:**
  - ratatui dashboard (`crates/orchard/`) — one pane that shows fleet state.
  - orchard-gui (Svelte/Houdini) — browser frontend over the same GraphQL.
  - tmux itself — the workspace the user actually lives in.
- **Skill surface:** `/launch`, `/spawn-session`, `/stop-session`, `/delegate` etc. are already a command palette without a UI.

## Implications

1. **Don't reinvent what tmux does.** Status line, splits, focus, layout. Those belong to tmux. If we find ourselves rendering pane lists or focus indicators, we're duplicating.
2. **Composition primitives are tmux objects.** `session`, `window`, `pane`. Not custom widgets.
3. **The ratatui dashboard is one pane.** It's a viewer over daemon state; it should not be the "shell" the user lives in.
4. **Input model is tmux keybindings + daemon GraphQL.** Not a custom event loop.
5. **The slash-command surface is the palette.** It just lacks a tmux-bound UI to invoke it from anywhere.

## Prior art

Three external projects worth knowing about — each solves a slice of what orchardist is.

### tmuxy ([flplima/tmuxy](https://github.com/flplima/tmuxy))

Rust backend, talks to tmux via **control mode**, streams pane state to a web/desktop frontend. Explicitly positioned as "watch AI agents work in tmux." This is the closest architectural analog to orchardist: Rust + tmux control mode + frontend over the streamed state. They are solving our problem from a different angle.

Status: "vibe coded, not stable" per their README. Inspiration, not dependency.

Take: read their control-mode bridge before we build our own.

### tmux-palette ([eduwass/tmux-palette](https://github.com/eduwass/tmux-palette))

Raycast-style command palette bound to `C-Space` in tmux. JSON config. Built-in commands cover split pane, jump window, detach session, popup tool, custom palettes.

Take: this is the right shape for surfacing our skill catalog (`/launch`, `/spawn-session`, etc.) from inside tmux without typing into a Claude REPL.

### tmux-jump ([schasse/tmux-jump](https://github.com/schasse/tmux-jump))

Vimium/easymotion-style pane and window navigation. `tmux-prefix + j`, type a character, jump to highlighted target.

Take: the navigation primitive for many-pane workspaces. Paired with daemon state, we could highlight *which* pane has red CI or a waiting Claude.

## What this reframe does NOT decide

- It does not abandon `crates/orchard/`. The ratatui dashboard stays; its role narrows to "one pane that views daemon state."
- It does not commit to adopting tmuxy / palette / jump as dependencies. Each gets its own issue and its own evaluation.
- It does not commit to a "tmux app" as the final name. Working term until we pick a sharper noun.

## Open vocabulary question

"Tmux app" is accurate but vague. Candidate sharper nouns:
- **orchardist workspace** — emphasizes the user's environment.
- **tmux harness** — emphasizes that tmux is held in place by orchardist scaffolding.
- **pane composer** — emphasizes that the job is composing panes.
- **orchestration surface** — emphasizes the skill catalog + daemon over tmux.

Not picking now. Whichever survives a few weeks of use sticks.

## Related research

Full prior-art survey of tmuxy, tmux-palette, and tmux-jump is in `~/.claude/research/455-orchardist-tmux-app-prior-art.md`. Synthesis:

- **tmuxy** — validates the architecture (Rust + tmux control mode + frontend) but is "vibe coded, not stable." Inspiration only. The real follow-up question — should our daemon move from poll-based pane reads to streaming via control mode? — is bigger than tmuxy and not blocked on it.
- **tmux-jump** — useful ergonomics for many-pane workspaces, but pure install-and-bind. Daemon-aware variant ("jump to the pane that needs me") folds into the palette idea.
- **tmux-palette** — strongest candidate for actual work. Our slash-command catalog is *already* a palette; tmux-palette is the missing input surface. Filing as an issue.

## Next moves

One issue:
- **Palette layer** — surface the action catalog (`/launch`, `/spawn-session`, `/stop-session`, `/delegate`, ...) through a tmux-bound popup driven by daemon GraphQL. See research file §2 for the design sketch.

Not filing tmuxy or jump as issues. The reframe doc + the research file are sufficient artifacts for those two threads.
