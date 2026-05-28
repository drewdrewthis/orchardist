---
name: my-contracts
description: List the open contracts for the current session — what you still owe before Stop will let the conversation end. Use when asked "what contracts do I have", "what do I owe", "list my contracts", "/my-contracts", or before stopping to check what's still open. Reads the session jsonl via the shared fold; no MCP, no daemon.
---

# /my-contracts

List the contracts currently OPEN for this session — the deliverables that will block Stop until closed. This is the on-demand read surface: the same fold the Stop hook runs, available any time you want to know "what do I still owe?".

## Flow

1. Run the shared fold script in `--auto` mode — it picks the newest jsonl under this cwd's projects dir (which IS the current session, by definition, because we are running inside it as it writes to itself):

   ```bash
   bash "$CLAUDE_PLUGIN_ROOT/scripts/fold-contracts.sh" --auto
   ```

   It prints one line per open contract: `- <id>: <statement>`. Empty output means no open contracts. `--auto` does not need `$CLAUDE_SESSION_ID` — that env var is unset in SDK/--print contexts, so prefer `--auto` over `--session "$CLAUDE_SESSION_ID"` here.

3. Report the result to the user:
   - Open contracts → list them and note each is closed with `/close-contract <id>`.
   - None → "No open contracts — Stop is clear."

## Notes

- This is read-only: it never writes a sentinel. Opening/closing is `/open-contract` and `/close-contract`.
- It calls the SAME `scripts/fold-contracts.sh` the Stop hook uses, so what it lists is exactly what would block Stop. One fold, no drift.
- `--auto` is the production-correct mode. The earlier `--session "$CLAUDE_SESSION_ID"` recipe was an i-am-done canonical anti-pattern: `CLAUDE_SESSION_ID` is not exported to skill subprocesses in SDK/--print runs, so the fold ran blind. `--auto` resolves the path from `$PWD` and picks the newest jsonl, which is the running session.
