---
name: my-contracts
description: List the open contracts for the current session — what you still owe before Stop will let the conversation end. Use when asked "what contracts do I have", "what do I owe", "list my contracts", "/my-contracts", or before stopping to check what's still open. Reads the session jsonl via the shared fold; no MCP, no daemon.
---

# /my-contracts

List the contracts currently OPEN for this session — the deliverables that will block Stop until closed. This is the on-demand read surface: the same fold the Stop hook runs, available any time you want to know "what do I still owe?".

## Flow

1. Run the shared fold script. Prefer `--session "$CLAUDE_SESSION_ID"` when the env var is set (the most-robust path — scans all projects subdirs for the matching jsonl, so a post-startup `cd` doesn't break it). Fall back to `--auto` (newest jsonl under the cwd's encoded projects dir) when it isn't:

   ```bash
   PR="${CLAUDE_PLUGIN_ROOT:-$(find ~/.claude/plugins/cache -path '*/conversation-contracts/*/scripts/fold-contracts.sh' -print -quit 2>/dev/null | sed 's|/scripts/fold-contracts.sh$||')}" \
     && if [ -n "${CLAUDE_SESSION_ID:-}" ]; then \
          bash "$PR/scripts/fold-contracts.sh" --session "$CLAUDE_SESSION_ID"; \
        else \
          bash "$PR/scripts/fold-contracts.sh" --auto; \
        fi
   ```

   It prints one line per open contract: `- <id>: <statement>`. Empty output means no open contracts. The first line is a `$CLAUDE_PLUGIN_ROOT` fallback — the harness sets it when invoking hooks, but interactive `Bash` tool calls in skill subprocesses often don't have it.

3. Report the result to the user:
   - Open contracts → list them and note each is closed with `/close-contract <id>`.
   - None → "No open contracts — Stop is clear."

## Notes

- This is read-only: it never writes a sentinel. Opening/closing is `/open-contract` and `/close-contract`.
- It calls the SAME `scripts/fold-contracts.sh` the Stop hook uses, so what it lists is exactly what would block Stop. One fold, no drift.
- `--session` is preferred when `$CLAUDE_SESSION_ID` is set (interactive Claude Code typically exports it). It scans every projects subdir for the matching jsonl by session id, so the session's startup cwd vs. current `$PWD` doesn't matter.
- `--auto` is the fallback when `$CLAUDE_SESSION_ID` is unset (SDK/--print contexts). It encodes `$PWD` and picks the newest jsonl there — correct as long as the session was started in the current cwd, which is the common case for `--print`.
