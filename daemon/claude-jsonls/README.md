# `daemon/claude-jsonls/`

Claude Code session JSONLs at `~/.claude/projects/<encoded-cwd>/<sessionUuid>.jsonl`.

This domain owns base record parsing AND companion records from the Conversation/Contracts plugins, which share the same JSONL stream and surface as enriched fields on the same records.

## Owns

- **Types:** `Conversation`, `ClaudeInstance`, `Contract`, `ContractQuestion`
- **Enums + inputs:** `InstanceState`, `ContractStatus`, `ContractFilter`
- **Queries:** `conversations`, `conversation`, `contract`, `contracts`, `claudeInstances`
- **Subscriptions:** `nodeChanged`, `peer`, `conversationChanged`
- **Schema partial:** [`schema.graphql`](./schema.graphql) per [S15](../../RULES.md)

## ClaudeInstance is derived, not primary

A `ClaudeInstance` is the **join** of a live JSONL tail with a matching live pane process. Most JSONLs are historical (no live REPL). The instance set is:

> "JSONLs whose heartbeat is fresh AND there is a tmux pane whose process is `claude` with the matching sessionUuid in its working state."

Per the user's call: "a jsonl might not have a running instance. most don't. there is a chance that there are two instances for the same jsonl (although problematic, via /resume) but no, probably not. it's probably tmuxPane.process.isClaude or something like that."

`Conversation.liveInstances: [ClaudeInstance!]!` returns 0, 1, or 2+ matching live panes (the rare /resume edge case) — per the user, "graphs can return arrays."

## Cross-domain back-edges (resolved here)

| Field | Owning domain |
|---|---|
| `ClaudeInstance.pane` | [`tmux`](../tmux/) |
| `ClaudeInstance.process` | [`ps`](../ps/) |
| `ClaudeInstance.account` | [`claude-account`](../claude-account/) |
| `ClaudeInstance.worktree` | [`git`](../git/) |
| `ClaudeInstance.conversation` | self (same file) |

## Heavy fields excluded

Per [S10](../../RULES.md) and [ADR-016](../../docs/adr/016-daemon-as-protocol.md): `Conversation` excludes transcripts and message bodies. Heavy reads use `GET /v1/conversations/<sessionUuid>/jsonl` — out of band from GraphQL.

## Current source location (pre-refactor)

- `internal/server/providers/claudeprojects/` (Conversation)
- `internal/server/providers/claudeinstance/` (instance derivation — folds in as the consumer of jsonl tail data)
- `internal/server/providers/contracts/` (Contracts engine — its records ride on the same jsonl stream)

## Constitution citations

- [L4](../../RULES.md): jsonl tails cache in-process; field resolvers don't shell out
- [R3](../../RULES.md): instance derivation goes through `ConversationsBySessionUuid` loader, not `Snapshot()`
- [S10, ADR-016](../../RULES.md): transcripts are an out-of-band REST endpoint, not a GraphQL field
- [O8](../../RULES.md): per-session memory bounded — 100MB jsonl uses tail-window, not memory growth
- [O3](../../RULES.md): `conversationChanged` subscription emits small deltas, not 50KB re-queries
