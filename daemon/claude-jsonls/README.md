# `daemon/claude-jsonls/`

Raw record parsing of Claude Code session JSONLs at `~/.claude/projects/<encoded-cwd>/<sessionUuid>.jsonl`.

## Owns

- **Types:** `Conversation`
- **Queries:** `conversations`, `conversation`
- **Subscriptions:** `nodeChanged`, `peer`, `conversationChanged`
- **Schema partial:** [`schema.graphql`](./schema.graphql) per [S15a](../../RULES.md)

## What this used to own (now elsewhere)

Per devils-advocate review of PR #618, the original `claude-jsonls` was bundling three independent lifecycles:

| Concern | Now in |
|---|---|
| `ClaudeInstance` derivation, `InstanceState` | [`claude-instance`](../claude-instance/) |
| `Contract`, `ContractStatus`, `ContractQuestion` | [`contracts`](../contracts/) |

What remains here is the **lowest layer**: parse JSONLs into `Conversation` nodes. Higher layers (claude-instance, contracts) CONSUME this domain's service.

## Higher-layer consumers

- [`claude-instance`](../claude-instance/) — derives live REPLs from jsonl tail freshness + matching pane processes
- [`contracts`](../contracts/) — parses Contracts-plugin records that ride on the same JSONL stream

## Cross-domain back-edges (resolved here)

- `Conversation.liveInstances: [ClaudeInstance!]!` — declared here, resolved by calling [`claude-instance`](../claude-instance/)'s service.

## Heavy fields excluded

Per [S10](../../RULES.md) and [ADR-016](../../docs/adr/016-daemon-as-protocol.md): `Conversation` excludes transcripts and message bodies. Heavy reads use `GET /v1/conversations/<sessionUuid>/jsonl` — out of band from GraphQL.

## Current source location (pre-refactor)

- `internal/server/providers/claudeprojects/`

## Constitution citations

- [L4](../../RULES.md): jsonl tails cache in-process; field resolvers don't shell out
- [L9](../../RULES.md): Conversation state is a projection of the jsonl file on disk
- [R3](../../RULES.md): conversation reads go through `ConversationByID` + `ConversationsBySessionUuid` loaders
- [S10, ADR-016](../../RULES.md): transcripts are an out-of-band REST endpoint, not a GraphQL field
- [O8](../../RULES.md): per-session memory bounded — 100MB jsonl uses tail-window, not full-file load
- [O3, R16](../../RULES.md): `conversationChanged` emits delta after cache write
