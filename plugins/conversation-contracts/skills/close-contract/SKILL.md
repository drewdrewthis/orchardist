---
name: close-contract
description: Close an open contract by id — deliver it (with evidence) or abandon it (with reason). When the agent initiates the close (not a user-typed close path), gate the close sentinel behind an AskUserQuestion confirmation — the user owns the close decision. Use when a committed deliverable is done or being dropped ("close contract C-...", "mark X delivered", "/close-contract <id>"). Writes an orchard_contract close sentinel so the Stop hook stops blocking on it.
---

# /close-contract

Close a contract you opened with `/open-contract` (or that was auto-opened for the conversation). Closing is how you unblock Stop: the Stop hook folds open-minus-close, so a contract with a matching close no longer blocks.

A close is one of two things:
- **delivered** — the work is done; cite the evidence in the reason.
- **abandoned** — the work is being dropped; say why (prefix the reason with `abandoned:`).

## Authority: the user owns the close

The agent produces evidence (via `/i-am-done`) but does NOT close on its own authority. Every agent-initiated close must be gated behind explicit user consent — the user is the closer; the agent is the proposer.

The literal user-typed close commands bypass the consent gate because the typing IS the consent. **Bypass requires the literal slash-command as the first non-whitespace token of a user message.** Paraphrased intents ("yes go ahead", "close it", "we're done here", "ship it") are NOT bypass triggers — they look like consent but the user did not commit a specific close action. Route them through the consent gate.

Specifically:
- `/close-contract <id>` (literal, first token) → bypass for that id.
- `/exit`, `/quit`, `/bye` (literal, first token) → bypass for the conversation contract (closes it as part of session exit). Does NOT bypass closes on other ids — those still gate.
- `/close-conversation` (literal, first token) → triggers the `/close-conversation` skill, which has its own consent flow. It is NOT a bypass for `/close-contract <id>` on arbitrary ids.

## Flow

1. Identify the **id** to close. If the user didn't give one, run `/my-contracts` to list open contracts and pick/confirm the id.

2. Run `/i-am-done` to produce the evidence block for the proposed close. The skill produces quoted evidence per claim and a decision (done / partial / not-done). Only `done` justifies proposing a delivered close — `partial` or `not-done` means either complete the missing gate or propose `abandoned` with reason.

3. Compose the **reason**:
   - delivered → `delivered: <one-line evidence>` (a command output, a PR link, a test result — what proves it).
   - abandoned → `abandoned: <why it's being dropped>`.

4. **If you are the agent and the user did NOT type a literal close command** — gate the close sentinel behind an `AskUserQuestion` confirmation. The question text MUST embed `/i-am-done`'s verbatim decision (`done` / `partial` / `not-done`) so the user sees what the agent saw. Two options:

   - "Yes — close `<id>` as `<delivered|abandoned>` with reason: `<reason>`"
   - "Keep open — there's more to do"

   If the user wants to redirect (edit the reason, abandon instead of deliver, etc.), they will say so in chat — that's normal user-driven redirection, not a third menu option. Only emit the close sentinel on the "Yes" selection.

   Example question text shape:
   > "Close `C-XXX` as **delivered** (reason: `<reason>`)? /i-am-done returned: **done** with evidence [quoted]. Options below."

   If `/i-am-done` returned `partial` or `not-done`, do NOT propose a `delivered` close — either complete the missing gate first or propose `abandoned` with the gap as the reason.

5. **If the user typed a literal close command** as the first non-whitespace token of their message — skip the AskUserQuestion. The typing IS the consent. See the "Authority" section above for which literals bypass and which don't.

6. Emit the close sentinel with a single `Bash` call to the shared `scripts/emit-sentinel.sh` — the same script `/open-contract` and the SessionStart auto-open hook use, so the on-disk shape stays consistent.

   The script lives at `<this-skill-dir>/../../scripts/emit-sentinel.sh`. The "Base directory" line at the top of this SKILL.md gives you the absolute skill directory; substitute that literal path for `<this-skill-dir>` when you construct the Bash call. Do not use `$CLAUDE_PLUGIN_ROOT` — it's not set in skill subprocesses.

   ```bash
   bash "<this-skill-dir>/../../scripts/emit-sentinel.sh" close "<id>" "<reason>"
   ```

   The script JSON-escapes the reason, so double-quotes and backslashes in your evidence text are safe — you do not need to escape them by hand.

7. Report: "Closed `<id>` (<delivered|abandoned>): <reason>."

## Notes

- The id must match an open contract's id exactly, or the fold won't cancel it.
- Closing an already-closed or unknown id is harmless (the fold just sees an extra close); prefer `/my-contracts` first if unsure.
- For the conversation-level contract specifically, prefer `/close-conversation` — it runs the loose-ends inventory before closing. `/close-contract` is the direct, generic closer for any contract by id.
