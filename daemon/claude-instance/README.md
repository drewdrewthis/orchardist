# `daemon/claude-instance/`

Live Claude REPL — derived from jsonl tail + matching tmux pane process.

## Owns

- **Types:** `ClaudeInstance`, `InstanceState`
- **Queries:** `claudeInstances`
- **Schema partial:** [`schema.graphql`](./schema.graphql) per [S15a](../../RULES.md)

## Why this exists

Per devils-advocate review of PR #618: `claude-jsonls` was bundling three independent lifecycles (Conversation parsing + ClaudeInstance derivation + Contracts). They have different write paths, cadences, and consumers. Splitting prevents one domain refactor from blocking the others.

ClaudeInstance is a **join**, not a primary resource. The instance set is:

> "JSONLs whose heartbeat is fresh AND there is a tmux pane whose process is `claude` with the matching sessionUuid."

Most JSONLs have no live instance (historical). Some have 2+ (rare /resume).

## Cross-domain inputs (this domain CONSUMES)

| Input | Owning domain |
|---|---|
| jsonl tail + sessionUuid | [`claude-jsonls`](../claude-jsonls/) |
| pane whose `pane_current_command` is `claude` | [`tmux`](../tmux/) |
| pane.process pid + cwd | [`ps`](../ps/) |

Per [R5](../../RULES.md): consumed via each domain's service interface.

## Cross-domain back-edges (resolved here)

| Field | Owning domain |
|---|---|
| `ClaudeInstance.pane` | [`tmux`](../tmux/) |
| `ClaudeInstance.process` | [`ps`](../ps/) |
| `ClaudeInstance.account` | [`claude-account`](../claude-account/) |
| `ClaudeInstance.worktree` | [`git`](../git/) |
| `ClaudeInstance.conversation` | [`claude-jsonls`](../claude-jsonls/) |

## Current source location (pre-refactor)

- `internal/server/providers/claudeinstance/` (instance derivation — this domain's natural home)

## Constitution citations

- [R5](../../RULES.md): consumes 3 domains' services; declares none of their types
- [R3, O1](../../RULES.md): instance derivation goes through `ConversationsBySessionUuid` + `PanesByCommand` loaders, not `Snapshot()`
- [S14](../../RULES.md): `ClaudeInstance.worktree` resolver delegates to git's worktree resolver
- [T4](../../RULES.md): cross-domain join tested at the GraphQL boundary, not at the provider boundary
