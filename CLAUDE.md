# Git Orchard (Rust)

## Build & Test

After **every** code change, run both:

```bash
cargo test
cargo build --release
```

Both must pass before work is considered complete. No exceptions.

The release binary is symlinked from `~/Library/pnpm/orchard` → `target/release/orchard`, so `cargo build --release` automatically updates the user's installed command.

## Architecture

Read `docs/architecture.md` for the full picture. Key principles:

- **Functional Core, Imperative Shell** — pure `build_state()` joins data, shell modules fetch it
- **Modules are service boundaries** — no service objects or traits for testability
- **SRP at every level** — files ≤ 300 lines, one responsibility per module/function
- **`OrchardState`** is the single unified data model consumed by both TUI and `--json`
- **`--json` always fetches fresh** — never returns cached results
- ADRs live in `docs/adr/`

## Documentation

Every public module, function, and type must have doc comments:
- `//!` module-level docs at the top of each file (what, why, how it fits)
- `///` on every public function and type
- Code examples in doc comments are tested by `cargo test`

## Project Structure

- Rust + Ratatui TUI for managing git worktrees
- Supports local and remote (SSH) worktrees with tmux session management
- Per-repo config: `.orchard.json` (committable) + `.git/orchard.json` (local)
- Global config: `~/.config/orchard/config.json`
