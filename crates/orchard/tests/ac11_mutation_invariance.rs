//! AC11 — federation must not alter mutation paths.
//!
//! Mutation functions (kill remote session, remove remote worktree, create
//! remote session) and any future worktree-transfer code must NOT dispatch
//! on `RemoteKind`. They operate on `&str host` only, so an OrchardProxy
//! remote transparently flows through the same legacy SSH commands as
//! Remmy / BoxdShared / BoxdFork.
//!
//! This invariant is pinned here as a source-level grep. If a future refactor
//! introduces a federation-specific mutation branch, these tests fail and
//! point the author at AC11 / ADR-008 / issue #329 for context.

use std::path::{Path, PathBuf};

/// Kind-enum identifier, assembled at runtime so the forbidden pattern never
/// appears as a string literal in this test file itself. Otherwise a
/// self-read via `include_str!` would always trip the assertion.
fn kind_prefix() -> String {
    format!("{}{}{}", "Remote", "Kind", "::")
}

/// The current mutation module in the orchard crate. Lives at
/// `crates/orchard/src/remote.rs` relative to the crate manifest. Returned as
/// a source string so tests can scan it without opening it each time.
fn read_mutation_module() -> (PathBuf, String) {
    let path = Path::new(env!("CARGO_MANIFEST_DIR"))
        .join("src")
        .join("remote.rs");
    let src = std::fs::read_to_string(&path).expect("read crates/orchard/src/remote.rs");
    (path, src)
}

#[test]
fn mutation_module_does_not_dispatch_on_kind_enum() {
    let (path, src) = read_mutation_module();
    let prefix = kind_prefix();
    for variant in ["OrchardProxy", "Remmy", "BoxdShared", "BoxdFork"] {
        let arm = format!("{prefix}{variant} =>");
        assert!(
            !src.contains(&arm),
            "{} must not dispatch on the kind enum (found a match arm for \
             variant {variant}) — mutations stay on the legacy per-host SSH \
             path. See AC11 / ADR-008 in this issue.",
            path.display()
        );
    }
}

#[test]
fn mutation_module_does_not_import_kind_enum() {
    let (path, src) = read_mutation_module();
    let use_line = format!("use crate::remote_adapter::{}", "RemoteKind");
    assert!(
        !src.contains(&use_line),
        "{} must not import the kind enum (AC11 / ADR-008).",
        path.display()
    );
}

/// No transfer module exists in this crate today; the feature file references
/// it aspirationally. If one ever appears, it must honour the same invariant.
#[test]
fn transfer_module_if_present_does_not_dispatch_on_kind_enum() {
    let path = Path::new(env!("CARGO_MANIFEST_DIR"))
        .join("src")
        .join("transfer.rs");
    if !path.exists() {
        return;
    }
    let src = std::fs::read_to_string(&path).expect("read transfer.rs");
    let prefix = kind_prefix();
    let arm = format!("{prefix}OrchardProxy =>");
    assert!(
        !src.contains(&arm),
        "{} must not dispatch on the kind enum for the federated variant — \
         transfers stay on the legacy per-host path (AC11 / ADR-008).",
        path.display()
    );
}
