---
name: wait
description: "Park a contract by logging a `blocked` event. Thin wrapper for update_contract status=blocked. Use when the user types /contracts:wait, says 'wait for CI', 'park contract', 'block on external', 'cooldown'. Args: contract-id + reason + optional duration (1m, 5m, 30m). Encodes cooldown_until in reasoning when a duration is supplied."
user-invocable: true
argument-hint: "<contract-id> \"<reason>\" [<duration>]"
allowed-tools:
  - Bash
  - mcp__plugin_claude-contracts_contracts__update_contract
---

# /contracts:wait — Park a contract

Wraps `update_contract status=blocked`. The contract stays open in the
sense that it isn't delivered, but the reasoning explains what's blocking
it. When a duration is supplied, the reasoning encodes
`cooldown_until: <ISO timestamp>` so consumers (Stop hook, dashboards) can
treat the block as time-bounded.

## Argument parsing

- First positional: contract-id (required).
- Quoted reason text (required) — what you're waiting on.
- Third positional: duration — `1m`, `5m`, `30m`, `1h` (optional). When
  given, the wrapper computes `cooldown_until` and prepends it to the
  reasoning.

## Steps

### 1. Parse duration → ISO timestamp (optional)

```bash
RAW="${3:-}"
if [[ -n "$RAW" ]]; then
  if [[ "$RAW" =~ ^([0-9]+)m$ ]]; then
    SECS=$(( ${BASH_REMATCH[1]} * 60 ))
  elif [[ "$RAW" =~ ^([0-9]+)h$ ]]; then
    SECS=$(( ${BASH_REMATCH[1]} * 3600 ))
  else
    echo "bad duration: $RAW (use 1m / 5m / 30m / 1h)" >&2
    exit 1
  fi
  UNTIL=$(date -u -v+${SECS}S +"%Y-%m-%dT%H:%M:%SZ" 2>/dev/null \
       || date -u -d "+${SECS} seconds" +"%Y-%m-%dT%H:%M:%SZ")
  REASONING="cooldown_until: ${UNTIL}; ${REASON}"
else
  REASONING="${REASON}"
fi
```

### 2. Identify caller

`AGENT_NAME` = tmux session name or `$USER`.

### 3. Call update_contract

```jsonc
mcp__plugin_claude-contracts_contracts__update_contract({
  contract_id: "<id>",
  status: "blocked",
  reasoning: "<REASONING>",
  created_by: "<AGENT_NAME>"
})
```

### 4. Surface

```
Parked: <id>
Status: blocked
Reasoning: <REASONING>
```

## Notes

- For genuinely-external blocks: CI runs, human ack on Slack, service
  downtime. Don't use it to dodge thinking.
- The reasoning carries the deadline; no separate `cooldown_until` field
  exists in v0.7. Consumers parse `cooldown_until: <ts>` from the reasoning
  if they need it.
