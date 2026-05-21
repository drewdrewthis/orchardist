# daemon-self — Audit & Implementation Plan

Domain: daemon liveness + version + schemaSDL + node-id dispatcher entrypoint.
Owns: `Health`. Queries: `health`, `version`, `schemaSDL`, `node`.

## Applicable Rules

| Rule | Status | How satisfied |
|---|---|---|
| **R1** | ✅ | All code in `daemon/daemon-self/` by domain, not by technical role |
| **R2** | ✅ | `service.go` exposes the only consumer-facing API (`DaemonSelfReader`) |
| **R3** | ✅ | `Health` has no per-node loaders (computed from start-time; single scalar per request). `Query.node` delegates to `daemon/node.go` registry. |
| **R4** | ✅ | Consumer-defined `NodeRegistry` interface in `daemon/daemon-self/service.go` |
| **R6** | ✅ | `resolver_health.go` owns Health + root Query fields. One file per type. |
| **R8** | ✅ | Errors wrapped with `fmt.Errorf`; sentinel `errors.Is` where applicable |
| **R9** | ✅ | All methods accept `context.Context` first |
| **R11** | ✅ | Constructor returns concrete `*DaemonSelfService`; consumer interface defined by consumer |
| **R14** | ✅ | `DaemonSelfService` — names what it is |
| **R17** | ✅ | No long-running goroutines in this domain (read-only liveness; no poll loop) |
| **S2** | N/A | `Health` is not a Node (it's a liveness probe scalar envelope, not a graph entity) |
| **S5** | ✅ | `Health.status: String!`, `Health.uptimeS: Int!` — required fields are non-null |
| **S15a** | ✅ | Schema partial already at `daemon/daemon-self/schema.graphql` |
| **L4** | ✅ | All reads are in-process (time arithmetic + string constants). No shellout. |
| **L10** | ✅ | Domain surfaces daemon-self introspection; daemon CLI (`orchard daemon ...`) wires it |
| **T1** | ✅ | `resolver_health_test.go` tests every typed field against a stub service |
| **T3** | ✅ | Assertions use concrete expected values, not trivially-true checks |

## File Map

| File | Purpose |
|---|---|
| `schema.graphql` | Already exists — the schema partial (S15a) |
| `service.go` | `DaemonSelfReader` interface + `DaemonSelfService` concrete impl (R2) |
| `resolver_health.go` | Health resolver + root Query resolvers (health, version, schemaSDL, node) (R6) |
| `resolver_health_test.go` | T1 tests for every typed field |
| `AUDIT.md` | This file |

## Cross-Domain Interfaces

### Defined here (consumers import)

None — `daemon-self` is a leaf domain (no domain dependencies per architecture doc).

### Consumed here

- `NodeRegistry` interface (defined in this file, implemented by `daemon/node.go` at shell):
  ```go
  type NodeRegistry interface {
      Resolve(ctx context.Context, id string) (Node, error)
  }
  ```
  The shell's `daemon/node.go` builds the registry from per-domain registrations. The resolver accepts it via constructor injection.

## schemaSDL Embedding Strategy

The `Query.schemaSDL` resolver needs the composed schema as a string baked into the binary.

Decision: use `go:embed` over a single concatenated file written during `make generate`.

The Makefile currently does:
```makefile
cp schema.graphql internal/server/resolvers/schema.graphql
```

Post-refactor it should concatenate:
```makefile
cat daemon/schema.graphql daemon/*/schema.graphql > daemon/daemon-self/schema_combined.graphql
```

And `resolver_health.go` embeds `schema_combined.graphql`.

During this PR (pre-full-migration), the resolver falls back to the existing
`internal/server/resolvers/schema_sdl.go::SchemaSDL()` function to keep tests
green without requiring a full Makefile migration.

## Node Registry

`Query.node` dispatches through a `NodeRegistry` interface. In this PR the
resolver accepts the registry via the `DaemonSelfService` constructor. The
concrete registry (`daemon/node.go`) is a shell concern authored as part of
the integration phase; this domain only owns the `Query.node` entry point.

For test purposes a stub `NodeRegistry` is injected.
