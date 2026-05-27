---
name: close-conversation
description: Close the open conversation contract for the current session after a loose-ends inventory. Use when the user signals the session is wrapping up ("we're done here", "close the conversation", "/close-conversation"). Runs the inventory, then either closes the conversation contract as delivered or files child contracts for named open items.
---

# /close-conversation

Close the open conversation contract for the current session. Unlike the generic `/close-contract`, this skill runs a **loose-ends inventory** first, so the conversation contract closes only when there is genuinely nothing left dangling.

Contracts are `orchard_contract` sentinels in the session jsonl (open minus close, folded by id) — there is no MCP server and no daemon. The shared fold script is the single source of truth.

## Flow

1. **Fold the open contracts.** Run `scripts/fold-contracts.sh` against this session's jsonl to list every open contract:

   ```bash
   FOLD="$CLAUDE_PLUGIN_ROOT/scripts/fold-contracts.sh"
   ROOT="${CLAUDE_PROJECTS_DIR:-$HOME/.claude/projects}"
   ENC=$(printf '%s' "$PWD" | tr '/' '-' | tr '.' '-')
   bash "$FOLD" "$ROOT/$ENC/$CLAUDE_SESSION_ID.jsonl"
   ```

2. **Identify the conversation contract** by its fixed statement: `user agrees conversation has come to a close and there are no loose ends`. Any other open contract is a **child** contract.

3. **Build the inventory** of what is still open:
   - Open child contracts: every open contract that is NOT the conversation contract.
   - Open TodoWrite items: scan the session jsonl for pending TodoWrite entries (best-effort).

4. **If the inventory is empty** (only the conversation contract is open): close it as **delivered**. Emit a close sentinel for the conversation contract's id with a `Bash` echo, using the session summary as the reason:

   ```bash
   echo "{\"orchard_contract\":\"close\",\"id\":\"<conversation-contract-id>\",\"reason\":\"delivered: <inventory summary>\",\"ts\":\"$(date -u +%Y-%m-%dT%H:%M:%SZ)\"}"
   ```
   Report: "Conversation contract closed as delivered."

5. **If the user names items that are still open:** file a child contract for each via `/open-contract` (one open sentinel per named item). Do NOT close the conversation contract — it stays open. Report: "Conversation contract remains open. Filed child contracts for: <items>."

6. **If the user confirms everything is resolved** (non-empty inventory but they accept it): close the conversation contract as delivered (step 4), folding the inventory summary into the reason for auditability.

## Close paths

| Trigger | Result |
|---------|--------|
| User confirms (empty inventory) | close sentinel, reason `delivered`, conversation contract CLOSED |
| User confirms (non-empty inventory) | close sentinel `delivered` with inventory note, conversation contract CLOSED |
| User names open items | one open sentinel per item (child contracts), conversation contract stays open |
| `/exit`, `/quit`, `/bye` | host treats these as user-accepts-close; close the conversation contract delivered |
| Direct `/close-contract <id>` | closes immediately by id, no inventory prompt (escape hatch) |

## Notes

- The conversation contract deliverable is fixed: `user agrees conversation has come to a close and there are no loose ends`.
- Idempotency: if the fold shows no open conversation contract, report success immediately — nothing to close.
- The close reason should include the inventory summary at the moment of closure for auditability.
