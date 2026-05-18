# daemon/host-services — Domain Audit

## Domain

launchd (macOS) / systemd (Linux) unit watchlist.

Owns: `HostService`, `HostServiceState`, `HostServiceFilter`.
Queries: `hostServices`, `hostServiceCtl` (pass-through).
Extends: `Host.hostServices` (cross-domain back-edge per S15b).

## Rule applicability

| Rule | How satisfied | File |
|---|---|---|
| **L1** | Lifecycle mutations (start/stop/restart) exec `scripts/host-service-{start,stop,restart}.sh` | `mutations.go`, `scripts/` |
| **L2** | Scripts emit `{"ok": bool, "data"?: …, "error"?: {"code", "message"}}` | `scripts/host-service-*.sh` |
| **L3** | Scripts are bash — clearest for shellout wrapping | `scripts/` |
| **L4** | `launchctl`/`systemctl` shellouts live in the adapter's poll loop, not in field resolvers | `adapter*.go`, `provider.go` |
| **L5** | Mutations exec scripts; daemon validates input and projects `--json` output | `mutations.go` |
| **L8** | `serviceStart/Stop/Restart` return the affected `HostService` node | `mutations.go`, schema |
| **L9** | No persisted daemon state — cache rebuilt from shellouts on every poll | `provider.go` |
| **O6** | Poll loop uses `PollInterval = 5s`; idle daemon doesn't hammer launchctl | `provider.go` |
| **R1** | All code lives in `daemon/host-services/` | All files |
| **R2** | `service.go` is the only public API; resolvers import only this package | `service.go` |
| **R3** | `HostServiceByID` and `HostServicesByHostID` loaders; field resolvers call `Load()` not `Snapshot()` | `loaders.go`, `resolver_hostservice.go` |
| **R4** | `HostIdentityReader` interface defined here for the host-identity dependency | `service.go` |
| **R5** | Cross-domain reach via `HostIdentityReader` interface, never via host-identity's provider type | `service.go`, `resolver_host.go` |
| **R6** | `resolver_hostservice.go` (HostService type), `resolver_host.go` (Host extension) — one file per type | separate files |
| **R8** | Typed sentinel errors (`ErrServiceManagerMissing`); consistent `fmt.Errorf("%w")` wrapping | `service.go`, adapter files |
| **R9** | All blocking calls accept `context.Context` first | all |
| **R10** | Poll goroutine owned by `Provider`; stops on ctx cancel | `provider.go` |
| **R11** | `New()` / `NewWith()` return `*Service` (concrete); consumers use `ServiceReader` interface | `service.go` |
| **R12** | `Subscribe()` returns `<-chan InvalidationEvent` (read-only direction) | `provider.go`, `service.go` |
| **R13** | `sync.RWMutex` for read-heavy cache map | `provider.go` |
| **R14** | `StateNotInstalled` ≠ `StateUnknown` — honest naming per S5 | `service.go` |
| **R16** | Subscription fan-out fires AFTER cache write in `refreshOne` | `provider.go` |
| **R17** | `pollLoop` goroutine has panic-recover + structured logging | `provider.go` |
| **S5** | `not_installed` and `unknown` are first-class enum values, never nulls | schema, adapter |
| **S8** | `serviceStart/Stop/Restart` return affected `HostService` | mutations schema |
| **S13** | Mutations named `serviceStart`, `serviceStop`, `serviceRestart` | schema |
| **S15a** | Schema partial in `daemon/host-services/schema.graphql` | schema (pre-existing) |
| **S15b** | `extend type Host` and `Host.hostServices` resolver live here | `resolver_host.go` |
| **S15c** | No re-declaration of scalars or root operation types | schema (pre-existing) |
| **S16a** | `hostServices(filter)` typed core with cached, loader-batched reads | `resolver_hostservice.go`, `loaders.go` |
| **S16b** | `hostServiceCtl(host, args): JSON` pass-through with 30s timeout + concurrency cap 4 | `resolver_passthrough.go` |
| **T1** | Every typed field tested against stubbed service | `resolver_hostservice_test.go`, `resolver_host_test.go` |
| **T2** | Each mutation script tested for `--json` envelope on success and failure | `scripts/*_test.sh` |
| **T5** | Loader coalescing verified by counting underlying service fetches | `loaders_test.go` |
| **T7** | Pass-through timeout and concurrency cap asserted | `resolver_passthrough_test.go` |
| **M1** | All mutations declared in `daemon/host-services/schema.graphql` | schema (pre-existing) |
| **M4** | Input validation in `mutations.go` before script exec | `mutations.go` |
| **M5** | `serviceStart` is idempotent (starting an already-running service is a no-op per launchctl/systemctl) — documented in schema | mutations schema |

## Cross-domain interfaces

### Defined here (R4 — consumer defines the interface in its own module)

```go
// HostIdentityReader is the narrow host-identity surface this domain needs.
// host-identity domain must satisfy this interface.
type HostIdentityReader interface {
    MachineID(ctx context.Context) (string, error)
}
```

### Consumed from other domains

| Domain | Interface | What we need |
|---|---|---|
| `host-identity` | `HostIdentityReader` | `Host.machineId` to build `HostService.id` prefix |

## File map

| File | Purpose |
|---|---|
| `service.go` | `Service` struct + `ServiceReader` interface. The only public API. |
| `provider.go` | In-process cache + 5s poll loop. Internal. |
| `adapter.go` | Build-tag entry point — `NewAdapter()`. |
| `adapter_darwin.go` | `launchctl list <Label>` shellout. |
| `adapter_linux.go` | `systemctl --user` + `journalctl --user` shellout. |
| `loaders.go` | `HostServiceByID`, `HostServicesByHostID` DataLoaders. |
| `resolver_hostservice.go` | `HostService` field resolvers. |
| `resolver_host.go` | `extend type Host { hostServices }` resolver. |
| `resolver_passthrough.go` | `hostServiceCtl` pass-through with L4 guards. |
| `mutations.go` | `serviceStart`, `serviceStop`, `serviceRestart` — exec scripts. |
| `subscriptions.go` | `hostServiceChanged` subscription. |
| `*_test.go` | Unit tests (T1, T5, T7). |
| `scripts/host-service-start.sh` | L5 script — `launchctl start` / `systemctl --user start`. |
| `scripts/host-service-stop.sh` | L5 script — stop. |
| `scripts/host-service-restart.sh` | L5 script — restart. |

## BLOCKED

None. The integration test (`integration/host_services_test.go`) requires the
`host-identity` domain's service to be importable. It is filed as a follow-up
cross-domain test per T4 — pending `host-identity` domain PR landing and the
`daemon/` package tree being importable. The unit tests here (T1, T5, T7) are
self-contained and will pass today.
