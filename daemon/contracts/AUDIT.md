# daemon/contracts — Audit

## Rules applicability matrix

| Rule | Applicable | How satisfied |
|---|---|---|
| **L9** | YES | State is a projection of JSONL events via `Fold()`; nothing is persisted. |
| **R1** | YES | All code lives in `daemon/contracts/`. |
| **R2** | YES | `service.go` exposes `ContractsService` interface; resolvers import only that. |
| **R3** | YES | `loaders.go` `ContractByIDLoader` / `ContractsByOwnerLoader` back all resolver reads. |
| **R4** | YES | `ClaudeJSONLSReader` interface defined in this module; consumer defines it. |
| **R5** | YES | Domain owns `Contract`, `ContractQuestion`, `ContractStatus`, `ContractFilter`. |
| **R6** | YES | One file per type: `resolver_contract.go` for `Contract`. |
| **R8** | YES | Typed sentinel + wrapped errors, uniform per module. |
| **R9** | YES | All blocking calls accept `context.Context`. |
| **R10** | YES | Provider goroutines (watcher, consume) owned by `Provider`; `Stop()` closes both. |
| **R11** | YES | Constructors return concrete types; consumers depend on `ContractsService`. |
| **R12** | YES | Subscribe returns `<-chan InvalidationEvent[ContractID]`. |
| **R13** | YES | `sync.RWMutex` for read-heavy cache; `sync.Mutex` for subscriber map. |
| **R14** | YES | Naming: `ContractID`, `Fold`, `Provider`, `Adapter`, `Watcher`. |
| **R16** | YES | `fanOut()` called after `p.mu.Unlock()` (cache write complete). |
| **R17** | YES | Provider poll goroutine has `recover()` + `slog` structured logging. |
| **S15a** | YES | `schema.graphql` is the domain's partial. |
| **T1** | YES | `fold_test.go` covers 9 event kinds; stub stream → assert fold. |
| **T5** | YES | `loaders_test.go` counts underlying service calls; asserts ≤1 per batch. |

## Cross-domain interfaces consumed

| Interface | Defined in | Imported via |
|---|---|---|
| `ClaudeJSONLSReader` | `daemon/contracts/service.go` (consumer owns it per R4) | `daemon/contracts` package — no cross-domain import needed; the concrete claude-jsonls service is injected at wiring time |

## Cross-domain interfaces defined (for others to consume)

None — contracts is consumed by `views` which reads it through `ContractsService`.

## Pending rename

The schema partial uses `PENDING_USER_APPROVAL` (renamed from `PENDING_DREW_APPROVAL`). The existing `internal/server/graphql/models_gen.go` still has `ContractStatusPendingDrewApproval`. After the final `make generate` run against the composed 13-domain schema, `models_gen.go` will emit `ContractStatusPendingUserApproval`. The `mapStatus()` function in `provider.go` uses a local `ContractStatus` type — no dependency on the generated constant — so no change is needed at that point.

## BLOCKED

None.
