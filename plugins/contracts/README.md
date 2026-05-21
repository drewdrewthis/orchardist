# claude-contracts

Contract authoring + reading + Stop-hook reminder for any Claude session.

**Spec:** [`references/contracts.md`](https://github.com/drewdrewthis/orchard-codex/blob/main/references/contracts.md) (ADR-011, 2026-05-04).

## What a contract is

A record of an agent commitment: "I, this agent, said I would do X." Drew
never authors contracts. A human committing to something belongs in a
different system (TODO list, calendar, etc.) — this surface is for *agent*
commitments only.

## Status surface

Three values. **No others.**

| Status | Meaning |
|---|---|
| `started` | An agent is actively working on this commitment, OR intends to. |
| `blocked` | Work has paused on something. The `reasoning` field explains what. |
| `delivered` | Terminal. Fulfilled OR abandoned (reasoning prefixed `abandoned:`). |

There is no `dropped`, `cancelled`, or `pending_drew_approval` status.
Abandonment collapses to `delivered` with `reasoning: "abandoned: <why>"`.
Terminal states are terminal — consumers read the same "this is done, stop
reminding" signal whether or not the work shipped.

## MCP tools

Three v1 tools (per the spec) plus a convenience reader.

| Tool | Purpose |
|------|---------|
| `add_contract` | File a new contract. Logs the first `started` event. |
| `update_contract` | Append a status event. Status must be `started \| blocked \| delivered`. |
| `list_contracts` | Folded current state of contracts matching an optional filter. |
| `get_contract` | Read one contract's folded state. |

`add_contract` takes `{summary, reasoning?, owner?, source?, created_by?}`.
`update_contract` takes `{contract_id, status, reasoning, owner?, source?, created_by?, summary?}`.
`list_contracts` takes `{filter?: {status?, owner?}}`.

## Storage

JSONL log per contract under `~/.claude/contracts/<id>--<slug>.jsonl`. Every
line is one event with the schema below. The first event creates the
contract; subsequent events update fields.

```jsonc
{
  "timestamp":   "2026-05-06T18:34:56Z",
  "contract_id": "C-2026-05-06-abc12345",
  "status":      "started",                          // started | blocked | delivered
  "summary":     "Stand up Workstream A scaffold",   // creation only; null on updates
  "reasoning":   "kicking off after ADR-011 lock",
  "owner":       "local:claude:920a0b2f-...",        // machine:project:session_id
  "created_by":  "spawn1_v07_rewrite",
  "source":      "conversation:920a0b2f"             // optional
}
```

A Markdown mirror is regenerated next to each JSONL on every event for
human / Obsidian browsing. The `.jsonl` is the source of truth.

## Skills

| Skill | Purpose |
|------|---------|
| `/contracts:add` | Wraps `add_contract`. Resolves owner from runtime. |
| `/contracts:done` | Wraps `update_contract status=delivered`. Auto-resolves contract id when only one is open. |
| `/contracts:wait` | Wraps `update_contract status=blocked`. Encodes optional `cooldown_until: <ts>` in reasoning. |
| `/contracts:mine` | Lists the calling session's contracts. |
| `/contracts:show` | Pretty-prints a contract's Markdown mirror. |

## Hooks

| Hook | Behaviour |
|------|-----------|
| `SessionStart` | Injects header + open-contracts block. Mechanical. |
| `UserPromptSubmit` | Injects open-contracts block. Mechanical. |
| `Stop` | Lists open contracts as a `systemMessage`. **Never blocks.** |

The Stop hook is a reminder, not a gate. The spec is explicit: contracts
are written by explicit MCP calls, never by Stop-hook inference.

## What was retired in v0.7

The v0.6 plugin shipped a much richer surface that the canonical spec
struck out per ADR-011. Retired:

- Statuses: `pending_drew_approval`, `awaiting_cancel_ack`,
  `delivered_pending_validation`, `delivered_pending_parent_validation`,
  `judge_rejected_terminal`, `satisfied`/`cancelled` distinction, `open`.
  All collapsed into the 3-value canonical surface.
- Tools: `approve_contract`, `cancel_contract`, `ack_cancel`,
  `withdraw_cancel_request`, `accept_contract`, `drain_nudges`,
  `set_cooldown`, `add_criterion`, `ask_question`, `answer_question`,
  `timeout_question`, `report_done`. Replaced by `update_contract`.
- Fields: `reports_to`, `parent_contract_id`, `judge_verdict`, `evidence`,
  `cooldown_until`, `closed_on`, `closed_on_reason`, `child_contract_ids`.
  Replaced by `owner`, `reasoning`, `source`.
- Skills: `/contracts:approve`, `/contracts:cancel`, `/contracts:ask`,
  `/contracts:answer`, `/contracts:tree`.
- Bundled judge agent + judge-runner subprocess invocation.
- Stop-hook judge-nudge / blocking loop.
- Per-agent nudge queue files (`_nudges/<agent>.jsonl`).
- Timeouts queue (`_timeouts/pending.jsonl`).

A migration script at `scripts/migrate-v06-to-v07.ts` folds existing
contracts into the new shape (idempotent — checks for a marker event
before re-stamping). Run once per contracts directory.

## Install

```
bash scripts/install.sh
```

Idempotent: re-runnable to repair links. The script symlinks the plugin
into `~/.claude/plugins/`, enables it in `~/.claude/settings.json`, and
runs `bun install` in the server directory.
