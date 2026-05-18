# `daemon/daemon-self/`

Daemon liveness, version, schema bytes, and the node-id dispatcher entry.

## Owns

- **Types:** `Health`
- **Queries:** `health`, `version`, `schemaSDL`, `node`
- **Schema partial:** [`schema.graphql`](./schema.graphql) per [S15a](../../RULES.md)

## What this used to own (now elsewhere)

Per devils-advocate review of PR #618, the original `daemon-self` was bundling concerns that belong in their own domains:

| Concern | Now in |
|---|---|
| `DaemonState`, `ProviderHealth`, `Meta`, `daemonReload` | [`daemon-meta`](../daemon-meta/) |
| `WorkView`, composite views | [`views`](../views/) |
| `Query.gh` pass-through | [`daemon/gh/`](../gh/) (per [S16b](../../RULES.md)) |

What remains here is the irreducible liveness + introspection surface:
- **`Health`** — the one true "is this daemon serving?"
- **`version`** — what binary is running
- **`schemaSDL`** — bytes of the schema the binary was built against
- **`Query.node(id)`** — generic Node lookup, dispatched through the prefix registry in `daemon/node.go`

## `Query.node` placement

`Query.node` is the prefix-registry dispatcher that every domain registers its `<Type>:` prefix into. The registry itself lives at the shell (`daemon/node.go`), not in any domain. We surface `Query.node` here because it isn't a Node-domain field — it's the entrypoint to the dispatcher, and dispatch is daemon-self's level of abstraction.

## Current source location (pre-refactor)

- `internal/server/server.go` (Health, version, schemaSDL)
- `internal/server/resolvers/node.resolvers.go` (current 535-line file with 14 hard-coded prefix branches → becomes a tiny registry that each domain registers into)

## Constitution citations

- [L10](../../RULES.md): operations about the daemon itself live under `orchard daemon ...`
- [R6](../../RULES.md): one type per file; `schema.resolvers.go`'s 1800-line god-file is the failure mode this domain split addresses
