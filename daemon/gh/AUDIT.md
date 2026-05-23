# `daemon/gh` — Refactor Audit

## Rules applicable to this domain

| Rule | How satisfied | File |
|---|---|---|
| **L1** | Mutations exec `scripts/gh-*.sh`; daemon is a thin façade | `mutations.go`, `scripts/` |
| **L2** | Each script emits `{"ok":bool,"data"?:…,"error"?:{…}}` with correct exit codes | `scripts/gh-*.sh` |
| **L3** | Scripts are bash with `#!/usr/bin/env bash` shebang | `scripts/gh-*.sh` |
| **L4** | All GitHub reads resolve in-process from the provider's caches; no script exec on read path | `provider.go`, `adapter.go` |
| **L5** | Every mutation (`reviewPullRequest`, `labelPullRequest`, `commentOnPullRequest`, `createIssue`) execs the matching script | `mutations.go` |
| **L9** | No persisted state; caches are projections of GitHub API truth, rebuildable on restart | `provider.go` |
| **R1** | All code in `daemon/gh/` — package-by-feature, not by layer | layout |
| **R2** | `service.go` is the ONLY public API; `provider.go` and `adapter.go` are internal | `service.go` |
| **R3** | Field resolvers go through loaders; no `Snapshot()` in field resolver paths | `loaders.go`, `resolver_pull_request.go`, `resolver_issue.go`, `resolver_workflow_run.go` |
| **R4** | Consumer-defined narrow interfaces in `service.go` | `service.go` |
| **R6** | One resolver file per GraphQL type: `PullRequest`, `Issue`, `WorkflowRun` | `resolver_pull_request.go`, `resolver_issue.go`, `resolver_workflow_run.go` |
| **R8** | Wrapped errors throughout; `errors.Is` + typed sentinels | `service.go`, `adapter.go` |
| **R9** | `context.Context` first on every blocking call | all files |
| **R10** | Goroutine ownership: provider owns subscribe goroutine shutdown; subscription goroutines cleanup on ctx.Done | `subscriptions.go` |
| **R12** | `<-chan T` in public subscribe returns | `service.go` |
| **R13** | `sync.RWMutex` for read-heavy maps; `sync.Once` for auth | `provider.go` |
| **R16** | Subscriptions emit AFTER cache write | `subscriptions.go` |
| **R17** | Subscription goroutines wrap loop in recover+log | `subscriptions.go` |
| **M1** | `schema.graphql` is the canonical mutation enumeration | `schema.graphql` |
| **M4** | Mutations validate input at resolver boundary before exec | `mutations.go` |
| **M5** | Idempotency documented per mutation | `mutations.go` |
| **O4** | Cache hit/miss via structured slog logging | `provider.go` |
| **O11** | Cache policy explicit: stale-while-revalidate for enrichment (read-through for basic, SWR for enrichment) | `provider.go`, `service.go` |
| **O12** | Stale-while-revalidate promoted to first-class pattern for enrichment and rate-limit | `provider.go` |
| **S9** | Rate-limit, auth errors are typed (ErrRateLimitedT, ErrNotAuthenticated) | `adapter.go` |
| **S15a** | `schema.graphql` already present in constitution skeleton | `schema.graphql` |
| **S15c** | Root scalars/types declared in `daemon/schema.graphql` only; this partial uses `extend type` | `schema.graphql` |
| **S16a** | Typed core: `pullRequests`, `openPullRequests`, `issues`, `issue`, `pullRequest`, `workflowRuns` | `resolver_pull_request.go`, `resolver_issue.go`, `resolver_workflow_run.go` |
| **S16b** | Pass-through `Query.gh` with: top-level only, 30s timeout, concurrency cap 4, no cache | `resolver_passthrough.go` |
| **T1** | Resolver tests against stub service | `resolver_pull_request_test.go`, `resolver_issue_test.go` |
| **T2** | Script envelope tests on success and failure paths | `scripts/gh-*_test.sh` |
| **T5** | Loader coalescing verified by counting underlying fetches | `loaders_test.go` |
| **T7** | Pass-through L4 guard tests | `resolver_passthrough_test.go` |

## Cross-domain interfaces

### Defined by this domain (exported for consumers)

`Service` interface in `daemon/gh/service.go` — the contract consumers import.

### Consumed by other domains

- `git` domain consumes `gh.Service` for `Worktree.pr` / `Worktree.issue` back-edges (per S15b — those resolvers live in `daemon/git/`; they import `daemon/gh`'s `Service` interface).

### What the `git` domain needs from `gh`:

```go
type GHLookup interface {
    GetPullRequest(ctx context.Context, key PullRequestKey) (PullRequest, error)
    GetIssue(ctx context.Context, key IssueKey) (Issue, error)
    ListPullRequests(ctx context.Context, owner, name string, state PullRequestState) ([]PullRequest, error)
}
```

These are already on `Service`. The `git` domain will define a narrower interface in its own module (R4 ISP).

## File map

| File | Purpose |
|---|---|
| `service.go` | `Service` interface + `NewService()` constructor wrapping `*Provider` |
| `provider.go` | In-process cache, rate-limit SWR, auth bootstrap, subscription fanout |
| `adapter.go` | HTTP I/O: Client, endpoints, pagination, GraphQL, auth shellout |
| `resolver_pull_request.go` | `PullRequest` type resolver (enrichment fields via loader) |
| `resolver_issue.go` | `Issue` type resolver (labels, dependency edges) |
| `resolver_workflow_run.go` | `WorkflowRun` type resolver |
| `resolver_passthrough.go` | `Query.gh` pass-through with S16b guards |
| `loaders.go` | `PullRequestByKey`, `IssueByKey`, `PREnrichmentBatch` dataloaders |
| `mutations.go` | `reviewPullRequest`, `labelPullRequest`, `commentOnPullRequest`, `createIssue` |
| `subscriptions.go` | `pullRequestChanged`, `runChanged` subscription fanout |
| `mappers.go` | Provider→GraphQL projections (isolated from generated types) |
| `service_test.go` | Unit tests: T1 resolver projections against stub service |
| `loaders_test.go` | T5: loader coalescing fetch-count assertion |
| `resolver_passthrough_test.go` | T7: L4 guard tests (timeout, concurrency cap) |

## BLOCKED

None. All inputs present; current source fully read.
