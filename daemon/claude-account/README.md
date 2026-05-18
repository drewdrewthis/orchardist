# `daemon/claude-account/`

`claude auth status` shellout + `ccusage` quota scraping.

## Owns

- **Types:** `ClaudeAccount`
- **Queries:** `claudeAccounts`
- **Schema partial:** [`schema.graphql`](./schema.graphql) per [S15](../../RULES.md)

## Why this is its own domain

Different source (`claude auth status` + `ccusage`) and cadence (slow, manual) from `claude-jsonls` (fsnotify-driven, sub-second). Per the design call: even though they share the `claude-` prefix, modules with different sources / cadences / failure modes stay separate until they genuinely become indistinguishable.

## Cross-domain back-edges (resolved here)

| Field | Owning domain |
|---|---|
| `ClaudeAccount.host` | [`host-identity`](../host-identity/) |
| `ClaudeAccount.instances` | [`claude-jsonls`](../claude-jsonls/) |

## Current source location (pre-refactor)

- `internal/server/providers/claudeaccount/`

## Constitution citations

- [L4](../../RULES.md): `claude auth status` poll cached; field resolvers serve from cache
- [O6](../../RULES.md): adaptive polling — quota changes slowly, so default cadence is generous
- [S5](../../RULES.md): nullability discipline — `quotaUsed`/`quotaCap`/`quotaResetsAt` are nullable because `ccusage` may not yet have observed traffic. Nullability conveys "unknown," not "zero."
