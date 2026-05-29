# Changelog

All notable changes to the `conversation-contracts` plugin.

Format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).
This plugin uses semantic versioning.

## 0.12.2 — 2026-05-29

### Fixed
- `hooks/on-session-start.sh` skips emit (exit 0, empty stdout) when
  `CLAUDE_CODE_ENTRYPOINT=sdk-cli`. Real Claude Code 2.1.x exports that value
  for `claude --print` and SDK invocations — ephemeral one-shot sessions with
  no interactive user to "agree the conversation has come to a close." Before
  this fix the hook auto-opened the conversation contract, the agent's final
  text became close-confirmation prose ("Closed C-XXX delivered: ..."), and
  the caller (cron audits, drew-sim probes, programmatic SDK calls) got that
  string instead of the requested task output. Closes parked-question
  2026-05-28 "disable SessionStart auto-open for ephemeral claude -p?".
- `hooks/on-stop.sh` honors Claude Code's `stop_hook_active` re-entry
  protocol. Claude Code's `runQuery` loop tracks `stopHookBlockingCount`;
  after `CLAUDE_CODE_STOP_HOOK_BLOCK_CAP` (default 8) consecutive blocks the
  harness force-overrides Stop with a fixed message ("A hook blocked the
  turn from ending 9 consecutive times — overriding and ending turn") that
  drowns the open-contracts ledger. The hook now surfaces the ledger ONCE
  per generation, then steps aside on re-entry — every new user message
  resets the harness counter, so the next first-Stop blocks again. The
  discipline is preserved across generations; the override-noise is not.

### Why this isn't "less DUMB about content"
v0.11.2 made the hook DUMB about CONTENT — no procedural commentary about
close mechanics, statement-as-discipline. v0.12.2 keeps that. The re-entry
fix is about MECHANISM (correct hook contract with the harness), not
policy. The block reason is still the verbatim open-contracts list.

### Why a denylist, not an allowlist on `cli`
The discriminator is intentionally narrow: skip on known ephemeral values
(`sdk-cli`), default-emit on everything else. If Anthropic adds a new
interactive entrypoint (`cli-tui`, `vscode-extension`, `ide`, …), the hook
keeps auto-opening rather than silently falling out of the discipline
gateway. The denylist is widened by design, never by accident.

### Added
- `hooks/on-session-start.bats` — three regression cases pinning the
  three-way behavior: skip on `sdk-cli`, emit on `cli`, emit on an unknown
  future entrypoint value. The unknown-value case is the design-intent
  pin — if a future contributor flips the discriminator to an allowlist, the
  bats suite catches it.

### Verified
- Direct hook smoke: empty stdout under `CLAUDE_CODE_ENTRYPOINT=sdk-cli`,
  full sentinel under `cli`, full sentinel when unset.
- Live recipe-verbatim drive under real `claude --print`: `/open-contract`,
  `/close-contract`, `/my-contracts`, `/close-conversation` step-1 fold all
  executed verbatim against current SKILL.md templates. The drive sessions
  themselves were ephemeral (`entrypoint:sdk-cli`); confirming the fix
  doesn't break the recipes that DO get invoked by the agent in `--print`
  on demand (vs. the SessionStart hook auto-open, which is what gets
  skipped).

## 0.12.1 — 2026-05-29

### Added
- Conversation contract statement now references `/decide` as the procedure for
  in-flight decisions (escalate vs act, ask vs decide, branch on ambiguity).
  Pairs with v0.12.0's close-bar: when the agent cannot self-close (always),
  it also has a named procedure for how to act when uncertain mid-conversation
  instead of defaulting to ask-the-user.

## 0.12.0 — 2026-05-29

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

## 0.11.2 — 2026-05-29

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

## 0.10.1 — 2026-05-28

### Fixed
- `on-session-start.bats`: restored strict round-trip assertion
  (`[[ "$fold_out" == *"$DELIVERABLE"* ]]`). The earlier relaxation cited
  "fold truncates long statements" — `fold-contracts.sh` does NOT
  truncate; the relaxation was based on a false premise. Test now binds
  the full v0.10.0 statement through the open→record→fold pipeline.
- `on-session-start.sh`: hardened the fallback path. `$CLAUDE_PLUGIN_ROOT`
  was unguarded under `set -u`, so an unset env var aborted before any
  fallback could fire. Now prefers `$CLAUDE_PLUGIN_ROOT` (what real
  Claude Code exports) and falls back to `$(BASH_SOURCE[0])/..` so the
  hook still emits a well-formed sentinel without the env var set.

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

## (test) PR #674 — 2026-05-28 (between v0.9.2 and v0.10.0)

### Added
- `recipe-verbatim.bats` — bats tests that extract the bash code block from
  each SKILL.md recipe and execute it literally under `env -i`. Catches
  the class of bug where the script changes but the recipe doesn't (and
  vice versa). No version bump — pure test addition.

## 0.9.2 — 2026-05-28

### Changed
- SKILL.md recipes for `/open-contract` and `/close-contract` now use
  `<this-skill-dir>/../../scripts/emit-sentinel.sh` instead of
  `$CLAUDE_PLUGIN_ROOT/scripts/emit-sentinel.sh`. The latter is empty in
  skill subprocesses; the relative path is resolved against the skill's
  Base directory (injected by the Claude Code harness on skill load).

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
