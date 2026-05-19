# host-identity domain audit

## Applicable rules

| Rule | How satisfied |
|---|---|
| **R1** | Package at `daemon/host-identity/` — one domain, one directory. |
| **R2** | `service.go` exposes `Service` interface + `NewService`. Resolvers import only `Service`. |
| **R3** | `resolver_host.go` calls `Loaders.HostByID.Load(key)`. No `Snapshot()` anywhere. |
| **R4** | `HostProvider` interface defined in `service.go`; resolver depends on `Service`, not `*Provider`. |
| **R5** | No cross-domain imports — leaf domain with no service dependencies. |
| **R6** | `resolver_host.go` owns `Host` resolver methods; `resolver_query.go` owns query root methods. One type per file. |
| **R7** | New types/fields live in this directory; nothing external changes. |
| **R8** | Errors wrapped with `%w` throughout; `fmt.Errorf` only — one style per module. |
| **R9** | Every blocking call accepts `ctx context.Context` and propagates it to I/O. |
| **R10** | `provider.go` owns `pollLoop` goroutine; `Start` launches it; cancel propagates via ctx. |
| **R11** | `NewService` returns `*serviceImpl` (concrete); `Service` interface is what resolvers consume. |
| **R12** | `Subscribe` returns `<-chan adapter.InvalidationEvent[HostID]` (receive-only). |
| **R13** | `provider.go` uses `sync.RWMutex` (read-heavy: many resolver calls vs one poll writer). |
| **R14** | `HostID`, `Identity`, `Load`, `Service`, `Provider` — all honest names. |
| **R15** | New files — no pre-existing files dirtied. |
| **R16** | `fanOutInvalidate` called inside `refreshLoad` after cache write, before unlock. |
| **R17** | `pollLoop` wrapped in `defer func() { recover(); log }` per rule. |
| **S2** | `Host` implements `Node` (id `Host:<machineId>`). `ResourceLoad` is not a node — inline on Host. |
| **S5** | `resourceLoad: ResourceLoad` nullable (null at cold boot). All other non-optional fields non-null. |
| **S11** | Schema partial is the source of truth; Go types are projections of it. |
| **S13** | `host`, `hosts`, `peers` queries (nouns). PascalCase types, camelCase fields. |
| **S15a** | Schema partial at `daemon/host-identity/schema.graphql` (copied from constitution). |
| **S15b** | No cross-domain `extend type` here — other domains extend Host in their own partials. |
| **S15c** | No scalar re-declarations; root partial owns `scalar Time`, `scalar JSON`, `interface Node`. |
| **S16a** | `Host` and `ResourceLoad` are typed-core nodes — loader-batched, cached, R3-clean. |
| **L4** | Identity is one-shot at boot (in-process); resource load polls via OS syscalls/shellouts (no script exec in resolver hot-path). |
| **L9** | No persisted state — restart re-reads machineId from OS. |
| **O6** | 5s TTL — bounded poll cadence. Adaptive: poll only fires when TTL expires. |
| **T1** | `service_test.go` tests every typed resolver field against a stub service. |
| **T5** | `loaders_test.go` verifies `HostByID` loader coalesces N parallel calls to ≤1 provider fetch. |

## File map

| File | Purpose |
|---|---|
| `service.go` | `Service` interface + `serviceImpl` wrapping `*Provider`. The R2 API consumers see. |
| `provider.go` | In-process cache: identity one-shot + 5s load poll. Owns goroutine lifecycle. |
| `adapter.go` | `IdentityReader` + `LoadReader` interfaces + shared helpers (types). |
| `adapter_darwin.go` | macOS implementations (build tag: darwin). |
| `adapter_linux.go` | Linux implementations (build tag: linux). |
| `resolver_query.go` | `Query.host`, `Query.hosts`, `Query.peers` — one file per rule R6 concern (query root). |
| `resolver_host.go` | `Host.peers`, `Host.version` — Host type resolver methods. |
| `loaders.go` | `HostByID` DataLoader; `Loaders` struct for request-scoped bundle. |
| `service_test.go` | T1: every typed resolver field against stubbed service. |
| `loaders_test.go` | T5: loader coalescing assertion. |

## Cross-domain interfaces I define

None — leaf domain.

## Cross-domain services I consume

None — leaf domain. `ps` and `host-services` will `extend type Host` in their own partials.

## BLOCKED

Nothing blocked.
