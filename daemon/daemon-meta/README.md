# `daemon/daemon-meta/`

Provider freshness counters + per-field provenance envelope.

## Owns

- **Types:** `DaemonState`, `ProviderHealth`, `Meta`
- **Queries:** `daemonState`
- **Mutations:** `daemonReload` (the canonical exception to [L5](../../RULES.md) — affects daemon-internal state, not external truth)
- **Schema partial:** [`schema.graphql`](./schema.graphql) per [S15a](../../RULES.md)

## Why this exists

Per devils-advocate review of PR #618: `daemon-self` was a god-module (Health + DaemonState + ProviderHealth + Meta + node-dispatcher + schemaSDL + daemonReload). That violates [R6](../../RULES.md). Split into two:

- `daemon-self/` — liveness only (Health, version, schemaSDL, node-dispatcher)
- `daemon-meta/` — **this domain** — provider rollup, freshness counters, the Meta provenance envelope

## Cross-domain back-edges

- `DaemonState.providers[].name` is a stable label that matches the providers' own self-naming. No graph edge — pure freshness telemetry.

## Current source location (pre-refactor)

- `internal/server/resolvers/schema.resolvers.go` — DaemonState + ProviderHealth scattered in the 1800-line god-file

## Constitution citations

- [L5, L8](../../RULES.md): `daemonReload` is the rare carve-out for "affects daemon-internal state, not external truth"
- [O4](../../RULES.md): cache hit attribution surfaces HERE via `DaemonState.providers[]` + `Meta`
- [O5](../../RULES.md): cold-start cost is measured via `DaemonState.startedAt` + per-provider `lastSuccessfulRefresh`
- [T2](../../RULES.md): mutation tests live here for `daemonReload` (note: no script — it's the L5 exception, tested in-process)
