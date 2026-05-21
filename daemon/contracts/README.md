# `daemon/contracts/`

Agent delivery commitments parsed from the Contracts-plugin records.

## Owns

- **Types:** `Contract`, `ContractStatus`, `ContractQuestion`
- **Inputs:** `ContractFilter`
- **Queries:** `contract`, `contracts`
- **Schema partial:** [`schema.graphql`](./schema.graphql) per [S15a](../../RULES.md)

## Why this exists

Per devils-advocate review of PR #618: Contracts has its own state machine (9 statuses: OPEN → DELIVERED_PENDING_VALIDATION → ... → SATISFIED | CANCELLED | JUDGE_REJECTED_TERMINAL). That state machine is a domain concern in its own right — bundling it inside `claude-jsonls/` (whose mission is "tail jsonls") was the layer-by-file-format thinking [R1](../../RULES.md) forbids.

## How contracts read from jsonls

Contract records ride on the same JSONL stream as Conversation records (the Contracts plugin appends to `~/.claude/projects/<...>.jsonl`). This domain CONSUMES [`claude-jsonls`](../claude-jsonls/)'s service to read those records — it does not parse jsonl files directly.

The state-machine fold (event records → current `ContractStatus`) lives in this domain's `service.go`.

## Cross-domain inputs (this domain CONSUMES)

| Input | Owning domain |
|---|---|
| jsonl records typed as `contracts.*` events | [`claude-jsonls`](../claude-jsonls/) |

## Current source location (pre-refactor)

- `internal/server/providers/contracts/`

## Constitution citations

- [L9](../../RULES.md): contract state is a projection of jsonl event records — no persisted state in this domain
- [R1, R5](../../RULES.md): consumes claude-jsonls' service, owns its own types and state machine
- [R3](../../RULES.md): list/single lookups go through `ContractsByID` + `ContractsByOwner` loaders
- [T1](../../RULES.md): state-machine fold has its own unit tests (stub event stream → assert fold)
