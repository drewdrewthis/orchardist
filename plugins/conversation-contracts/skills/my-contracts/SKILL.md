---
name: my-contracts
description: List the open contracts for the current session — what you still owe before Stop will let the conversation end. Use when asked "what contracts do I have", "what do I owe", "list my contracts", "/my-contracts", or before stopping to check what's still open. Reads the session jsonl via the shared fold; no MCP, no daemon.
---

# /my-contracts

List the contracts currently OPEN for this session — the deliverables that will block Stop until closed. This is the on-demand read surface: the same fold the Stop hook runs, available any time you want to know "what do I still owe?".

## Flow

1. Resolve this session's jsonl path. It is `<projects_root>/<encoded-cwd>/<session-id>.jsonl`, where `encoded-cwd` is `$PWD` with `/` and `.` both replaced by `-`, and `projects_root` is `$CLAUDE_PROJECTS_DIR` (default `$HOME/.claude/projects`). Use `$CLAUDE_SESSION_ID` for the session id.

2. Run the shared fold script against it with a single `Bash` call:

   ```bash
   FOLD="$CLAUDE_PLUGIN_ROOT/scripts/fold-contracts.sh"
   ROOT="${CLAUDE_PROJECTS_DIR:-$HOME/.claude/projects}"
   ENC=$(printf '%s' "$PWD" | tr '/' '-' | tr '.' '-')
   bash "$FOLD" "$ROOT/$ENC/$CLAUDE_SESSION_ID.jsonl"
   ```

   It prints one line per open contract: `- <id>: <statement>`. Empty output means no open contracts.

3. Report the result to the user:
   - Open contracts → list them and note each is closed with `/close-contract <id>`.
   - None → "No open contracts — Stop is clear."

## Notes

- This is read-only: it never writes a sentinel. Opening/closing is `/open-contract` and `/close-contract`.
- It calls the SAME `scripts/fold-contracts.sh` the Stop hook uses, so what it lists is exactly what would block Stop. One fold, no drift.
- If `$CLAUDE_SESSION_ID` isn't set in your environment, the path can't be resolved — fall back to telling the user the Stop hook will surface open contracts when they try to end the session.
