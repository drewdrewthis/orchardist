---
name: close-conversation
description: Close the open conversation contract for the current session after a loose-ends inventory. Use when the user signals the session is wrapping up ("we're done here", "close the conversation", "/close-conversation"). Runs the inventory, then either closes the conversation contract as delivered or files child contracts for named open items.
---

# /close-conversation

Close the open conversation contract for the current session. Unlike the generic `/close-contract`, this skill runs a **loose-ends inventory** first, so the conversation contract closes only when there is genuinely nothing left dangling.

Contracts are `orchard_contract` sentinels in the session jsonl (open minus close, folded by id) — there is no MCP server and no daemon. The shared fold script is the single source of truth.

## Flow

1. **Fold the open contracts.** Run `scripts/fold-contracts.sh` — prefer `--session` (scans projects subdirs for the matching jsonl by id), fall back to `--auto` (encoded `$PWD`) when `$CLAUDE_SESSION_ID` is unset.

   The script lives at `<this-skill-dir>/../../scripts/fold-contracts.sh`. The "Base directory" line at the top of this SKILL.md gives you the absolute skill directory; substitute that literal path for `<this-skill-dir>` when you construct the Bash call. Do not use `$CLAUDE_PLUGIN_ROOT` — it's not set in skill subprocesses.

   ```bash
   if [ -n "${CLAUDE_SESSION_ID:-}" ]; then
     bash "<this-skill-dir>/../../scripts/fold-contracts.sh" --session "$CLAUDE_SESSION_ID"
   else
     bash "<this-skill-dir>/../../scripts/fold-contracts.sh" --auto
   fi
   ```

2. **Identify the conversation contract** by its fixed statement: `user agrees conversation has come to a close and there are no loose ends`. Any other open contract is a **child** contract.

3. **Build the inventory** of what is still open:
   - Open child contracts: every open contract that is NOT the conversation contract.
   - Open TodoWrite items: scan the session jsonl for pending TodoWrite entries (best-effort).

4. **If the inventory is empty** (only the conversation contract is open) AND the user typed a close path (`/exit`, `/quit`, `/bye`, `/close-conversation`) — the typing IS the consent. Emit a close sentinel for the conversation contract's id via the shared `scripts/emit-sentinel.sh` (the same script `/close-contract` uses), folding the session summary into the reason. Same `<this-skill-dir>/../../scripts/...` shape as step 1 — substitute the "Base directory" path inline:

   ```bash
   bash "<this-skill-dir>/../../scripts/emit-sentinel.sh" close "<conversation-contract-id>" "delivered: <inventory summary>"
   ```
   Report: "Conversation contract closed as delivered."

5. **If the inventory is empty but the user did NOT type a close path** (e.g. they said "this is done?" or the agent decided the conversation is winding down) — gate the close behind an `AskUserQuestion` confirmation. Present the loose-ends inventory summary and the three options:

   - "Yes — close the conversation contract as delivered"
   - "Keep open — there's more I want to do"
   - "Abandon with reason: `<reason>`"

   Only emit the close sentinel on "Yes". On "Keep open" report the inventory and continue. On "Abandon" emit with `reason: "abandoned: <reason>"`.

6. **If the user names items that are still open:** file a child contract for each via `/open-contract` (one open sentinel per named item). Do NOT close the conversation contract — it stays open. Report: "Conversation contract remains open. Filed child contracts for: <items>."

7. **If the user explicitly confirms everything is resolved** (non-empty inventory but they accept it): close the conversation contract as delivered (step 4 emit-sentinel call), folding the inventory summary into the reason for auditability. The explicit confirmation IS the consent.

## Close paths

| Trigger | Consent gate | Result |
|---------|--------------|--------|
| User types `/exit`, `/quit`, `/bye`, `/close-conversation` | typing IS consent — bypass | close sentinel, reason `delivered`, conversation contract CLOSED |
| User explicitly confirms close (empty inventory) | typed confirmation IS consent — bypass | close sentinel, reason `delivered`, conversation contract CLOSED |
| User explicitly confirms close (non-empty inventory, accepted) | typed confirmation IS consent — bypass | close sentinel `delivered` with inventory note, conversation contract CLOSED |
| Agent infers conversation is winding down | AskUserQuestion gate required | close ONLY on "Yes" selection |
| User names open items | (no close happening) | one open sentinel per item (child contracts), conversation contract stays open |
| Direct `/close-contract <id>` (escape hatch) | AskUserQuestion gate if agent-initiated | closes immediately by id on consent, no inventory prompt |

## Notes

- The conversation contract deliverable is fixed: `user agrees conversation has come to a close and there are no loose ends`.
- Idempotency: if the fold shows no open conversation contract, report success immediately — nothing to close.
- The close reason should include the inventory summary at the moment of closure for auditability.
