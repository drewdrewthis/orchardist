# Top-level Makefile for the polyglot git-orchard-rs repo.
#
# Two halves coexist:
#   - Rust workspace at crates/{orchard,orchard-gui} — built via cargo.
#   - Go module at repo root (cmd/orchard) — built via go.
#
# Per ADR-011 the Go binary is the new orchard daemon + CLI. The Rust CLI
# stays for the time being and will sunset organically.

.PHONY: daemon generate rust gui all clean install test test-go test-rust

# Go binary — built from cmd/orchard, packages all subcommand groups.
daemon:
	go build -o bin/orchard ./cmd/orchard

# Generate gqlgen types/stubs from schema.graphql + gqlgen.yml.
# Generated files live under internal/server/graphql/ and are committed
# (see README — schema-first, codegen is reproducible but committing
# keeps the build hermetic in CI without forcing a gqlgen install).
generate:
	go generate ./...

rust:
	cargo build --release

gui:
	cd crates/orchard-gui/src-tauri && cargo tauri build

all: daemon rust

install: daemon
	install -m 755 bin/orchard /usr/local/bin/orchard

clean:
	rm -rf bin/ target/

test: test-go test-rust

test-go:
	go test ./...

test-rust:
	cargo test
