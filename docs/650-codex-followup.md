# Codex Follow-up Checklist — Issue #650 L1.16 + L2.14

These changes belong in the codex repo (`~/.claude/`) and cannot be
applied from this worktree. Apply them as a separate commit to
`drewdrewthis/orchard-codex` (main branch, no PR ceremony).

---

## 1. Delete `~/.claude/CLAUDE.md` lines 117-124 (update_contract comment workflow)

The "update_contract comment workflow" block is dead code: update_contract
no longer exists in v0.8 (the schema collapsed to open_contract /
close_contract). Remove the block from the Interrupt Discipline section.

**Target**: `~/.claude/CLAUDE.md` — find and delete the section that
documents `update_contract` usage (was roughly lines 117-124 per the issue
brief; verify line numbers in the live file before applying).

---

## 2. Delete `~/.claude/skills/accept-contract/` directory

`accept-contract` called `update_contract` which no longer exists. The
skill is a dead-code artifact of the old multi-status contract model.

```bash
rm -rf ~/.claude/skills/accept-contract/
```

---

## 3. Update `~/.claude/skills/digest/SKILL.md` — remove dropped Contract fields

The `/digest` skill queries `Query.contracts` via GraphQL. The v0.8 schema
removed the following fields from `Contract`:

- `criteria`
- `openQuestions { questionId text askedBy askedAt deadline blocksClose }`
- `reportsTo`
- `parentContractId`

And added:
- `closedReason`

Update any GraphQL fragment or inline query in the skill to match the v0.8
projection. The canonical projection is now:

```graphql
{
  id
  contractId
  statement
  ownerSessionId
  status
  closedReason
  createdAt
  updatedAt
  lastEventAt
}
```

Status enum values changed from the multi-value set to:
- `SIGNED` — active (was `OPEN`)
- `CLOSED` — ended (was all other terminal statuses)

If the skill filters by status, update the filter values accordingly.

---

## 4. Add "Conversation contracts" section to `~/.claude/CLAUDE.md` under "Interrupt Discipline"

Add ≤ 25 lines under the "Interrupt Discipline — Say → Do → Report" section
documenting:

```markdown
### Conversation contracts

Every Claude session auto-opens one conversation contract on the first user
message (via the UserPromptSubmit hook). The contract id is
`C-YYYY-MM-DD-XXXXXXXX`; the deliverable is fixed: "user agrees conversation
has come to a close and there are no loose ends".

**Loose-ends inventory** (surfaced at Stop when the conversation contract is
still open): lists open child contracts and open TodoWrite items. No regex
heuristic — inventory is derived from the daemon's contracts provider.

**Four close paths** — any of the following closes the conversation contract
with `closedReason: DELIVERED`:
- `/exit`
- `/quit`
- `/bye`
- `/close-conversation`

**Exit = delivered semantics**: typing any close path signals the user
accepted the conversation is complete. Abandoned conversations (session
killed mid-flight) are handled by the daemon's orphan-detection path.

**Resource consequence**: long-running worker or cron sessions that never
receive a close command cannot Stop cleanly until the conversation contract
is closed. Schedule a close or use `close_contract` directly to unblock.
```

---

## Verification checklist (apply after making the changes)

- [ ] `grep -r "update_contract" ~/.claude/` returns no results outside
      this document
- [ ] `~/.claude/skills/accept-contract/` does not exist
- [ ] `/digest` skill queries no removed Contract fields
- [ ] `~/.claude/CLAUDE.md` contains the "Conversation contracts" section
      under "Interrupt Discipline"
- [ ] "Conversation contracts" section is ≤ 25 lines
