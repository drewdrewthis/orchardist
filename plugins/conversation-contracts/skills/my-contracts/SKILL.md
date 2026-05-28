---
name: my-contracts
description: List the open contracts for the current session — what you still owe before Stop will let the conversation end. Use when asked "what contracts do I have", "what do I owe", "list my contracts", "/my-contracts", or before stopping to check what's still open. Reads the session jsonl via the shared fold; no MCP, no daemon.
---

# /my-contracts

List the contracts currently OPEN for this session — the deliverables that will block Stop until closed. This is the on-demand read surface: the same fold the Stop hook runs, available any time you want to know "what do I still owe?".

## Flow

1. Run the shared fold script with a single `Bash` call — it resolves this session's jsonl from the session id itself:

   ```bash
   bash "$CLAUDE_PLUGIN_ROOT/scripts/fold-contracts.sh" --session "$CLAUDE_SESSION_ID"
   ```

   It prints one line per open contract: `- <id>: <statement>`. Empty output means no open contracts. (`--session <id> [<cwd>]` resolves `<projects_root>/<encoded-cwd>/<id>.jsonl`; pass an explicit jsonl path instead if you have one.)

3. Report the result to the user:
   - Open contracts → list them and note each is closed with `/close-contract <id>`.
   - None → "No open contracts — Stop is clear."

## Notes

- This is read-only: it never writes a sentinel. Opening/closing is `/open-contract` and `/close-contract`.
- It calls the SAME `scripts/fold-contracts.sh` the Stop hook uses, so what it lists is exactly what would block Stop. One fold, no drift.
- If `$CLAUDE_SESSION_ID` isn't set in your environment, the path can't be resolved — fall back to telling the user the Stop hook will surface open contracts when they try to end the session.
