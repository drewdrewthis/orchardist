---
name: done
description: "Mark a contract delivered. Thin wrapper for update_contract status=delivered. Use when the user types /contracts:done, says 'I'm done with', 'ship contract', 'deliver contract', 'mark as done', 'report done'. Args: contract-id (or auto-resolve when exactly one open contract is owned by self) + --reasoning. To abandon, prefix --reasoning with 'abandoned:'."
user-invocable: true
argument-hint: "[<contract-id>] --reasoning \"<text>\""
allowed-tools:
  - Bash
  - mcp__plugin_claude-contracts_contracts__update_contract
  - mcp__plugin_claude-contracts_contracts__list_contracts
  - mcp__plugin_claude-contracts_contracts__get_contract
---

# /contracts:done — Mark a contract delivered

Wraps `update_contract status=delivered`. Logs a `delivered` event with
the supplied reasoning. To signal abandonment instead of completion,
prefix `--reasoning` with `abandoned:`.

## Argument parsing

- First positional (if it looks like `C-YYYY-MM-DD-XXXXXXXX`): contract-id.
- `--reasoning "<text>"` — required. Audit-trail explanation. Prefix with
  `abandoned:` to mark as abandoned.

If `--reasoning` is missing, refuse.

## Steps

### 1. Resolve contract-id (auto-mode)

If contract-id is omitted:

1. Resolve owner triple (same as `/contracts:add`).
2. Call `mcp__plugin_claude-contracts_contracts__list_contracts({filter: {status: "started"}})`.
3. Filter results to those whose `owner` field contains your session_id.
4. If exactly one match → use its id.
5. Otherwise → refuse: `ambiguous (N open contracts) — pass contract-id explicitly`.

### 2. Identify caller

`AGENT_NAME` = tmux session name (or `$USER`).

### 3. Call update_contract

```jsonc
mcp__plugin_claude-contracts_contracts__update_contract({
  contract_id: "<id>",
  status: "delivered",
  reasoning: "<--reasoning value>",
  created_by: "<AGENT_NAME>"
})
```

### 4. Surface

```
Delivered: <id>
Reasoning: <--reasoning value>
```

If reasoning starts with `abandoned:`, the surface should mention it
explicitly:

```
Delivered (abandoned): <id>
Reason: <text after 'abandoned:'>
```
