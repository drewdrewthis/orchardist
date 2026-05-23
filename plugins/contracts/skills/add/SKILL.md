---
name: add
description: "File a new contract committing the calling session to a deliverable. Use when the user types /contracts:add, says 'file a contract', 'create a contract', 'I need to commit to', 'add contract for', 'new contract'. Args: summary (required) plus optional --reasoning, --source. Owner is auto-derived from the runtime session id."
user-invocable: true
argument-hint: "<summary> [--reasoning \"<why>\"] [--source <pointer>]"
allowed-tools:
  - Bash
  - mcp__plugin_claude-contracts_contracts__add_contract
---

# /contracts:add — File a contract

Wraps `add_contract`. Logs a `started` event with the calling session as
owner.

## Argument parsing

- First positional (or quoted block): `summary` — required, imperative one-liner.
- `--reasoning "<text>"` — why this contract is being filed (audit-trail).
  Defaults to `"contract filed"`.
- `--source <pointer>` — optional. e.g. `conversation:<uuid>`,
  `pr:owner/repo/N`, `issue:owner/repo/N`.

If `summary` is missing or whitespace-only, refuse and print usage.

## Steps

### 1. Resolve owner — `machine:project:session_id`

```bash
SESSION_ID="${CLAUDE_CODE_SESSION_ID:-}"
if [ -z "$SESSION_ID" ]; then
  TMUX_SESSION="$(tmux display-message -p '#S' 2>/dev/null || echo "")"
  STATE_FILE="/tmp/orchard-claude-${TMUX_SESSION}.json"
  [ -f "$STATE_FILE" ] && SESSION_ID="$(jq -r '.session_id' "$STATE_FILE")"
fi
MACHINE="$(hostname -s 2>/dev/null || hostname)"
PROJECT="${ORCHARD_PROJECT:-claude}"
OWNER="${MACHINE}:${PROJECT}:${SESSION_ID}"
AGENT_NAME="$(tmux display-message -p '#S' 2>/dev/null || echo "${USER:-unknown}")"
```

If `SESSION_ID` is empty, **refuse** with `cannot resolve session_id —
refusing to file. Stop hook would not see this contract.` Do NOT invent a
value.

### 2. Call add_contract

```jsonc
mcp__plugin_claude-contracts_contracts__add_contract({
  summary: "<summary>",
  reasoning: "<--reasoning value, or 'contract filed'>",
  owner: "<OWNER triple>",
  created_by: "<AGENT_NAME>",
  source: "<--source value, omitted if not given>"
})
```

### 3. Surface

Print:

```
Filed: <id>
Summary: <one-line trim>
Owner: <OWNER>
Source: <source or none>
```

## Notes

- Owner is `machine:project:session_id` per references/contracts.md.
- Don't re-file on failure. Surface the error verbatim.
