# Contributing to Git Orchard

Thanks for your interest in contributing! This guide covers everything you need to get started.

## Prerequisites

- [Rust](https://www.rust-lang.org/tools/install) (edition 2024)
- [tmux](https://github.com/tmux/tmux) (for session management features)
- [gh](https://cli.github.com/) (GitHub CLI, for PR/issue integration)
- [jq](https://jqlang.github.io/jq/) (for JSON processing in shell scripts)

## Getting Started

1. Fork and clone the repository:

   ```bash
   gh repo fork drewdrewthis/orchardist --clone
   cd orchardist
   ```

2. Build the project:

   ```bash
   cargo build --release
   ```

3. Run tests:

   ```bash
   cargo test
   ```

4. Run the TUI:

   ```bash
   cargo run
   ```

## Development Workflow

### Build and Test

After every code change, run both:

```bash
cargo test
cargo build --release
```

Both must pass before submitting a PR.

### Linting

Run clippy and rustfmt before committing:

```bash
cargo clippy -- -D warnings
cargo fmt --check
```

To auto-format:

```bash
cargo fmt
```

### Architecture

Read [docs/architecture.md](docs/architecture.md) for the full picture. Key principles:

- **Functional Core, Imperative Shell** — pure functions compute meaning, shell modules fetch data
- **Modules are service boundaries** — no service objects or traits for testability
- **SRP at every level** — files under 300 lines, one responsibility per module/function

## Submitting a Pull Request

1. Create a feature branch from `main`:

   ```bash
   git checkout -b your-feature main
   ```

2. Make your changes with tests.

3. Ensure `cargo test` and `cargo build --release` both pass.

4. Run `cargo clippy -- -D warnings` and `cargo fmt --check`.

5. Push and open a PR against `main`:

   ```bash
   git push -u origin your-feature
   gh pr create
   ```

### PR Guidelines

- Keep PRs focused — one feature or fix per PR.
- Include tests for new functionality.
- Add doc comments (`///`) for new public functions and types.
- Link related issues in the PR description (e.g., "Closes #42").
- PRs require passing CI checks before merge.

## Reporting Bugs

Use the [bug report template](https://github.com/drewdrewthis/orchardist/issues/new?template=bug_report.md) to file bugs with steps to reproduce, expected/actual behavior, and your environment.

## Requesting Features

Use the [feature request template](https://github.com/drewdrewthis/orchardist/issues/new?template=feature_request.md) to propose new functionality.

## License

By contributing, you agree that your contributions will be licensed under the [MIT License](LICENSE).
