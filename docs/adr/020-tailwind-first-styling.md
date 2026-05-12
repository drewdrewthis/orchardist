# ADR-020: Tailwind-First Styling

## Status
Accepted. Migration tracked in #544.

## Decision
orchard-gui uses Tailwind for styling. No hand-rolled scoped CSS for new components. Existing scoped CSS migrates to Tailwind opportunistically when a component is touched.

## Why
- Scoped CSS in Svelte components hides shared design tokens; theme drift across components is invisible.
- Margin math, manual flex calculations, and bespoke color variables are tool-rejection: Tailwind already provides these.
- Per-component CSS makes it harder to enforce visual consistency at review time.

## How
- New components: Tailwind classes only.
- Touched components: opportunistic migration of the touched section.
- Design tokens: Tailwind config, not per-component variables.
- Conditional styling: Tailwind variants (`hover:`, `dark:`, etc.) over JS-driven class swaps.

## Anti-patterns
- Adding `<style>` blocks for new components.
- Using `style="..."` attributes for layout/spacing.
- Margin math (`margin-left: calc(...)`) where flex/grid would solve it.
- Bespoke color variables shadowing Tailwind theme tokens.

## Consequences
- Tailwind config becomes the design system.
- Visual review at PR time is easier (class names tell the story).
- Existing scoped CSS migrates incrementally; no big-bang refactor.
