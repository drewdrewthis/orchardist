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

## Cross-domain fields on Worktree (S15b: declared in the producing domain)

Per [S15b](../../RULES.md), cross-domain `extend type` blocks and their resolvers live in the domain that **produces** the data — NOT in the type owner (`git`). Worktree is owned here; the cross-domain fields live elsewhere:

| Field | Producing domain (schema + resolver) |
|---|---|
| `Worktree.processes` | [`ps`](../ps/) |
| `Worktree.tmuxPanes` | [`tmux`](../tmux/) |
| `Worktree.tmuxSession` | [`tmux`](../tmux/) |
| `Worktree.claudeInstances` | [`claude-instance`](../claude-instance/) |
| `Worktree.pr` | [`gh`](../gh/) |
| `Worktree.issue` | [`gh`](../gh/) |

Each producing domain imports only a narrow service interface from `git` (per [R4, R5](../../RULES.md)) to look up the Worktree's `path` and `branch` — never the `git` provider directly.

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
