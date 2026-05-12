# ADR-018: Daemon Owns Mutations

## Status
Accepted.

## Decision
All worktree, tmux, Claude session, and config mutations flow through daemon GraphQL mutations. Clients (orchard-tui, orchard-gui, orchard-worktree CLI, mobile) call mutations; they do not exec local processes for state changes.

## Why
- Mobile and remote clients cannot exec local processes. Mutation-via-exec excludes them by construction.
- Multiple clients hitting the same worktree race without a coordinating authority.
- Follows ADR-017 layer responsibilities: daemon owns state changes.

## Scope

| Action | Owner |
|--------|-------|
| Create/destroy worktree | daemon mutation |
| Kill tmux session | daemon mutation |
| Switch tmux session | daemon mutation |
| Transfer worktree | daemon mutation |
| Write `~/.orchard/config.json` | daemon mutation |
| Read-path joins | daemon (ADR-008) |

## Consequences
- gqlgen schema grows mutation surface; resolvers thin-wrap existing CLI primitives.
- `orchard-worktree` CLI becomes a daemon client, not a direct git wrapper.
- Mobile and boxd clients unblock for write operations.
- `worktree-core` keeps the primitive functions; daemon resolvers call them; CLI becomes a thin daemon client.

See ADR-016 (GraphQL protocol), ADR-017 (layer responsibilities).
