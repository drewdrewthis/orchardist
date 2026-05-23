# `daemon/contracts/`

Agent delivery commitments parsed from the claude-contracts and
conversation-contracts plugins' tool_use events.

## Owns

- **Types:** `Contract`, `ContractStatus`, `ContractReason`
- **Inputs:** `ContractFilter`
- **Queries:** `contract`, `contracts`
- **Schema partial:** [`schema.graphql`](./schema.graphql) per [S15a](../../RULES.md)

## v0.8 model

Two statuses (`SIGNED` → `CLOSED`) and a close `ContractReason`
(`DELIVERED` | `ABANDONED`). The earlier 9-status machine (v0.7) is
removed — the daemon never wrote nor read it in production, so there
is no migration path to preserve.

## How contracts read from jsonls

Contract records ride on the same JSONL stream as Conversation records
(the plugins append `tool_use` events for `open_contract` /
`close_contract` to `~/.claude/projects/<...>.jsonl`). This domain
CONSUMES [`claude-jsonls`](../claude-jsonls/)'s service to read those
records — it does not parse jsonl files directly.

The state-machine fold (event records → current `ContractStatus` +
`ContractReason`) will live in this domain's `service.go` once the
daemon migration brings the live implementation here. Until then, the
live implementation is at:

- `internal/server/providers/contracts/`

The schema partial in this directory IS the authoritative contract for
the future migration — Go code that lives here later must match it.

## Cross-domain inputs (this domain CONSUMES)

| Input | Owning domain |
|---|---|
| jsonl records typed as `open_contract` / `close_contract` tool_use | [`claude-jsonls`](../claude-jsonls/) |

## Constitution citations

- [L9](../../RULES.md): contract state is a projection of jsonl event records — no persisted state in this domain
- [R1, R5](../../RULES.md): consumes claude-jsonls' service, owns its own types and state machine
- [R3](../../RULES.md): list/single lookups go through `ContractsByID` + `ContractsByOwner` loaders
- [T1](../../RULES.md): state-machine fold has its own unit tests (stub event stream → assert fold)
