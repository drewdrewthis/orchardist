# ADR-017: Layer Responsibilities

## Status
Accepted.

## Decision
Three layers, single responsibility each:

| Layer | Owns | Does NOT do |
|-------|------|-------------|
| **Daemon** (Go) | State, joins, subscriptions, mutations, federation | Render, no opinions on display |
| **Client** (orchard-tui, orchard-gui, mobile) | Render, input → mutation, view state | Joins, business logic, caching |
| **Tools** (git, gh, tmux, ssh) | Primitives invoked by daemon only | Invoked directly by clients |

## Why
- Joins re-implemented in N clients = N drift sources. Done once in the daemon = one truth.
- Caching done by client = competes with daemon's view of fresh data. Daemon owns invalidation.
- Tools invoked from clients = mobile and remote clients excluded by construction.

## Anti-patterns
- Lens projections that re-join data the daemon could serve in one query.
- Client-side `Map` keyed by id rebuilding daemon-known relationships.
- Frontend code that reads two daemon fields and combines them when a single field would do — file a daemon issue instead.
- Clients that exec `gh`, `git`, `tmux` directly. Always go through the daemon.

## Exceptions
- Title fallback chain (`agentName → customTitle → branch → cwd → uuid`): GraphQL doesn't fluent-coalesce; this stays client-side.
- Pure presentation derived state (sort order, filter results, hover state).

## Consequences
- New behavior starts with: "what daemon field/mutation/subscription do I need?"
- Frontend complexity stays at render + input mapping.
- Daemon complexity grows; mitigated by gqlgen codegen and schema-first design.

See ADR-016 (GraphQL as protocol), ADR-008 (federated discovery via daemon).
