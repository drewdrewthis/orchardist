# ADR-003: Per-Repo Configuration with CI Check Filtering

## Status

Accepted

## Context

Orchard needs per-repo configuration for behaviors like CI check filtering. Different repos have different noisy checks (codecov, deploy previews) that shouldn't affect the dashboard's display groups.

## Decision

### Two-layer config with merge semantics

- **`.orchard.json`** (repo root, committable) — team-shared defaults
- **`.git/orchard.json`** (local, gitignored) — personal overrides

The local layer overlays on top of the root layer. Array fields (`ci.ignore`, `ci.required`) use union semantics: values from both layers are concatenated and deduplicated.

### CI check filtering

```json
{
  "ci": {
    "ignore": ["codecov/patch", "deploy-preview"],
    "required": ["test", "build"]
  }
}
```

- **`ignore`** — checks matching these patterns (case-insensitive substring) are excluded from `checks_state` derivation
- **`required`** — if set, *only* matching checks count (takes precedence over `ignore`)
- **Neither set** — all checks count via the aggregate GraphQL rollup (no filtering)

### Individual check data

The GraphQL query fetches `statusCheckRollup.contexts` to get individual check names and states (`CachedCheckRun`). When CI config patterns are present, `checks_state` is re-derived from filtered individual checks. When no patterns are configured, the aggregate rollup state is used directly (zero overhead for unconfigured repos).

### Key types

- `RepoLocalConfig` — top-level per-repo config (currently only `ci: CiConfig`)
- `CiConfig` — CI filtering patterns with `filter_checks()` and `derive_checks_state()` methods
- `DeriveContext` — groups all inputs to `derive_worktree_rows` (replaces 7 positional params)
- `RepoCacheEntry` — named struct for per-repo cache data passed to `derive_all_repos`

## Consequences

- Teams can commit `.orchard.json` to share CI filtering across contributors
- Individuals can override with `.git/orchard.json` without affecting teammates
- Adding new per-repo config fields only requires extending `RepoLocalConfig`
- The existing `.git/orchard.json` remote loading in `global_config.rs` is unaffected (separate parse path)
