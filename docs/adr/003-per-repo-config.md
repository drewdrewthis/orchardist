# ADR-003: Per-repo configuration via `.orchard.json`

## Status

Accepted

## Context

Orchard needs per-repo configuration for things like:
- Which CI checks to ignore vs require
- Custom display preferences
- Future: custom grouping rules, notification settings

Currently, per-repo remote config lives in `.git/orchard.json` (local, not committed). Global config lives in `~/.config/orchard/config.json`.

As an OSS tool, orchard needs a config story that works for:
1. **Solo developers** — quick setup, minimal config
2. **Teams** — shared conventions (which CI checks matter)
3. **Multi-repo** — different rules per repo

## Decision

Use a **two-layer config** with merge semantics:

### Layer 1: `.orchard.json` in repo root (committable, shareable)

Team-shared config. Checked into the repo. Any orchard user who clones the repo gets sensible defaults.

```json
{
  "ci": {
    "ignore": ["codecov/patch", "deploy-preview"],
    "required": ["test", "build"]
  }
}
```

### Layer 2: `.git/orchard.json` (local, private)

User-local overrides. Not committed. For personal preferences and sensitive config (remotes, SSH hosts).

```json
{
  "remotes": [{ "host": "user@10.0.0.1", "path": "~/repo" }],
  "ci": {
    "ignore": ["slow-integration-tests"]
  }
}
```

### Merge semantics

- `.orchard.json` (repo root) loads first as the base
- `.git/orchard.json` overlays on top
- Array fields (`ci.ignore`, `ci.required`) are **unioned**, not replaced
- Object fields are **merged** (local overrides shared)
- `remotes` only comes from `.git/orchard.json` (never committed)

### Global config relationship

The global `~/.config/orchard/config.json` references repos by slug and path. It does NOT duplicate per-repo settings. Per-repo config is always read from the repo itself.

The global config holds:
- Repo registry (slug, path, remotes as legacy fallback)
- Machine-local user preferences that describe the *user's environment*, not any individual repo

**Amendment (issue #30):** Machine-local user preferences are allowed in the global config alongside the repo registry. The first such preference is `terminal_app` — the macOS bundle ID of the terminal app to activate when a notification is clicked (e.g. `"com.googlecode.iterm2"`). These preferences belong in global config because they describe the machine/user environment, not any specific repository. They are set via `orchard-tui init` and persist to `~/.config/orchard/config.json`.

### Migration

The existing `.git/orchard.json` `remotes` field continues to work unchanged. New fields (`ci`) can appear in either layer. No breaking changes.

## Consequences

- Teams can commit `.orchard.json` with shared CI rules — new contributors get them automatically
- Individuals can override locally via `.git/orchard.json`
- OSS repos can ship `.orchard.json` to configure orchard for contributors
- The global config stays minimal (just a repo registry)
- `.orchard.json` should be added to orchard's own repo as a dogfooding example

## Alternatives considered

**Only `.git/orchard.json`** — rejected because teams can't share config. Every contributor configures from scratch.

**Only repo-root `.orchard.json`** — rejected because remotes and personal overrides need a private layer.

**Config in `~/.config/orchard/config.json` per-repo** — rejected because it couples repo config to a specific machine. Doesn't travel with the repo.
