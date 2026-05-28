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

4. **Build the consent context.** Classify the trigger and decide whether the consent gate fires:

   | Trigger | Consent gate | Inventory state |
   |---|---|---|
   | User typed `/exit`, `/quit`, `/bye`, or `/close-conversation` as the first non-whitespace token | bypass — typing IS consent | empty OR non-empty (the typing accepts the inventory as-is) |
   | User typed an explicit confirmation in chat ("yes close it", "I accept the inventory", "close delivered") | bypass — explicit confirmation IS consent | empty OR non-empty |
   | Agent inferred the conversation is winding down (paraphrased intent like "we're done here", `/i-am-done` returns done, no user close-action) | **AskUserQuestion gate required** | empty OR non-empty |
   | User named items that are still open | (no close happening) | non-empty after filing |

5. **If close path bypasses the gate** (typed close command or explicit chat confirmation): emit the close sentinel for the conversation contract's id via the shared `scripts/emit-sentinel.sh`, folding the inventory summary into the reason:

   ```bash
   bash "<this-skill-dir>/../../scripts/emit-sentinel.sh" close "<conversation-contract-id>" "delivered: <inventory summary>"
   ```
   Report: "Conversation contract closed as delivered."

6. **If the consent gate fires** (agent-inferred close): present the inventory summary via `AskUserQuestion`. The question text MUST embed `/i-am-done`'s verbatim decision so the user sees what the agent saw. Two options:

   - "Yes — close the conversation contract as delivered"
   - "Keep open — there's more I want to do"

   Only emit the close sentinel (same shape as step 5) on "Yes". On "Keep open" report the inventory and continue. If the user wants to abandon instead, they will say so in chat — that's normal redirection, not a third menu option; emit the close with `reason: "abandoned: <reason>"`.

7. **If the user names items that are still open:** file a child contract for each via `/open-contract` (one open sentinel per named item). Do NOT close the conversation contract — it stays open. Report: "Conversation contract remains open. Filed child contracts for: <items>."

## Notes on the consent gate

The numbered flow above (step 4 table + steps 5–7) is the canonical close-path source. The same discipline applies to `/close-contract` for arbitrary ids — see `close-contract/SKILL.md` § "Authority: the user owns the close" for that skill's gate, which has the same bypass-vs-gate rule scoped to its own trigger vocabulary.

Typing `/close-conversation` invokes THIS skill — it does NOT bypass `/close-contract <id>` for arbitrary ids.

## Notes

- The conversation contract deliverable is fixed: `user agrees conversation has come to a close and there are no loose ends`.
- Idempotency: if the fold shows no open conversation contract, report success immediately — nothing to close.
- The close reason should include the inventory summary at the moment of closure for auditability.
