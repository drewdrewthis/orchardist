# ADR-020: Adopt Tailwind v4 for orchard-gui

**Status:** Accepted (2026-05-11)

## Context

The no-handrolling rule (Drew, 2026-05-10) states: "If a tool/library does the
job, use it. Don't hand-roll … Tailwind (when adopted, #544) over hand-rolled
scoped CSS."

The current state: 11 of 28 Svelte files carry scoped `<style>` blocks.
`src/lib/styles/theme.css` (711 lines) and `src/lib/styles/layout.css`
(~1,549 lines) hold ~150 bespoke class names. 47 CSS custom properties
already define a mature design system using `oklch` + `color-mix`, with
dark mode driven by `[data-theme=dark|light]` on `<html>` and a
runtime-mutable `--accent-hue` property.

The win is utility-class ergonomics, JIT-only bundled CSS, and dead-CSS
elimination. This is **not** a new token system — we already have a good one.
The migration must preserve it.

## The decision

Adopt Tailwind v4 (CSS-first via `@theme {}`), installed via `@tailwindcss/vite`,
coexisting with the existing CSS-variable theme during a stop-bleed migration.

### Version pin and init flow

Pin: Tailwind v4 (currently v4.3.0).

Preferred install: `npx sv add tailwindcss`. If `sv add` rewrites
`vite.config.js` in a way that breaks Houdini plugin ordering, fall back to
manual install per the official Tailwind+Vite guide: install `tailwindcss` +
`@tailwindcss/vite`, add `tailwindcss()` to the `plugins` array, and add
`@import "tailwindcss";` to the global stylesheet imported from `+layout.svelte`.
Both paths are idiomatic v4.

No `tailwind.config.js`. Tailwind v4 is CSS-first; a JS config file would
re-create the hand-rolled smell this ADR intends to retire.

### Vite plugin order

```js
plugins: [houdini(), sveltekit(), tailwindcss()]
```

Houdini stays first: its docs explicitly require it to run before SvelteKit so
codegen and the `$houdini` alias resolve before routes are traversed. Tailwind
slots last so its content scan sees post-Houdini-transform output. Update the
existing plugin-order comment in `vite.config.js` to reference this ADR.

### Static-vs-dynamic token split

Static tokens (palette, spacing, radii, shadows) live in `@theme {}`:

```css
@import "tailwindcss";

@theme {
  --color-bg:   oklch(var(--bg-l) var(--bg-c) var(--bg-h));
  --color-fg:   oklch(var(--fg-l) var(--fg-c) var(--fg-h));
  --color-attn: oklch(var(--attn-l) var(--attn-c) var(--attn-h));
  --color-ok:   oklch(var(--ok-l) var(--ok-c) var(--ok-h));
  --color-bad:  oklch(var(--bad-l) var(--bad-c) var(--bad-h));

  --spacing-xs: 0.25rem;
  --spacing-sm: 0.5rem;
  --spacing-md: 1rem;
  --spacing-lg: 1.5rem;

  --radius-sm: 4px;
  --radius-md: 8px;
}
```

`--accent-hue` stays declared on `:root` (mutated at runtime by
`+layout.svelte:$effect`). The six accent-derived tokens live in
`@theme inline {}` so they reference `var(--accent-hue)` and remain reactive:

```css
:root {
  --accent-hue: 220;
}

@theme inline {
  --color-accent:      oklch(55% 0.22 var(--accent-hue));
  --color-accent-soft: oklch(55% 0.22 var(--accent-hue) / 15%);
  --color-accent-line: oklch(55% 0.22 var(--accent-hue) / 40%);
  --color-accent-fg:   oklch(98% 0.01 var(--accent-hue));
}
```

When a component needs a dynamic accent surface and a static utility won't
compose, `bg-[color:var(--accent)]` is the supported fallback.

### Dark-mode variant declaration

```css
@custom-variant dark (&:where([data-theme=dark], [data-theme=dark] *));
```

This preserves the existing `document.documentElement.dataset.theme = "dark"|"light"`
toggle in `+layout.svelte` unchanged. No class-based `class="dark"` toggle.

### Exemplar component

`crates/orchard-gui/src/lib/components/ChatMessage.svelte` is the named exemplar
for this PR. It is small (33 LOC of scoped CSS), stable, has no third-party DOM,
and demonstrates variant patterns (`.is-user` / `.is-agent` / `.is-question`)
that map naturally to Tailwind utilities. Acceptable alternates if blocked:
`PanesArea.svelte` or `SessionPane.svelte`.

Explicitly excluded from this PR:
- `LensSidebar/SidebarItem.svelte` — in flight in #548.
- `TerminalAttach.svelte` — uses `:global(.xterm)` for third-party DOM; this is
  an allowlisted exception (see stop-bleed section below).

### Stop-bleed CI enforcement

A CI step on PRs touching `crates/orchard-gui/` diffs new `.svelte` files under
`crates/orchard-gui/src/lib/components/`. If any new file introduces a `<style>`
block, the step exits non-zero and surfaces the offending filename along with a
pointer to this ADR and the allowlist.

Allowlist file: `crates/orchard-gui/.style-allowlist.txt` (plain text,
one filename per line, `#` comments supported).

Initial allowlist:
```
# Third-party DOM wrappers — :global() is unavoidable here
TerminalAttach.svelte
```

Implementation: grep-on-diff in a GitHub Actions step is acceptable; an
`eslint-plugin-svelte` rule is also acceptable. Whichever lands first wins.

### design-prototype/ scan policy

`design-prototype/` is a Claude-Design handoff bundle; it is not production
and must not be scanned. Exclude it from inside the global stylesheet:

```css
@source not "../../../design-prototype";
```

The path is resolved relative to the global stylesheet's location
(`crates/orchard-gui/src/lib/styles/`, so three `..` reach
`crates/orchard-gui/design-prototype`).

### Completion deadline / revert condition

Migration of the remaining 10 styled Svelte files completes within 5 follow-up
PRs OR 6 weeks from this PR's merge, whichever is sooner. If neither is met,
revert ADR-020 and reopen the trade-off.

## Consequences

- Utility-class ergonomics in markup; JIT-only bundled CSS; a long-term path to
  delete most of `layout.css`.
- A coexistence period where some components are Tailwind and some are scoped CSS.
- A documentation tax for new contributors (static-vs-dynamic token rule).
- A small but non-zero risk that the Houdini + Tailwind + Tauri stack hits an
  unproven edge case (mitigated by the cold-boot smoke test gate in the AC list).

## Related

- [#544](https://github.com/drewdrewthis/git-orchard-rs/issues/544) — this issue
- [#548](https://github.com/drewdrewthis/git-orchard-rs/issues/548) — sidebar rework (concurrent, excluded from this PR)
- [#549](https://github.com/drewdrewthis/git-orchard-rs/issues/549)–[#552](https://github.com/drewdrewthis/git-orchard-rs/issues/552) — downstream beneficiaries
