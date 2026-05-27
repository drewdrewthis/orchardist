---
name: close-contract
description: Close an open contract by id — deliver it (with evidence) or abandon it (with reason). Use when a committed deliverable is done or being dropped ("close contract C-...", "mark X delivered", "/close-contract <id>"). Writes an orchard_contract close sentinel so the Stop hook stops blocking on it.
---

# /close-contract

Close a contract you opened with `/open-contract` (or that was auto-opened for the conversation). Closing is how you unblock Stop: the Stop hook folds open-minus-close, so a contract with a matching close no longer blocks.

A close is one of two things:
- **delivered** — the work is done; cite the evidence in the reason.
- **abandoned** — the work is being dropped; say why (prefix the reason with `abandoned:`).

## Flow

1. Identify the **id** to close. If the user didn't give one, run `/my-contracts` to list open contracts and pick/confirm the id.

2. Compose the **reason**:
   - delivered → `delivered: <one-line evidence>` (a command output, a PR link, a test result — what proves it).
   - abandoned → `abandoned: <why it's being dropped>`.
   Keep it free of unescaped double-quotes — it embeds in a JSON line.

3. Emit the close sentinel with a single `Bash` call:

   ```bash
   echo "{\"orchard_contract\":\"close\",\"id\":\"<id>\",\"reason\":\"<reason>\",\"ts\":\"$(date -u +%Y-%m-%dT%H:%M:%SZ)\"}"
   ```

4. Report: "Closed `<id>` (<delivered|abandoned>): <reason>."

## Notes

- The id must match an open contract's id exactly, or the fold won't cancel it.
- Closing an already-closed or unknown id is harmless (the fold just sees an extra close); prefer `/my-contracts` first if unsure.
- For the conversation-level contract specifically, prefer `/close-conversation` — it runs the loose-ends inventory before closing. `/close-contract` is the direct, generic closer for any contract by id.
