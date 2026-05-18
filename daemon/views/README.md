# `daemon/views/`

Composite views that traverse multiple domains for round-trip economy.

## Owns

- **Types:** `WorkView`
- **Queries:** `workView`
- **Schema partial:** [`schema.graphql`](./schema.graphql) per [S15a](../../RULES.md)

## Why this exists

Per devils-advocate review of PR #618: composite views like `WorkView` touch every domain. If they lived inside `daemon-self/`, every domain refactor would also need to edit daemon-self. That violates [R7](../../RULES.md) (open-closed). Pulling composite views out keeps the other domains' refactors independent.

## The S14 contract

[S14](../../RULES.md): composite views DELEGATE to the per-type resolvers; they do NOT re-implement any join. `WorkView.repos` reuses the `Query.repos` resolver. `WorkView.tmuxSessions` reuses `Query.tmuxSessions`. There is no `WorkView`-specific join logic.

What `views` adds is the **shape**: clients pull repo + worktree + PR + tmux + claude in one round-trip query, but the actual resolvers are imported from each domain's service.

## Cross-domain dependencies

`views` is the most cross-cutting domain in the entire daemon. It imports the service interface of every domain that contributes a field to `WorkView`:

| Field | Owning domain |
|---|---|
| `WorkView.repos` | [`git`](../git/) (delegated) |
| `WorkView.tmuxSessions` | [`tmux`](../tmux/) (delegated) |
| `WorkView.claudeInstances` | [`claude-instance`](../claude-instance/) (delegated) |
| `WorkView.meta` | [`daemon-meta`](../daemon-meta/) (delegated) |

Per [R5](../../RULES.md): all imports go through service interfaces, never providers.

## Current source location (pre-refactor)

- `internal/server/resolvers/schema.resolvers.go` — `WorkView` field resolvers scattered in the god-file

## Constitution citations

- [S14](../../RULES.md): one resolver per logical field; composite views delegate
- [O2](../../RULES.md): lazy field resolution — `WorkView` doesn't force evaluation of fields the client doesn't select
- [T4](../../RULES.md): integration-test cross-domain joins at the GraphQL boundary
