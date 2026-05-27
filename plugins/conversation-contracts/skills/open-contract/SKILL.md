---
name: open-contract
description: Open a contract — a named commitment to a deliverable that blocks Stop until closed. Use when committing to ship/do something specific within this session ("I'll commit to X", "open a contract for Y", "/open-contract"). Writes an orchard_contract open sentinel into the session jsonl; the Stop hook folds it and blocks until you /close-contract it.
---

# /open-contract

Open a contract: a named commitment that the Stop hook will block on until you close it. This is how you bind yourself (or get bound) to a deliverable so the session can't quietly end with the work undone.

## How contracts work (the whole model)

Contracts live as JSON **sentinels** in this session's own jsonl — there is no database, no MCP server, no sidecar file. The jsonl IS the store.

- **Open** → append an `orchard_contract` open sentinel (this skill).
- **Close** → append a matching close sentinel by id (`/close-contract`).
- **List** → fold open-minus-close over the jsonl (`/my-contracts`).
- **Block** → the Stop hook runs the same fold and blocks Stop while any contract is open, naming the verbs you need.

## Flow

1. Get a one-line **statement** of the commitment from the user (or state your own). Keep it free of unescaped double-quotes — it is embedded in a JSON line.

2. Generate an id and emit the open sentinel with a single `Bash` call:

   ```bash
   echo "{\"orchard_contract\":\"open\",\"id\":\"C-$(date -u +%Y-%m-%d)-$(openssl rand -hex 4)\",\"statement\":\"<one-line statement>\",\"ts\":\"$(date -u +%Y-%m-%dT%H:%M:%SZ)\"}"
   ```

   The echoed JSON lands in the jsonl (in the tool_result), where the fold picks it up.

3. Read the generated `id` from the echo output and **report it back** to the user: "Opened contract `<id>`: <statement>. Close it with `/close-contract <id>` when delivered." The user needs the id to close it later.

## Notes

- One contract per distinct commitment. Open as many as you need — they fold uniformly by id.
- The Stop hook will block until every open contract has a matching close. Don't open a contract you don't intend to close this session.

## Putting the definition-of-done in the statement

The sentinel is deliberately two fields only (`id` + `statement`) — no `done`/`template`/`gates` keys. To bind a contract to a clear done-condition, **write it into the `statement` text**. The statement is free-form, so a "code-work" contract can read:

> "ship the X refactor — done when /review is clean, /prove-it is green, SOLID, no known bugs"

That keeps the JSON schema fixed while letting the statement carry whatever acceptance bar you want. Variants are just statement templates the skill can offer; they add no new fields.
