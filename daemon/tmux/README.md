# `daemon/tmux/`

tmux server: sessions, windows, panes, clients.

## Owns

- **Types:** `TmuxServer`, `TmuxSession`, `TmuxWindow`, `TmuxPane`, `TmuxClient`
- **Enums + inputs:** `TmuxSessionSort`, `TmuxSessionFilter`, `TmuxPaneFilter`
- **Queries:** `tmuxServer`, `tmuxSessions`, `tmuxPanes`
- **Subscriptions:** `tmuxSessionsChanged`
- **Mutations:** `sendTextToPane` (per [L5](../../RULES.md), execs `scripts/tmux-send-text.sh`)
- **Schema partial:** [`schema.graphql`](./schema.graphql) per [S15](../../RULES.md)

## Cross-domain dependencies

tmux is the **only** domain in the [north-star dependency graph](../../docs/architecture.md#dependency-graph-north-star-not-current-state) that has cross-domain deps (per ADR-023 design):

| Cross-domain field | Owning domain | Why |
|---|---|---|
| `TmuxPane.process` | [`ps`](../ps/) | Pane→Process is the canonical "what's running here" join |
| `TmuxPane.claudeInstance` | [`claude-jsonls`](../claude-jsonls/) | Derived from matching the pane's process to a Claude jsonl tail |

Per [R5](../../RULES.md): cross-module reach happens via the consumer's service interface, not by importing the other module's provider.

## Performance landmines

- **R3 violation in current code:** `pane.window.session` traversal triggered `Snapshot()` per call, causing ~60s cold lens loads (#612). The refactor must route through a `PaneByID`/`SessionByID` DataLoader.
- **R14 violation in current code:** `TmuxPane.claudeInstance` is named honestly here, but the schema also has `chatMute` (consumed for Claude REPL pings — fix by renaming, not by comments).

## Current source location (pre-refactor)

- `internal/server/providers/tmux/`

## Constitution citations

- [L4, L5](../../RULES.md): queries cached in-process; `sendTextToPane` execs a script
- [R3, O1](../../RULES.md): every field resolver goes through a loader; loaders consumed everywhere they exist
- [R14](../../RULES.md): naming honesty — rename `chatMute` rather than comment it
- [S7](../../RULES.md): subscription emits `{nodeId, patch}`, not full re-fetches
- [R16](../../RULES.md): subscription fires AFTER cache write
- [O9](../../RULES.md): hot-path allocation audited — `Snapshot()` map-clone is the canonical anti-pattern
