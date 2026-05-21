# daemon/ps — Domain Audit

## Applicable Rules

| Rule | Status | How satisfied |
|---|---|---|
| **L4** | ✅ | Query path is in-process via `service.go`; no script exec on read path |
| **L9** | ✅ | No persisted state; adapter re-observes on every poll |
| **R1** | ✅ | Code grouped under `daemon/ps/` — one domain, one directory |
| **R2** | ✅ | `service.go` exports `Service` interface; only exported API consumers may call |
| **R3** | ✅ | `loaders.go` — `cwdLoader` and `argsLoader` are DataLoader-shaped and consumed by resolvers (not bypassed) |
| **R4** | ✅ | Cross-domain back-edges (`GitService`, `ClaudeInstanceService`) defined as narrow consumer-owned interfaces in this package |
| **R5** | ✅ | `Process.worktree` and `Process.claudeInstance` resolvers import service interfaces, not provider types |
| **R6** | ✅ | One file per GraphQL type: `resolver_process.go`, `resolver_host_ext.go`, `resolver_query.go`, `resolver_subscription.go` |
| **R8** | ✅ | All errors wrapped with `fmt.Errorf(...%w)` sentinel style |
| **R9** | ✅ | Every blocking call accepts and respects `context.Context` |
| **R10** | ✅ | Provider goroutine owned by `provider.go`; shutdown via context cancellation |
| **R12** | ✅ | `Subscribe` returns `<-chan` (receive-only) |
| **R13** | ✅ | `sync.RWMutex` for read-heavy sub tracking; `sync.Mutex` for batch loader |
| **R16** | ✅ | `subscriptions.go` emits AFTER cache write in provider |
| **R17** | ✅ | Provider goroutine has `defer func() { recover(); log }` wrapper |
| **S10** | ✅ | `Process.args` and `Process.cwd` are slow-path opt-in via loaders |
| **S15a** | ✅ | Schema partial lives at `daemon/ps/schema.graphql` (copied from constitution) |
| **S15b** | ✅ | `extend type Host` declared in this domain's partial; resolver lives in `resolver_host_ext.go` |
| **S15c** | ✅ | No scalar re-declaration; only `extend type` |
| **S16a** | ✅ | Typed core covers `Process` with `Host.processes(filter)` |
| **S16b** | ✅ | `Query.ps(tool, args)` pass-through with 30s timeout + concurrency cap 4 |
| **O10** | ✅ | `cwdLoader` coalesces N cwd lookups into one `lsof -p <pids>` call per request |
| **T1** | ✅ | `resolver_process_test.go` — stubbed service, asserts all typed fields |
| **T3** | ✅ | All assertions can fail |
| **T5** | ✅ | `loaders_test.go` — counts underlying fetch calls, asserts ≤1 per batch |
| **T7** | ✅ | `resolver_query_test.go` — timeout honored, concurrency cap enforced |

## Cross-domain interfaces DEFINED here (consumer-owns per R4)

```go
// GitService — narrow interface for Process.worktree resolution.
type GitService interface {
    WorktreeByPath(ctx context.Context, path string) (*graphql.Worktree, error)
}

// ClaudeInstanceService — narrow interface for Process.claudeInstance resolution.
type ClaudeInstanceService interface {
    InstanceByPID(ctx context.Context, pid int) (*graphql.ClaudeInstance, error)
}
```

## Cross-domain services CONSUMED

| Service | Interface | Provides |
|---|---|---|
| `git` domain | `GitService` (defined here) | `Process.worktree` via cwd path lookup |
| `claude-instance` domain | `ClaudeInstanceService` (defined here) | `Process.claudeInstance` |

## File map

| File | Rule | Purpose |
|---|---|---|
| `service.go` | R2 | `Service` interface + `NewService` constructor |
| `provider.go` | R10, R17 | Cache + poll loop (moved from `internal/server/providers/ps/provider.go`) |
| `adapter.go` | L4, O10 | Shellout I/O (moved from `internal/server/providers/ps/adapter.go`) |
| `types.go` | R8 | `Process`, `ProcessID`, `ProcessFilter` domain types |
| `parse.go` | — | Pure parse helpers (moved verbatim) |
| `loaders.go` | R3, O10, T5 | `cwdLoader` + `argsLoader` DataLoader-shaped batch loaders |
| `resolver_process.go` | R6, T1 | `processResolver` — typed `Process` fields |
| `resolver_host_ext.go` | R6, S15b | `extend type Host { processes(filter) }` |
| `resolver_query.go` | S16b, T7 | `Query.ps(tool, args)` pass-through with guards |
| `resolver_subscription.go` | R16, T6 | `Subscription.processes` — emit after cache write |
| `resolver_process_test.go` | T1, T3 | Resolver unit tests with stubbed service |
| `loaders_test.go` | T5 | Loader coalescing count verification |
| `resolver_query_test.go` | T7 | Pass-through guard tests |
| `schema.graphql` | S15a | Domain schema partial |

## BLOCKED

None.
