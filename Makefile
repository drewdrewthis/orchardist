# Top-level Makefile for the polyglot git-orchard-rs repo.
#
# Three binaries coexist (per ADR-013):
#   - `orchard` (Rust)        — thin dispatcher; routes verbs to helpers.
#   - `orchard-tui` (Rust)    — TUI dashboard. Dispatched as `orchard tui`.
#   - `orchard-daemon` (Go)   — GraphQL daemon + read queries + config.
#                               Dispatched as `orchard daemon ...`.
#   - `orchard-worktree` (Rust) — worktree mutation CLI. Dispatched as
#                               `orchard worktree ...` and via bare verbs
#                               (`orchard new`, `orchard rm`, etc.).
#
# Build artifacts:
#   bin/orchard-daemon                    — Go binary
#   target/release/orchard                — dispatcher (Rust)
#   target/release/orchard-tui            — TUI (Rust)
#   target/release/orchard-worktree       — worktree CLI (Rust)
#
# Install layout (after `make install`):
#   /usr/local/bin/orchard                → dispatcher (the only binary
#                                            users invoke directly)
#   /usr/local/bin/orchard-{tui,daemon,worktree}
#                                         → helper binaries the dispatcher
#                                            execs by name

.PHONY: daemon generate rust dispatcher worktree-cli gui all clean \
        install install-daemon install-dispatcher install-tui install-worktree-cli \
        test test-go test-rust \
        plugins-contracts-mcp

VERSION ?= dev

# Go binary — orchard-daemon. Built from cmd/orchard-daemon.
# Pass VERSION=<semver> to bake a release version: make daemon VERSION=1.2.3
daemon:
	go build -ldflags "-X main.version=$(VERSION)" -o bin/orchard-daemon ./cmd/orchard-daemon

# Generate gqlgen types/stubs from schema.graphql + gqlgen.yml.
# Generated files live under internal/server/graphql/ and are committed
# (see README — schema-first, codegen is reproducible but committing
# keeps the build hermetic in CI without forcing a gqlgen install).
generate:
	go generate ./...
	# Mirror schema.graphql into the resolvers package so go:embed can
	# bake it into the daemon binary for Query.schemaSDL (#469 F10).
	cp schema.graphql internal/server/resolvers/schema.graphql

# Rust release builds — one target per crate.
rust: dispatcher
	cargo build --release -p orchard
	cargo build --release -p orchard-worktree

dispatcher:
	cargo build --release -p orchard-dispatcher

worktree-cli:
	cargo build --release -p orchard-worktree

gui:
	cd crates/orchard-gui/src-tauri && cargo tauri build

all: daemon rust

install: install-dispatcher install-daemon install-tui install-worktree-cli

install-dispatcher: dispatcher
	install -m 755 target/release/orchard /usr/local/bin/orchard

install-daemon: daemon
	install -m 755 bin/orchard-daemon /usr/local/bin/orchard-daemon

install-tui: rust
	install -m 755 target/release/orchard-tui /usr/local/bin/orchard-tui

install-worktree-cli: worktree-cli
	install -m 755 target/release/orchard-worktree /usr/local/bin/orchard-worktree

clean:
	rm -rf bin/ target/

test: test-go test-rust

test-go:
	go test ./...

test-rust:
	cargo test

# Build the conversation-contracts MCP server binary.
# The binary is invoked by Claude Code when the plugin's MCP server is active.
plugins-contracts-mcp:
	go build -o plugins/conversation-contracts/bin/contracts-mcp \
		./plugins/conversation-contracts/mcp
