# `daemon/git/`

Local git: repos, worktrees, branches, refs, status, ahead/behind, remote heads.

## Owns

- **Types:** `Repo`, `Worktree`
- **Queries (typed core):** `Query.repos`
- **Query (pass-through, S16):** `git(worktreeId, args): JSON` — arbitrary `git` invocation against a known worktree
- **Subscriptions:** `Subscription.worktreeChanged`
- **Mutations** (to be added in #613, each execs a script per [L5](../../RULES.md)):
  `worktreeCreate`, `worktreeRemove`, `worktreeMove`, `fetch`, `pull`, `push`
- **Schema partial:** [`schema.graphql`](./schema.graphql) — partials are owned per-domain per [S15](../../RULES.md)

## Repo discovery

Walking the watched-projects list + known dirs to find git repos is a **startup routine** inside this domain — not its own domain. The data it populates (the set of `Repo` nodes) belongs to `git`.

## Cross-domain fields on Worktree (resolved here)

These fields are declared in this domain's partial; the resolver lives here and calls the owning domain's service:

| Field | Owning domain |
|---|---|
| `Worktree.processes` | [`ps`](../ps/) |
| `Worktree.tmuxPanes` | [`tmux`](../tmux/) |
| `Worktree.tmuxSession` | [`tmux`](../tmux/) |
| `Worktree.claudeInstances` | [`claude-jsonls`](../claude-jsonls/) |
| `Worktree.pr` | [`gh`](../gh/) |
| `Worktree.issue` | [`gh`](../gh/) |

The cross-domain join IS the GraphQL graph; per [R5](../../RULES.md), the resolver here imports the consumer's service interface and does not reach into another domain's provider.

## Current source location (pre-refactor)

- `internal/server/providers/git/`
- `internal/server/providers/config/` (Repo node provider — misnamed today; folds in here)
- `internal/server/providers/repodiscovery/` (startup routine; folds in here)

## Constitution citations

- [L1, L2, L5](../../RULES.md): mutations exec `scripts/<op>` with `--json` output
- [R1, R2](../../RULES.md): package-by-feature, service is the contract
- [R3](../../RULES.md): no `Snapshot()` in field resolvers — use DataLoaders
- [S1, S2](../../RULES.md): paginate worktree lists (S1), Node implementation (S2)
- [O6](../../RULES.md): steady-state poll cost is bounded
