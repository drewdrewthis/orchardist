# Changelog

All notable changes to the `conversation-contracts` plugin.

Format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).
This plugin uses semantic versioning.

## 0.12.1 — 2026-05-29

### Added
- Conversation contract statement now references `/decide` as the procedure for
  in-flight decisions (escalate vs act, ask vs decide, branch on ambiguity).
  Pairs with v0.12.0's close-bar: when the agent cannot self-close (always),
  it also has a named procedure for how to act when uncertain mid-conversation
  instead of defaulting to ask-the-user.

## 0.12.0 — 2026-05-28

### Changed
- Conversation contract statement now encodes an 8-clause impossibly-high
  agent-self-close bar. Effect: agent-self-close becomes structurally
  impossible because at any honest audit at least one clause is uncertain
  (always a follow-up, always a revision). Only user-typed close paths can
  close the conversation contract: `/exit`, `/quit`, `/bye`,
  `/close-conversation`, or literal `/close-contract <id>`.

### Why
v0.11.x added the AskUserQuestion consent gate, but enforcement still
depended on the agent applying the discipline by memory. v0.12.0 makes it
structural — the statement TEXT documents the bar; the close-contract
SKILL.md's AskUserQuestion gate ENFORCES it.

## 0.11.2 — 2026-05-28

### Changed
- `hooks/on-stop.sh` is DUMB: procedural commentary about close mechanics
  stripped. Hook emits only the `decision:block` + open-contracts list. The
  statement (loaded by SessionStart) IS the discipline — the hook is pure
  mechanism.

### Fixed
- `scripts/collapse-statement.sh` normalizes tabs (via `tr`) and CRLF (via
  `sed 's/\r$//'`) — statement file robust to Windows-edited or tab-
  containing input. Bats coverage added.
- `hooks/on-session-start.bats` symlink test honestly downgraded to a SMOKE
  check with the rationale: file reads through symlinks are kernel-
  transparent, so the `cd -P` defense isn't load-bearing in any current code
  path; the test verifies symlinked installs still emit a sentinel, not that
  `cd -P` regression-locks anything.

## 0.11.1 — 2026-05-28

### Fixed
- `/close-contract` consent-gate bypass requires the LITERAL slash command as
  the first non-whitespace token of a user message. Paraphrased intents
  ("yes go ahead", "close it", "we're done here") are NOT bypass triggers —
  they look like consent but the user did not commit a specific close
  action. Route them through the consent gate.
- AskUserQuestion text now embeds `/i-am-done`'s verbatim decision (`done`
  / `partial` / `not-done`) so the user sees what the agent saw before
  picking.

## 0.11.0 — 2026-05-28

### Added
- AskUserQuestion consent gate on agent-initiated `/close-contract` and
  `/close-conversation`. When the agent invokes the skill, the close
  sentinel is gated behind a Yes/Keep-open AskUserQuestion. User-typed close
  paths (`/exit`, `/quit`, `/bye`, `/close-conversation`) bypass the gate
  since the typing IS the consent.

### Why
Closing requires the user's specific consent, not just the agent's
evidence. `/i-am-done` provides evidence (necessary); user provides
authority via AskUserQuestion (sufficient). Hardened the discipline that
prior versions left to memory.

## 0.10.3 — 2026-05-28

### Added
- Leading-whitespace strip in `collapse-statement.sh` (a statement file
  starting with a blank line previously produced a leading-space sentinel
  that the single-line-prose shape lock didn't catch).
- Symlinked-path BASH_SOURCE fallback test in `on-session-start.bats`
  (subsequently downgraded in v0.11.2 — see notes there).

### Fixed
- Stale comments referencing pre-v0.10 inline `tr | sed` normalization.

## 0.10.2 — 2026-05-28

### Added
- Shared `scripts/collapse-statement.sh` helper — single source of truth
  for statement-text normalization, shared by the SessionStart hook and
  the bats tests. Prevents co-drift if the normalization rules ever change.
- `cd -P` / `pwd -P` in the SessionStart hook's BASH_SOURCE fallback
  (defensive — see v0.11.2 honest-downgrade for the load-bearing analysis).

## 0.10.0 — 2026-05-28

### Added
- Conversation contract statement file at
  `references/conversation-contract-statement.md`. The SessionStart hook
  reads this file and emits its content as the auto-opened conversation
  contract's statement. This makes the contract the **discipline gateway**:
  it points the agent at `/i-am-done` as the universal close gate.
- Fallback to the minimal deliverable string when the statement file is
  missing — keeps the hook robust against deployment-time absence.

### Why
Prior to v0.10.0 the auto-opened conversation contract had a hard-coded
minimal statement. v0.10.0 lets the discipline gateway evolve in a single
text file without touching the hook.

## (v0.10.1 superseded by v0.10.2 — no separate entry.)

## 0.9.2 — 2026-05-28

### Changed
- SKILL.md recipes for `/open-contract` and `/close-contract` now use
  `<this-skill-dir>/../../scripts/emit-sentinel.sh` instead of
  `$CLAUDE_PLUGIN_ROOT/scripts/emit-sentinel.sh`. The latter is empty in
  skill subprocesses; the relative path is resolved against the skill's
  Base directory (injected by the Claude Code harness on skill load).

### Added
- `recipe-verbatim.bats` — bats tests that extract the bash code block from
  each SKILL.md recipe and execute it literally under `env -i`. Catches
  the class of bug where the script changes but the recipe doesn't (and
  vice versa).

## 0.9.1 — 2026-05-28

### Changed
- Version bump only. v0.9.0 had two underlying bugs the v0.9.1-onward
  releases iteratively closed.

## 0.9.0 — pre-arc

Initial conversation-contracts plugin release. `/open-contract`,
`/close-contract`, `/my-contracts`, `/close-conversation` skills.
SessionStart auto-opens a fixed-statement conversation contract. Stop hook
folds open-minus-close and blocks Stop while any contract is open.

Known issues addressed by v0.9.1+:
- SKILL.md recipes used `$CLAUDE_PLUGIN_ROOT` which is empty in skill
  subprocesses (fixed in v0.9.2 via `<this-skill-dir>/../../scripts/`).
- Recipe layer had no real-runtime drive (fixed in PR #674 via
  `recipe-verbatim.bats`).
