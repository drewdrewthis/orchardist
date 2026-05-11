Feature: Adopt Tailwind CSS for the orchard-gui (no-handrolling rule)
  As an orchard-gui contributor
  I want Tailwind v4 wired in alongside our CSS-variable token system with a named exemplar component and a mechanical stop-bleed guard
  So that future GUI work uses utilities instead of hand-rolled scoped CSS, the dynamic accent stays runtime-mutable, and the migration backlog cannot silently grow

  # Issue: #544
  #
  # Notes on scope:
  #   - This is the *foundation* PR. Full migration of the other 10 styled
  #     Svelte files is explicitly out of scope and tracked via follow-up
  #     issues blocked on this one.
  #   - The behavioral contract below is anchored in artifacts (files,
  #     plugin order, lint output, PR description sections) because that
  #     is what a reviewer can verify. There is no user-visible UX change.
  #   - The dark-variant declaration is wired through `[data-theme=dark]`
  #     so the existing `document.documentElement.dataset.theme = "..."`
  #     toggle in `+layout.svelte` continues to work untouched.

  Background:
    Given the orchard-gui crate at crates/orchard-gui/
    And the existing CSS-variable token system in src/lib/styles/theme.css
    And the existing theme toggle that sets document.documentElement.dataset.theme

  # ===================================================================
  # AC 1 — ADR-020 committed
  # ===================================================================

  @integration
  Scenario: ADR-020 exists and documents the load-bearing decisions
    Given the PR branch
    When the reviewer opens docs/adr/020-tailwind-css-adoption.md
    Then the ADR is present in the diff
    And it pins a Tailwind v4 major version
    And it states the chosen init flow (sv add vs manual install) with rationale
    And it documents the Vite plugin order including houdini and sveltekit
    And it documents how the static palette splits between @theme {} and @theme inline {}
    And it specifies that --accent-hue stays in :root and is bridged via @theme inline
    And it declares the dark variant against [data-theme=dark] (not class="dark")
    And it names the lint enforcement scheme used by the stop-bleed CI guard
    And it states a completion deadline or revert condition for the coexistence window

  # ===================================================================
  # AC 2 — Tailwind v4 installed with explicit, documented plugin order
  # ===================================================================

  @integration
  Scenario: Tailwind v4 and the Vite plugin are listed as dependencies
    Given crates/orchard-gui/package.json
    When the dependencies are inspected
    Then tailwindcss is present at a v4 major version
    And @tailwindcss/vite is present at a compatible v4 major version
    And no tailwindcss-related JS config plugin (e.g. autoprefixer-shaped wrappers) is added that contradicts the v4 CSS-first idiom

  @integration
  Scenario: Vite plugin order is explicit in vite.config.js
    Given crates/orchard-gui/vite.config.js
    When the plugins array is read
    Then @tailwindcss/vite appears in the plugins array
    And the order relative to houdini() and sveltekit() matches what ADR-020 prescribes
    And a comment in the file points to ADR-020 for rationale

  # ===================================================================
  # AC 3 — CSS-first configuration, no tailwind.config.js
  # ===================================================================

  @integration
  Scenario: No tailwind.config.js is introduced
    Given the orchard-gui crate after migration
    When the directory is searched for tailwind.config.js, tailwind.config.ts, tailwind.config.mjs, or tailwind.config.cjs
    Then no such file exists
    # Constraint: v4 idiom is CSS-first; a JS config file would re-create the smell

  @integration
  Scenario: Tailwind is imported via a global stylesheet loaded from +layout.svelte
    Given the migrated codebase
    When src/routes/+layout.svelte is opened
    Then it imports a global stylesheet that contains @import "tailwindcss";
    And that import is the entry point Tailwind scans from

  # ===================================================================
  # AC 4 — Tokens defined in @theme {} and @theme inline {}, with dark variant on [data-theme=dark]
  # ===================================================================

  @integration
  Scenario: Static palette and scale tokens live in @theme {}
    Given the global Tailwind stylesheet
    When the @theme {} block is read
    Then it defines the static color palette (e.g. bg, fg, attn, ok, bad)
    And it defines spacing, radii, and shadow tokens that correspond to the existing custom properties
    And no static palette duplication remains in src/lib/styles/theme.css for tokens that moved

  @integration
  Scenario: Dynamic accent-hue derivations live in @theme inline {} referencing :root
    Given the global Tailwind stylesheet
    When the @theme inline {} block is read
    Then --accent-hue remains declared on :root (not inside @theme {})
    And accent-derived tokens (e.g. --color-accent, --color-accent-soft) are declared inside @theme inline {}
    And they reference var(--accent-hue) so they stay reactive to runtime mutation

  @integration
  Scenario: Dark variant uses [data-theme=dark] custom variant
    Given the global Tailwind stylesheet
    When the @custom-variant declaration is read
    Then it declares `dark` as `(&:where([data-theme=dark], [data-theme=dark] *))`
    And no Tailwind config switches to class-based dark mode (class="dark")
    And the existing theme toggle in +layout.svelte is unchanged

  @integration
  Scenario: Theme toggle still flips dark and light at runtime
    Given the GUI is running
    When document.documentElement.dataset.theme is set to "dark"
    Then dark-variant utilities and dark-mode token overrides apply
    When document.documentElement.dataset.theme is set to "light"
    Then light-mode tokens apply
    # Constraint: zero changes to the toggle code path

  @integration
  Scenario: Runtime accent-hue mutation is preserved
    Given the GUI is running
    When document.documentElement.style.setProperty("--accent-hue", "42") is called
    Then accent-tinted surfaces (including any using utility classes like bg-[color:var(--accent)]) reflect the new hue without a reload
    # Constraint: protects the dynamic capability that #549 depends on

  # ===================================================================
  # AC 5 — ChatMessage.svelte exemplar fully migrated
  # ===================================================================

  @integration
  Scenario: ChatMessage.svelte has no scoped <style> block
    Given src/lib/components/ChatMessage.svelte on the PR branch
    When the file is inspected
    Then it does not contain a <style> block
    And its markup uses Tailwind utility classes for layout, color, spacing, and typography

  @integration
  Scenario: ChatMessage demonstrates variant patterns via Tailwind
    Given ChatMessage.svelte after migration
    When the user-, agent-, and question-variant rendering paths are exercised
    Then each variant renders with its prior visual treatment
    And the variant selection is driven by Tailwind utility classes (no scoped class rewrites)

  @integration
  Scenario: ChatMessage demonstrates the dynamic --accent-hue pattern
    Given ChatMessage.svelte after migration
    When an accent-tinted surface is rendered
    Then the surface consumes the accent via a utility shape that references var(--accent-hue) or var(--accent) (e.g. bg-[color:var(--accent)])
    And mutating --accent-hue at runtime updates the surface in place

  @integration
  Scenario: ChatMessage renders identically in light and dark mode
    Given ChatMessage.svelte after migration
    When the component is rendered in both [data-theme=light] and [data-theme=dark]
    Then the visual output matches the pre-migration baseline within agreed tolerances

  # ===================================================================
  # AC 6 — Stop-bleed enforced by CI, with named allowlist
  # ===================================================================

  @integration
  Scenario: CI fails when a new component introduces a <style> block
    Given a synthetic commit that adds a <style> block to a new .svelte file under crates/orchard-gui/src/lib/components/
    When the CI stop-bleed step runs against that diff
    Then the step exits non-zero
    And the failure message names the offending file
    And the failure message points to the allowlist file and ADR-020

  @integration
  Scenario: CI passes when no new <style> blocks are introduced
    Given a commit that touches components but adds no <style> block
    When the CI stop-bleed step runs against that diff
    Then the step exits zero

  @integration
  Scenario: Allowlist file exists and names legitimate exceptions
    Given the PR branch
    When crates/orchard-gui/.style-allowlist.txt (or the ADR-named equivalent path) is read
    Then it lists TerminalAttach.svelte (for the :global(.xterm) wrapper)
    And it lists any other audit-identified exceptions
    And entries in the allowlist do not fail the CI stop-bleed step when their <style> blocks change

  @integration
  Scenario: CI stop-bleed step is wired into the build, not optional
    Given .github/workflows/ (or the equivalent CI definition)
    When the workflow files are read
    Then the stop-bleed step is invoked on PRs touching crates/orchard-gui/
    And the step's failure blocks PR merge

  # ===================================================================
  # AC 7 — Cold-boot smoke test in the PR description
  # ===================================================================

  @e2e
  Scenario: Cold-boot transcript is included in the PR description
    Given the PR for this issue
    When the PR description is read
    Then it includes a transcript (or linked log) of `pnpm install && pnpm tauri dev` (or equivalent) from a clean clone
    And the transcript shows Houdini codegen initializing without error
    And the transcript shows SvelteKit initializing without error
    And the transcript shows Tailwind initializing without error
    # Constraint: this is what catches the Houdini + Tailwind v4 + Tauri plugin-order footgun

  # ===================================================================
  # AC 8 — Build-perf baseline numbers in the PR description
  # ===================================================================

  @e2e
  Scenario: PR description includes before/after build-perf numbers
    Given the PR for this issue
    When the PR description is read
    Then it reports cold `vite build` timing on main and on the PR branch
    And it reports `pnpm check` timing on main and on the PR branch
    And the numbers are concrete (seconds, not adjectives)
    And the description states whether the deltas are within the perf budget set by ADR-020

  # ===================================================================
  # AC 9 — Follow-up issues filed for each remaining styled Svelte file
  # ===================================================================

  @integration
  Scenario: Follow-up issues exist for each remaining styled Svelte component
    Given the audit list of styled Svelte files (the 11 styled components minus the exemplar)
    When the issue tracker is queried
    Then each remaining styled file has a corresponding migration issue
    And each follow-up issue is marked blocked-by #544
    And each follow-up issue references ADR-020 as the migration recipe
    # Acceptable equivalents: a single tracking issue with explicit per-file
    # sub-issues, as long as the blocked-by and ADR references are present.

  # ===================================================================
  # Coexistence invariants (challenge findings baked in)
  # ===================================================================

  @integration
  Scenario: Non-migrated components still render against the existing tokens
    Given a component that has not yet been migrated to Tailwind
    When the GUI is rendered
    Then the component still consumes its existing scoped <style> rules
    And it does not visually regress relative to main
    # Constraint: coexistence during migration is the explicit policy

  @integration
  Scenario: design-prototype/ is not scanned by Tailwind unless ADR-020 says so
    Given the Tailwind content-scan configuration (the @source directives or default scan)
    When the scan paths are read
    Then design-prototype/ is either explicitly included per ADR-020 or explicitly excluded per ADR-020
    And the choice is not implicit

  # ===================================================================
  # AC Coverage Map
  # ===================================================================
  # AC 1 (ADR-020 committed covering version pin, init flow, plugin order, token split, dark variant, lint scheme, completion deadline)
  #   -> Scenario: ADR-020 exists and documents the load-bearing decisions
  #
  # AC 2 (Tailwind v4 + @tailwindcss/vite installed; plugin order explicit and documented)
  #   -> Scenario: Tailwind v4 and the Vite plugin are listed as dependencies
  #   -> Scenario: Vite plugin order is explicit in vite.config.js
  #
  # AC 3 (CSS-first @theme {} only; no tailwind.config.js; @import "tailwindcss"; in global stylesheet imported from +layout.svelte)
  #   -> Scenario: No tailwind.config.js is introduced
  #   -> Scenario: Tailwind is imported via a global stylesheet loaded from +layout.svelte
  #
  # AC 4 (Tokens in @theme {} + @theme inline {} for dynamic refs; dark via @custom-variant on [data-theme=dark]; existing toggle unchanged)
  #   -> Scenario: Static palette and scale tokens live in @theme {}
  #   -> Scenario: Dynamic accent-hue derivations live in @theme inline {} referencing :root
  #   -> Scenario: Dark variant uses [data-theme=dark] custom variant
  #   -> Scenario: Theme toggle still flips dark and light at runtime
  #   -> Scenario: Runtime accent-hue mutation is preserved
  #
  # AC 5 (ChatMessage.svelte exemplar fully migrated — no <style>, demonstrates utilities, dark variant, dynamic --accent-hue pattern)
  #   -> Scenario: ChatMessage.svelte has no scoped <style> block
  #   -> Scenario: ChatMessage demonstrates variant patterns via Tailwind
  #   -> Scenario: ChatMessage demonstrates the dynamic --accent-hue pattern
  #   -> Scenario: ChatMessage renders identically in light and dark mode
  #
  # AC 6 (Stop-bleed enforced by CI; named allowlist file)
  #   -> Scenario: CI fails when a new component introduces a <style> block
  #   -> Scenario: CI passes when no new <style> blocks are introduced
  #   -> Scenario: Allowlist file exists and names legitimate exceptions
  #   -> Scenario: CI stop-bleed step is wired into the build, not optional
  #
  # AC 7 (Cold-boot smoke test transcript in PR description showing Houdini + SvelteKit + Tailwind init)
  #   -> Scenario: Cold-boot transcript is included in the PR description
  #
  # AC 8 (Build-perf baseline numbers in PR description: before/after vite build and pnpm check)
  #   -> Scenario: PR description includes before/after build-perf numbers
  #
  # AC 9 (Follow-up issues filed for each remaining styled Svelte file, blocked by this PR, referencing ADR-020)
  #   -> Scenario: Follow-up issues exist for each remaining styled Svelte component
  #
  # Coexistence guardrails (challenge findings):
  #   -> Scenario: Non-migrated components still render against the existing tokens
  #   -> Scenario: design-prototype/ is not scanned by Tailwind unless ADR-020 says so
