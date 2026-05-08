# ADR-014: Global config moves to `~/.orchard/config.json` (dotdir convention)

**Status:** ACCEPTED — issue #424.
**Date:** 2026-05-08
**Decides:** Where orchard's global configuration file lives on disk.

---

## TL;DR

- Orchard's global config moves from `~/.config/orchard/config.json` (XDG-style) to **`~/.orchard/config.json`** (dotdir).
- No backwards-compatible read fallback. The legacy path is ignored.
- When the new path is missing AND the legacy path exists, the daemon and CLI emit a one-line migration hint pointing at `mv ~/.config/orchard ~/.orchard`.
- State directory (`~/.local/state/orchard`) is **unchanged**. Only the config root moves.

---

## Context

Until now, orchard followed the XDG Base Directory spec: `XDG_CONFIG_HOME` if set, otherwise `~/.config/orchard/`. On macOS the loader also fell back to `~/Library/Application Support/orchard/`.

Every other widely-used CLI tool in our stack stores per-user config under a single dotdir directly in `$HOME`:

| Tool        | Path                       |
| ----------- | -------------------------- |
| AWS CLI     | `~/.aws/config`            |
| kubectl     | `~/.kube/config`           |
| npm         | `~/.npm/`                  |
| Cargo       | `~/.cargo/config.toml`     |
| ssh         | `~/.ssh/config`            |
| Claude Code | `~/.claude/`               |

Orchard was the odd one out. Users had to remember a different location for one tool — surfaced from a federation session where Drew flagged the inconsistency immediately after `orchard config add-peer` wrote to the XDG path.

## Decision

1. **Hardcode the path.** `global_config_path()` (Rust, `crates/orchard/src/global_config.rs`) and `orchpaths.ConfigDir()` (Go, `internal/orchpaths/orchpaths.go`) both return `$HOME/.orchard[/config.json]`. No `XDG_CONFIG_HOME` branch. No `~/Library/Application Support/orchard/` macOS fallback.

2. **Clean break, no fallback load.** The legacy file is never deserialized into runtime state. Pre-1.0 software with a small user base; backward-compat machinery isn't worth the complexity.

3. **Migration hint at the load-failure site only.** Both the Go daemon (`internal/cli/daemon/daemon.go::runStart`) and the Rust loader (`load_global_config`) perform a single `stat` of `~/.config/orchard/config.json` when the new path is missing. If the legacy file exists, they emit the migration hint:

   > Found legacy config at ~/.config/orchard/config.json — the canonical location is now ~/.orchard/config.json. To migrate: mv ~/.config/orchard ~/.orchard

   The hint is informational; the daemon's startup error is unchanged in shape. The legacy stat is **not** added to every `ConfigFile()` / `global_config_path()` caller — one stat per process at most.

4. **State directory unchanged.** `orchpaths.StateDir()` continues to return `~/.local/state/orchard`. The orchardist daemon's working directory at `~/.config/orchard/.orchardist/` is **out of scope** — separate concern, separate move.

## Consequences

### Positive

- Consistent with every other dotdir tool — one mental model for `~/.foo/`-style config across the stack.
- Drops two path-resolution branches (XDG, macOS Application Support) in both Rust and Go.
- Failure mode is loud, fast, and self-explanatory — users see the exact `mv` command they need.

### Negative

- **BREAKING** for anyone with config at the legacy path. Mitigated by the migration hint and a CHANGELOG entry at the top of the next release section.
- Any third-party tool that scripted around `~/.config/orchard/config.json` will need updating. The migration hint surfaces this immediately.
- Orchardist working dir + config root now diverge (`~/.config/orchard/.orchardist/` vs `~/.orchard/config.json`) until the orchardist move lands separately. Acceptable transitional state.

### Neutral

- ADR-001 (per-source cache architecture) and ADR-003 (per-repo config) reference the legacy path in prose. They are amended-by-reference here — the canonical path is now `~/.orchard/config.json`. Their inline mentions were updated alongside this ADR but their decisions stand unchanged.

## Alternatives considered

- **Keep XDG, document the convention loudly.** Rejected — no other tool in the stack honors XDG. Users will keep tripping over it.
- **Read from both paths, prefer new.** Rejected — keeps two write paths alive indefinitely; the migration never completes; the loader gets more complex, not simpler.
- **Auto-migrate on first run.** Rejected — silent file-system mutation is the kind of thing that should never surprise a user. A clear error with the exact command is honest and reversible.
- **Move state dir too.** Out of scope. State (PID file, future cache moves) follows different semantics than config; a separate ADR can decide that move on its own merits.

## References

- Issue: #424
- PR: #477
- Related ADRs: ADR-001, ADR-003 (mention the legacy path in prose; amended-by-reference here)
