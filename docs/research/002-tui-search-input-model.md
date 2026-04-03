# 002: TUI Search Input Model

**Status:** Recommendation ready
**Date:** 2026-03-31
**Question:** How should keyboard input work to support instant-filter search while keeping quick action bindings?

## Problem Frame

The TUI currently uses `/` to activate search mode, which feels ceremonial. The user wants typing to filter immediately ("just work right away"), but ~15 single-key bindings (o, p, q, d, c, etc.) conflict with printable characters.

**Constraints:** Rust + Ratatui, TEA architecture, vim-familiar power user, Esc currently means quit.
**Decision type:** Reversible (UX only, no data model changes), moderate blast radius (every keyboard interaction).

## Prior Art

| Tool | Model | Search activation | Action keys |
|------|-------|-------------------|-------------|
| fzf | Always-filter | Immediate (modeless) | Ctrl/Alt only |
| lazygit | Trigger modal | `/` enters search | Context swap — search replaces all bindings |
| k9s | Trigger modal | `/` or `:` | Dialog-as-focus-steal suppresses actions |
| htop | Soft modal | `F3` or `/` | Any non-search key exits search and fires |
| telescope.nvim | Dual mode | Immediate (Insert mode) | Esc → Normal mode for actions |
| yazi | Layer isolation | Per-context | 8 separate keymap layers |
| nnn | Trigger modal | `/` | Ctrl-N toggles type-to-nav |

## Strategies Evaluated

### A. Soft Modal (htop-style)
Keep `/` activation, but any action key auto-exits search and fires. Minimal change (~10 lines), fully backwards compatible. Doesn't satisfy "just works" — still needs `/`.

### B. Dual Mode (telescope-style)
Default to Filter mode (typing filters). Esc → Command mode (action keys). Delivers instant search. Requires repurposing Esc (currently quit), mode indicator widget, mode confusion risk.

### C. Leader Key Prefix (recommended)
Bare keys filter immediately. Actions require Space prefix: `Space+o` opens PR, `Space+q` quits. No modes, no Esc change. Cost: every action becomes 2 keystrokes.

## Quorum Debate Summary

Three agents argued positions independently:

- **A argued:** Minimal change, zero retraining, search dissolves on action. Conceded it doesn't deliver "instant" search.
- **B argued:** Only option that literally delivers the stated goal. Conceded Esc rekeying is a breaking change and mode indicators are a smell.
- **C argued:** User suggested it, no modes, no confusion, vim leader is familiar. Conceded 2-keystroke actions are measurably slower.

## Recommendation: Leader Key Prefix (C)

**Why:**
1. User independently suggested a prefix — their mental model already works this way
2. No modes means no mode confusion, no indicator widget, no invisible state
3. Esc remains quit — no breaking change to the universal escape hatch
4. Opens unlimited keyspace for future bindings (`Space+[a-zA-Z0-9]`)

**Implementation sketch:**
- New `InputPhase` enum: `Filtering | AwaitingLeader`
- Space key → transition to `AwaitingLeader`
- Next key in `AwaitingLeader` → dispatch action Message, return to `Filtering`
- Printable chars in `Filtering` → append to search, filter immediately
- Enter → switch to selected (clear filter)
- Esc → quit (unchanged)
- j/k, arrows, 1-9, PageUp/Down → navigation (exempt from filter, work directly)
- Backspace → pop last filter char

**Open questions:**
- Should navigation keys (j/k, arrows, 1-9) require leader prefix or stay direct? Recommendation: stay direct — they're not printable-char conflicts (arrows/PgUp are non-printable, 1-9 and j/k need special handling)
- j/k conflict: these are both valid filter chars AND navigation. Options: (a) require leader for nav (`Space+j`), (b) use only arrows for nav, (c) short timeout to disambiguate. Recommend (a) for consistency.
- Space in filter text: use double-space or another mechanism if needed (git branches can't contain spaces, so this may be moot)

## Next Step

Create a GitHub issue and `/plan` the implementation, or present to user for feedback on the j/k question first.
