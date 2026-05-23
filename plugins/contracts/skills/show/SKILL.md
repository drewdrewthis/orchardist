---
name: show
description: "Pretty-print a contract's markdown mirror. Use when the user types /contracts:show, says 'show contract', 'open contract', 'what's in contract X', 'remind me about contract'. Reads contracts/<id>--*.md."
user-invocable: true
argument-hint: "<contract-id>"
allowed-tools:
  - Bash
  - Read
  - Glob
---

# /contracts:show — Pretty-print a contract

The contracts plugin mirrors every contract to a Markdown file at
`~/.claude/contracts/<id>--<slug>.md` (regenerated on every event).
This skill finds and reads it.

## Argument parsing

- First positional: `contract-id` (required, format
  `C-YYYY-MM-DD-XXXXXXXX`).

## Steps

### 1. Resolve the file

```bash
CONTRACTS_DIR="${CONTRACTS_DIR:-$HOME/.claude/contracts}"
MATCH="$(ls "$CONTRACTS_DIR"/<id>--*.md 2>/dev/null | head -1)"
if [ -z "$MATCH" ]; then
  MATCH="$(ls "$CONTRACTS_DIR"/<id>.md 2>/dev/null | head -1)"
fi
```

If no match, refuse: `no mirror file for <id>`.

### 2. Read

Use the `Read` tool on the matched path.

### 3. Surface

Print as-is. Don't reformat.

If there are many timeline events (>50), print only the latest 30 and
note `… and <N> earlier events (full file: <path>)`.
