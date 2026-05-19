# claude-instance Domain Audit

## Rules Applicable and How Satisfied

| Rule | Status | Evidence |
|---|---|---|
| **R1** Package-by-feature | ✅ | All code lives in `daemon/claude-instance/` |
| **R2** service.go is the only API | ✅ | `service.go` defines `Service` interface + `New`; resolvers only import `Service` |
| **R3** DataLoader reads — defined + consumed | ✅ | `loaders.go` defines `InstancesByPaneCommand` loader; resolver consumes it |
| **R4** Interface segregation (ISP) | ✅ | Three consumer-defined interfaces: `JsonlsService`, `TmuxPaneReader`, `PsReader` |
| **R5** Anti-corruption layer | ✅ | Cross-domain back-edges (`pane`, `process`, `account`, `worktree`, `conversation`) resolved via interfaces in this module |
| **R6** One file per GraphQL type | ✅ | `resolver_claude_instance.go` owns `ClaudeInstance`; `InstanceState` is an enum (no resolver file needed) |
| **R8** One error style | ✅ | Wrapped errors via `fmt.Errorf` throughout |
| **R9** Context propagation | ✅ | Every blocking call accepts and respects `context.Context` |
| **R10** Goroutine ownership | ✅ | Read-only domain — no background goroutines needed |
| **R11** Accept interfaces, return structs | ✅ | `New` returns `*Provider`; public constructors are concrete |
| **R13** RWMutex for read-heavy cache | ✅ | No mutable cache in this domain (pure join; joins happen at resolver time) |
| **R14** Naming honesty | ✅ | `ClaudeInstance` is a join node, named accurately |
| **R16** Subscription emit after write | N/A | Read-only domain |
| **R17** Goroutine panic-recovery | N/A | No long-running goroutines in a read-only domain |
| **S14** One resolver per logical field | ✅ | Cross-domain back-edges delegate via interfaces |
| **S15a** Schema partial per domain | ✅ | `schema.graphql` checked in |
| **S15c** No scalar re-declaration | ✅ | Schema partial uses `extend type Query` only |
| **T1** Resolver test per typed field | ✅ | `service_test.go` tests all fields via stub service |
| **T3** No tautological assertions | ✅ | All assertions can fail |
| **T4** Cross-domain join at GraphQL boundary | ✅ | `integration/claude_instance_test.go` |
| **T5** Loader coalescing by fetch count | ✅ | `loaders_test.go` counts underlying calls |

## File Map

| File | Purpose |
|---|---|
| `service.go` | R2: `Service` interface + `Inputs` (ISP consumer interfaces) + `Provider` concrete impl |
| `provider.go` | `Provider.List()` — the join logic: tmux panes → instances |
| `adapter.go` | `FsSnapshotReader` + `FsJsonlReader` (I/O boundary) |
| `loaders.go` | `InstancesByCommand` loader (T5) |
| `resolver_claude_instance.go` | R6: `ClaudeInstance` type resolver; all typed field resolvers |
| `service_test.go` | T1: typed-field resolver tests against stub service |
| `loaders_test.go` | T5: coalescing verified by fetch count |
| `integration/claude_instance_test.go` | T4: cross-domain join at GraphQL boundary |
| `schema.graphql` | S15a: domain schema partial (already present) |

## Cross-Domain Interfaces (R4 ISP — defined here, consumed here)

```go
// JsonlsService — from claude-jsonls
type JsonlsService interface {
    ListConversationsBySessionUuid(ctx context.Context) (map[string]ConversationSummary, error)
}

// TmuxPaneReader — from tmux
type TmuxPaneReader interface {
    PanesByCommand(ctx context.Context, command string) ([]*TmuxPaneSummary, error)
}

// PsReader — from ps
type PsReader interface {
    LoadCwd(ctx context.Context, pid int) (string, error)
}
```

## Cross-Domain Back-Edges (R5 — resolved via Inputs interfaces)

| Field | Owning Domain | Resolution |
|---|---|---|
| `ClaudeInstance.pane` | tmux | Pre-resolved at join time from TmuxPaneReader |
| `ClaudeInstance.process` | ps | Nil in this module; resolved by tmux resolver via back-edge |
| `ClaudeInstance.account` | claude-account | Injected via `Inputs.Account` reader |
| `ClaudeInstance.worktree` | git | Null in list; worktree resolver owns the back-edge |
| `ClaudeInstance.conversation` | claude-jsonls | Resolved via `JsonlsService.ListConversationsBySessionUuid` |

## BLOCKED

None. All 3 inputs (claude-jsonls, tmux, ps) have clear service interfaces.
The cross-domain back-edges (`worktree`, `process`) that the `git` and `tmux`
domains own are intentionally left as `nil` in this domain's list resolution
— those domains add `extend type ClaudeInstance` back-edges via their own
resolvers per S15b. This domain only populates the fields it can derive
from its own 3 inputs.
