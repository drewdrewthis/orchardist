# `daemon/daemon-self/`

The daemon's own state and introspection.

## Owns

- **Types:** `Health`, `DaemonState`, `ProviderHealth`, `Meta`, `WorkView`
- **Queries:** `health`, `daemonState`, `version`, `schemaSDL`, `workView`, `node`, `gh` (passthrough)
- **Mutations** (to be added in #613, the rare exception per [L5](../../RULES.md)):
  `daemonReload`, manual cache rebuild — these affect daemon-internal state, not external truth
- **Schema partial:** [`schema.graphql`](./schema.graphql) per [S15](../../RULES.md)

## Why this exists as a domain

The other 8 domains all reflect external truth (git, gh, tmux, ps, etc.). This domain is **the daemon talking about itself**: its health, its loaded config, its registered providers' freshness counters, the federation-aware node dispatcher entrypoint, the schema bytes baked into the binary.

Per the design call:

> "loaded config (the watched-projects list and peer list from `~/.orchard/config.json`), cache statistics, peer-connection status — that all has an owner. Call it `daemon-self`."

## `Query.gh` belongs here, not in `gh`

The `gh` domain owns typed GitHub fields (`PullRequest`, `Issue`, etc.) populated by the daemon's gh provider. `Query.gh(query, variables): JSON` is a different beast: it forwards an **arbitrary** GitHub GraphQL document with the daemon's credentials and returns opaque JSON. This is federation-aware infrastructure for callers who want to bypass orchard's typed surface — that's plumbing, not a typed `gh` field. So it lives here.

Same reason `Query.node(id)` lives here: it's the prefix-registry dispatcher that any domain registers into, not a `Node`-domain field.

## Cross-domain consumption

`WorkView` is a **composite view** that pre-walks every other domain's graph in a single round trip — per [O2](../../RULES.md), this is "eager join saving round trips," but the underlying field resolvers are the same per-type resolvers, so it doesn't violate lazy-by-default.

`Meta` is a **provenance envelope** returned alongside list/composite fields, disambiguating "valid empty" from "data unavailable."

## Current source location (pre-refactor)

- `internal/server/resolvers/schema.resolvers.go` (DaemonState/Health/WorkView scattered in the 1800-line god-file)
- `internal/server/resolvers/node.resolvers.go` (the `node(id)` dispatcher — 535 lines with 14 hard-coded prefix branches → becomes a tiny prefix registry that each domain registers into)
- `internal/server/server.go` (HTTP/WebSocket wiring — moves to `daemon/server.go`)

## Constitution citations

- [L8](../../RULES.md): mutations here are the rare exception ("affect daemon-internal state, not external truth"). Most domains' mutations exec scripts per L5.
- [L10](../../RULES.md): operations about the daemon itself live under `orchard daemon ...` in the CLI
- [R6, R7](../../RULES.md): the current `schema.resolvers.go` god-file is the canonical SRP violation. The refactor decomposes it.
- [O4](../../RULES.md): cache hit attribution is surfaced HERE via `DaemonState.providers[]`
- [O5](../../RULES.md): cold start cost is measured via `DaemonState.startedAt` + per-provider `lastSuccessfulRefresh`
