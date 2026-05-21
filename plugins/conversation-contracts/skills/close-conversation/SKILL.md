# /close-conversation

Close the open conversation contract for the current session.

## Flow

1. Call `Query.contracts(filter:{ownerSessionId:$CLAUDE_SESSION_ID, statuses:[SIGNED]})` to list all open (SIGNED) contracts.

2. Identify the conversation contract (statement == "user agrees conversation has come to a close and there are no loose ends").

3. Collect the inventory of open items:
   - Open child contracts: all open contracts that are NOT the conversation contract.
   - Open TodoWrite items: scan the session JSONL for pending TodoWrite entries.

4. If the inventory is empty:
   - Call `close_contract(id: <conversation-contract-id>, reason: "delivered")` with a close-note summarising the session.
   - Report: "Conversation contract closed as delivered."

5. If the user names items that are still open:
   - For each named item, call `open_contract(deliverable: <item>)` to create a child contract.
   - Do NOT close the conversation contract.
   - Report: "Conversation contract remains open. Filed child contracts for: <items>."

6. If the user confirms everything is resolved:
   - Call `close_contract(id: <conversation-contract-id>, reason: "delivered")` with the inventory summary as the close-note.
   - Report: "Conversation contract closed as delivered."

## Close paths

| Trigger | Result |
|---------|--------|
| User confirms (empty inventory) | `close_contract` delivered, conversation contract CLOSED |
| User confirms (non-empty inventory) | `close_contract` delivered with inventory note, conversation contract CLOSED |
| User names open items | `open_contract` per item, conversation contract stays open |
| `/exit`, `/quit`, `/bye` | Fold auto-synthesises `close_contract` delivered (no write needed) |
| Direct `close_contract` MCP call | Contract closes immediately, no inventory prompt |

## Notes

- The conversation contract deliverable is fixed: "user agrees conversation has come to a close and there are no loose ends".
- Idempotency: if no open conversation contract exists for the session, report success immediately.
- The close-note should include the inventory summary at the moment of closure for auditability.
