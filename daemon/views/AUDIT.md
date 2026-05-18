# daemon/views — Audit

## Applicable rules

| Rule | How satisfied |
|---|---|
| **R1** | Code lives in `daemon/views/`, not a categorical layer |
| **R2** | `service.go` defines `WorkViewService` interface — the only surface consumers import |
| **R4** | Narrow consumer-defined interfaces per contributing domain (`GitService`, `TmuxService`, `ClaudeInstanceService`, `MetaService`) — all defined in this module, never importing provider types directly |
| **R5** | Each cross-domain interface is defined here (consumer module); wired via `Service.SetXxx` — no provider leakage |
| **R6** | `resolver_workview.go` owns `WorkView` type. Single concern per file. |
| **R7** | New domain types extend without editing other domains' files |
| **R8** | Error style: `fmt.Errorf` wrapping; no panics |
| **R9** | `ctx context.Context` threaded through all blocking calls |
| **R14** | Names: `WorkViewService`, `GitService`, `TmuxService`, `ClaudeInstanceService`, `MetaService` — all honest |
| **R17** | No long-running goroutines in this domain (read-only, no provider/poll loop) |
| **S14** | WorkView DELEGATES to per-domain services — does NOT re-implement joins |
| **S15a** | `daemon/views/schema.graphql` is the canonical schema partial |
| **S15c** | Scalars/root types declared only in root `daemon/schema.graphql`; this partial uses `extend type Query` only |
| **O2** | Lazy field resolution: WorkView fields only resolve when the client selects them (GraphQL default) |
| **T1** | `service_test.go` / `resolver_workview_test.go` test resolver fields against stubbed services |
| **T4** | `integration/workview_test.go` tests workView at the GraphQL boundary with in-process fakes |

## File map

| File | Purpose |
|---|---|
| `service.go` | `WorkViewService` interface + `Service` concrete impl; ISP interfaces for git/tmux/claude-instance/meta |
| `resolver_workview.go` | `workViewResolver` — thin `Query.workView` + `WorkView.*` field delegation |
| `service_test.go` | T1 unit tests: stubbed service returns correct projections |
| `integration/workview_test.go` | T4 integration: real GraphQL query against in-process resolver tree |

## Cross-domain interfaces (defined here, per R4 ISP)

```go
// GitService — the slice of git domain the views domain needs.
type GitService interface {
    Repos(ctx context.Context) ([]*graphql.Repo, error)
}

// TmuxService — the slice of tmux domain views needs.
type TmuxService interface {
    TmuxSessions(ctx context.Context, filter *graphql.TmuxSessionFilter) ([]*graphql.TmuxSession, error)
}

// ClaudeInstanceService — the slice of claude-instance domain views needs.
type ClaudeInstanceService interface {
    ClaudeInstances(ctx context.Context) ([]*graphql.ClaudeInstance, error)
}

// MetaService — the slice of daemon-meta domain views needs.
type MetaService interface {
    NowRFC3339() *string
}
```

## Consumed services

- `GitService` (owner: `daemon/git`)
- `TmuxService` (owner: `daemon/tmux`)
- `ClaudeInstanceService` (owner: `daemon/claude-instance`)
- `MetaService` (owner: `daemon/daemon-meta`)

All consumed via narrow interfaces defined in THIS module, per R4.

## Notes

`views` is read-only. No mutations, no subscriptions, no pass-through escape hatch.
No provider loop, no goroutines → R17 goroutine recovery rule is vacuous here.
