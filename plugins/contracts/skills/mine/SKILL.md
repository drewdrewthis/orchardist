---
name: mine
description: "List the calling session's owned contracts. Use when the user types /contracts:mine, asks 'what contracts am I on the hook for', 'what do I own', 'what's open on me'. Compact, scannable output: status, summary, last reasoning. No args."
user-invocable: true
allowed-tools:
  - Bash
  - mcp__plugin_claude-contracts_contracts__list_contracts
---

# /contracts:mine — Show my owned contracts

Compact dashboard of the calling session's contracts. Shows everything
(started + blocked + delivered) so the model can decide what's interesting.

## Steps

### 1. Resolve session_id

```bash
SESSION_ID="${CLAUDE_CODE_SESSION_ID:-}"
if [ -z "$SESSION_ID" ]; then
  TMUX_SESSION="$(tmux display-message -p '#S' 2>/dev/null || echo "")"
  STATE_FILE="/tmp/orchard-claude-${TMUX_SESSION}.json"
  if [ -f "$STATE_FILE" ]; then
    SESSION_ID="$(jq -r '.session_id' "$STATE_FILE")"
  fi
fi
```

If still empty, refuse: `cannot resolve session_id — refusing to list`.

### 2. List

Call `mcp__plugin_claude-contracts_contracts__list_contracts` with no
filter, then filter results client-side to those whose `owner` field
contains your session_id.

### 3. Render

For each match, output one row:

```
<status-badge> <id>  <summary-truncated-to-80>  <last-reasoning-truncated-to-60>
```

Status badges: `[STARTED]`, `[BLOCKED]`, `[DONE]`.

Sort: started first (newest updated_at first), then blocked, then
delivered. Cap at 30 rows; if more, print `… +N older` at the bottom.

### 4. Header / footer

Top: `Owned by session <short-id>`.
Bottom: `<N> total · <S> started · <B> blocked · <D> delivered`.

### 5. Empty case

`no contracts owned by this session.`

## Notes

- Never print full session_ids in the rendered output — last 8 chars only.
- Don't error on empty list. That's the happy case.
