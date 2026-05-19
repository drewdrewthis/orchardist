# daemon/git — Domain Audit

## Applicable Rules

| Rule | Status | How satisfied |
|---|---|---|
| **L1** | ✅ | All mutations exec `scripts/git-<op>.sh` |
| **L2** | ✅ | All scripts emit `{ok, data?, error?}` envelope; exit 0 on ok |
| **L3** | ✅ | Scripts are bash with shebang |
| **L4** | ✅ | All reads go through provider cache + loaders. No script exec in field resolvers |
| **L5** | ✅ | `mutations.go`: each mutation execs the matching script and projects `--json` |
| **L8** | ✅ | Mutations return affected `Worktree` or `Repo` node |
| **L9** | ✅ | Provider holds no persisted state; restart re-observes from git on-disk layout |
| **L11** | ✅ | Scripts depend only on git CLI + filesystem; do not call daemon |
| **R1** | ✅ | `daemon/git/` — one directory, one domain |
| **R2** | ✅ | `service.go` exports the only API consumers may call |
| **R3** | ✅ | All field resolvers call loaders, not `Snapshot()` |
| **R4** | ✅ | Cross-domain consumer interfaces defined in this module (PsReader, TmuxReader, ClaudeReader, GhReader) |
| **R5** | ✅ | Cross-domain fields import service interfaces, not providers |
| **R6** | ✅ | `resolver_repo.go`, `resolver_worktree.go` — one type per file |
| **R8** | ✅ | Sentinel errors via `errors.Is`/`fmt.Errorf %w` throughout |
| **R9** | ✅ | All blocking calls accept `context.Context` first |
| **R10** | ✅ | Provider owns all goroutines; Stop() drains wg before returning |
| **R12** | ✅ | Subscribe returns `<-chan` (receive-only) |
| **R13** | ✅ | `RWMutex` for read-heavy store; `Mutex` for subs fan-out |
| **R16** | ✅ | `subscriptions.go`: emits after cache write |
| **R17** | ✅ | `provider.go` goroutines have panic-recover + slog structured logging |
| **S4** | ✅ | Mutations take typed Input objects |
| **S5** | ✅ | Non-null on required fields; nullable on optional |
| **S8** | ✅ | Mutations return affected nodes |
| **S13** | ✅ | Mutations are verbs (`worktreeCreate` etc.), queries are nouns |
| **S15a** | ✅ | Schema partial lives at `daemon/git/schema.graphql` |
| **S15b** | ✅ | `extend type Worktree` blocks with cross-domain fields declared here |
| **S15c** | ✅ | Scalars + root types NOT re-declared here |
| **S16a** | ✅ | Typed core: `repos`, `worktreeChanged` with loaders |
| **S16b** | ✅ | Pass-through: `git(worktreeId, args): JSON` with 30s timeout, concurrency cap 4 |
| **M1** | ✅ | Mutations enumerated in `schema.graphql` only |
| **M4** | ✅ | Input validation at resolver boundary before exec |
| **M5** | ✅ | Idempotency documented per mutation |
| **T1** | ✅ | `resolver_repo_test.go`, `resolver_worktree_test.go` test each typed field |
| **T2** | ✅ | Script tests assert `--json` envelope on success and failure |
| **T5** | ✅ | Loader coalescing verified by counting underlying fetches |
| **T7** | ✅ | Pass-through guards tested |
| **O6** | ✅ | Provider polls only on fsnotify events; no fixed-interval ticker |
| **O11** | ✅ | Read-through cache; policy documented in provider.go |

## File Map

| File | Purpose |
|---|---|
| `service.go` | Service interface + concrete impl wrapping provider + loader; the ONLY API consumers import |
| `provider.go` | In-process cache backed by git on-disk reads + fsnotify watcher; migrated from `internal/server/providers/git/` |
| `adapter.go` | Git on-disk I/O: reads `.git/HEAD`, `.git/worktrees/*`, packed-refs. Stateless |
| `watcher.go` | Per-project fsnotify watcher; closes invalidation channel on stop |
| `config_provider.go` | Repo config provider (migrated from `internal/server/providers/config/`); surfaces `Repo` nodes |
| `config_adapter.go` | JSON file adapter for `~/.orchard/config.json`; migrated from `internal/server/providers/config/adapter.go` |
| `discovery.go` | Repo discovery routine (migrated from `internal/server/providers/repodiscovery/`); startup routine, not a domain |
| `loaders.go` | DataLoaders: `RepoByID`, `WorktreeByID`, `WorktreesByProjectID` per ADR-022 axes |
| `resolver_repo.go` | Thin resolver for `Repo` type: `id`, `slug`, `path`, `worktrees` |
| `resolver_worktree.go` | Thin resolver for `Worktree` type: all scalar fields + cross-domain field delegation |
| `mutations.go` | Mutation resolvers: each execs `scripts/git-<op>.sh --json`, projects output |
| `subscriptions.go` | `worktreeChanged` subscription: emits after provider cache write |
| `passthrough.go` | `git(worktreeId, args): JSON` with L4 guards (30s timeout, concurrency cap 4) |
| `*_test.go` | Tests: T1 resolver, T2 script envelope, T5 loader coalescing, T7 pass-through guards |

## Cross-domain interfaces defined here (R4)

Consumer interfaces (defined in this module, implemented by sibling services):

```go
// PsReader is what this domain needs from the ps domain.
type PsReader interface {
    ProcessesByCwd(ctx context.Context, cwd string) ([]Process, error)
}

// TmuxReader is what this domain needs from the tmux domain.
type TmuxReader interface {
    PanesByCwd(ctx context.Context, cwd string) ([]TmuxPane, error)
    SessionByPane(ctx context.Context, paneID string) (*TmuxSession, error)
}

// ClaudeReader is what this domain needs from the claude-instance domain.
type ClaudeReader interface {
    InstancesByCwd(ctx context.Context, cwd string) ([]ClaudeInstance, error)
}

// GhReader is what this domain needs from the gh domain.
type GhReader interface {
    PRByBranch(ctx context.Context, repoSlug, branch string) (*PullRequest, error)
    IssueByBranch(ctx context.Context, repoSlug, branch string) (*Issue, error)
}
```

## Cross-domain fields on Worktree

Per S15b — declared in THIS partial, resolved HERE by calling the consumer interface:

- `Worktree.processes` → PsReader.ProcessesByCwd
- `Worktree.tmuxPanes` → TmuxReader.PanesByCwd  
- `Worktree.tmuxSession` → TmuxReader.SessionByPane (from first pane)
- `Worktree.claudeInstances` → ClaudeReader.InstancesByCwd
- `Worktree.pr` → GhReader.PRByBranch
- `Worktree.issue` → GhReader.IssueByBranch

## BLOCKED

None.
