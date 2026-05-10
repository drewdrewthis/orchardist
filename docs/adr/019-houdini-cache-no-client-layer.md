# ADR-019: Houdini Cache, No Client-Side Cache Layer

## Status
Accepted.

## Decision
The orchard-gui frontend uses Houdini's normalized cache and `defaultCachePolicy`. No hand-rolled caching layers, no module-level Maps keyed by id, no custom invalidation logic.

## Why
- Houdini already does cache normalization, fragment reuse, and subscription-driven invalidation.
- Hand-rolled cache layers compete with Houdini's; produces stale reads and update storms.
- Subscription flows (`Subscription.conversationChanged`, etc.) update Houdini's store automatically; bypassed by custom layers.

## How
- Default cache policy `CacheOrNetwork` unless explicitly overridden per query.
- Lens projections read from Houdini stores; no parallel state.
- Server-driven invalidation: mutations and subscriptions drive cache updates, not client polling.

## Consequences
- Schema requires stable IDs on every type for normalization.
- Cache bugs become Houdini bugs (well-tested), not ours.
- Frontend complexity drops.

See ADR-016 (GraphQL protocol), ADR-017 (layer responsibilities).
