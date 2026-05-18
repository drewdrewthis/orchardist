# `daemon/gh/`

GitHub API: repos (github-side view), issues, pull requests, workflow runs.

## Owns

- **Types:** `PullRequest`, `PullRequestReview`, `Issue`, `Label`, `IssueComment`, `WorkflowRun`
- **Enums:** `MergeableState`, `ReviewDecisionEnum`, `CiStatus`, `PullRequestState`, `IssueState`
- **Queries:** `pullRequests`, `openPullRequests`, `issues`, `issue`, `pullRequest`, `workflowRuns`
- **Subscriptions:** `pullRequestChanged`, `runChanged`
- **Mutations** (to be added in #613, each execs a script per [L5](../../RULES.md)):
  PR review/label/comment, issue create
- **Schema partial:** [`schema.graphql`](./schema.graphql) per [S15](../../RULES.md)

## Note: gh-passthrough lives in daemon-self

`Query.gh(query, variables): JSON` is the federation-aware passthrough escape hatch. It lives in [`daemon-self`](../daemon-self/), not here, because it is plumbing (proxies arbitrary GitHub GraphQL through orchard) rather than a typed gh field.

## Current source location (pre-refactor)

- `internal/server/providers/gh/`

## Constitution citations

- [L4](../../RULES.md): GitHub queries cache in-process; no script exec on the hot read path
- [L5](../../RULES.md): mutations exec `scripts/<op>` with `--json` output
- [O12](../../RULES.md): **stale-while-revalidate** is already done here ad-hoc for rate-limit; the refactor promotes it to first-class
- [O11](../../RULES.md): cache policy (read-through vs write-through) is explicit and not mixed within this module
- [S9](../../RULES.md): expected errors (rate-limit, permission-denied) are typed error union results, not `errors[]`
- [O1](../../RULES.md): the EnrichPR consolidation work (#615) lives in this module — duplicate single/batch paths get collapsed
