# claude-account — Refactor Audit

## Applicable rules and satisfaction map

| Rule | Status | How satisfied |
|---|---|---|
| **L1** | ✅ | No script mutations in scope. Pass-through executes shell per S16b guard. |
| **L2** | ⏭️ | No mutations in this domain (account login/logout out of scope). |
| **L4** | ✅ | `adapter.go` shells out; `provider.go` owns cache. Resolver serves from loader, not shellout. |
| **L5** | ⏭️ | No mutations. |
| **L9** | ✅ | Provider cache is reconstructable from `claude auth status` + `ccusage` on restart. |
| **R1** | ✅ | All code in `daemon/claude-account/`. |
| **R2** | ✅ | `service.go` is the only exported API. Consumers import this package, not provider internals. |
| **R3** | ✅ | `loaders.go` defines `AccountByID` and `AccountsByHost` loaders. `resolver_claude_account.go` routes through loaders. |
| **R4** | ✅ | `HostReader` and `InstancesReader` interfaces defined in this module; crossed via `service.go` consumer interfaces. |
| **R5** | ✅ | Anti-corruption: `HostReader` and `InstancesReader` are narrow interfaces. No foreign provider types leak in. |
| **R6** | ✅ | `resolver_claude_account.go` owns ClaudeAccount type only. Pass-through in `resolver_pass_through.go`. |
| **R8** | ✅ | Typed sentinel pattern (`ErrToolNotInstalled` / `ToolNotInstalledError`) throughout. |
| **R9** | ✅ | All blocking calls accept `context.Context` first. |
| **R10** | ✅ | Poll loop owned by provider; shutdown via `Stop()`. |
| **R11** | ✅ | Constructors return concrete types; consumers depend on `Service` interface. |
| **R12** | ✅ | `Subscribe` returns `<-chan`. |
| **R13** | ✅ | `RWMutex` for read-heavy cache. `Mutex` for subscriber map. |
| **R16** | ✅ | `broadcast` called after cache write in `refresh()`. |
| **R17** | ✅ | `pollLoop` goroutine wraps top-level with `defer recover()`. |
| **S2** | ✅ | `ClaudeAccount implements Node`. Stable id `ClaudeAccount:<host>:<email>`. |
| **S5** | ✅ | `quotaUsed`, `quotaCap`, `quotaResetsAt` nullable (`*float64`, `*float64`, `*time.Time`). |
| **S13** | ✅ | Query nouns (`claudeAccounts`), enum PascalCase. |
| **S15a** | ✅ | Schema partial in `daemon/claude-account/schema.graphql`. |
| **S15c** | ✅ | No scalar or root type re-declaration. Uses `extend type Query`, `implements Node`, `Time`. |
| **S16a** | ✅ | Typed core: `claudeAccounts` query cached + loader-batched. |
| **S16b** | ✅ | Pass-through: `claudeCli(tool, args)` top-level only. 30s timeout, concurrency cap 4 enforced in `resolver_pass_through.go`. Not cached. Not subscribable. |
| **O4** | ✅ | `LastError()` surfaces freshness for observability. |
| **O6** | ✅ | 60s poll — quota changes slowly; no over-polling. |
| **O11** | ✅ | Read-through cache policy. Documented in `provider.go`. |
| **T1** | ✅ | `resolver_claude_account_test.go` — every field asserted against stubbed `Service`. |
| **T5** | ✅ | `loaders_test.go` — counts underlying service calls; asserts ≤1 per request cycle. |
| **T7** | ✅ | `resolver_pass_through_test.go` — asserts timeout + concurrency cap. |

## File map

| File | Purpose |
|---|---|
| `service.go` | `Service` interface + `NewService(provider)`. The only API consumers import. |
| `provider.go` | In-memory cache + poll loop. Migrated from `internal/server/providers/claudeaccount/provider.go`. |
| `adapter.go` | Shell I/O (`claude auth status`, `ccusage blocks`). Migrated from `internal/server/providers/claudeaccount/adapter.go`. |
| `types.go` | `AccountID`, `Account`, `ErrToolNotInstalled`, `ToolNotInstalledError`. |
| `watcher.go` | External-cadence watcher helper. |
| `loaders.go` | `AccountByID` + `AccountsByHost` DataLoaders (ADR-022 axes). |
| `resolver_claude_account.go` | Resolvers for `ClaudeAccount` type fields + `Query.claudeAccounts`. |
| `resolver_pass_through.go` | `Query.claudeCli` pass-through with S16b guards. |
| `*_test.go` | Unit tests (T1, T5, T7). |
| `schema.graphql` | Domain schema partial (unchanged from constitution). |

## Cross-domain interfaces (defined here, R4)

```go
// HostReader — imported by this domain to resolve ClaudeAccount.host.
// Defined in this module; implemented by daemon/host-identity.
type HostReader interface {
    GetHost(ctx context.Context, hostID string) (*graphql.Host, error)
}

// InstancesReader — imported by this domain to resolve ClaudeAccount.instances.
// Defined in this module; implemented by daemon/claude-instance.
type InstancesReader interface {
    InstancesByAccount(ctx context.Context, email string) ([]*graphql.ClaudeInstance, error)
}
```

v1: both edges resolve via stub (Host ID prefix only; Instances = []). Full
cross-domain wiring lands when host-identity and claude-instance agents
complete their own PRs.

## Cross-domain services consumed

None at source level. Cross-domain edges are guarded behind interface stubs
that v1 fills with direct-construction values (R4 ISP: consumer defines the
interface, fills it with the stub in v1).
