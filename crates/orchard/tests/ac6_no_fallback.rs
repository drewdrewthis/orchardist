//! AC6 — no fallback machinery exists in the codebase.
//!
//! These are source-level grep tests that pin the absence of the removed
//! `FallbackAdapter` type and `fallback_kind` struct field. If a future
//! refactor reintroduces either, these tests fail and point the author at
//! AC6 / ADR-008 / issue #329 for context.
//!
//! The forbidden strings are assembled at runtime (not embedded as literals)
//! so reading this test file does not itself trip the assertions — the same
//! technique used in `ac11_mutation_invariance.rs`.

use std::path::{Path, PathBuf};

/// Recursively collects all `.rs` source files under `dir`.
fn collect_rs_files(dir: &Path, out: &mut Vec<PathBuf>) {
    let entries = match std::fs::read_dir(dir) {
        Ok(e) => e,
        Err(_) => return,
    };
    for entry in entries.flatten() {
        let path = entry.path();
        if path.is_dir() {
            collect_rs_files(&path, out);
        } else if path.extension().map(|x| x == "rs").unwrap_or(false) {
            out.push(path);
        }
    }
}

/// Returns all `.rs` source files under `crates/orchard/src/`.
fn src_files() -> Vec<PathBuf> {
    let src_dir = Path::new(env!("CARGO_MANIFEST_DIR")).join("src");
    let mut files = Vec::new();
    collect_rs_files(&src_dir, &mut files);
    files
}

/// Assembles the forbidden `FallbackAdapter` type name at runtime so this
/// file's own source does not trip the assertion.
fn fallback_adapter_type() -> String {
    format!("{}{}", "Fallback", "Adapter")
}

/// Assembles the forbidden `fallback_kind:` field spelling at runtime.
fn fallback_kind_field() -> String {
    format!("{}{}", "fallback_kind", ":")
}

/// No source file under `crates/orchard/src/` may contain the string
/// `FallbackAdapter`. The type was removed as part of AC6 (issue #329).
#[test]
fn no_fallback_adapter_type_in_src() {
    let needle = fallback_adapter_type();
    let mut found: Vec<String> = Vec::new();

    for path in src_files() {
        let src = std::fs::read_to_string(&path).unwrap_or_default();
        if src.contains(&needle) {
            found.push(path.display().to_string());
        }
    }

    assert!(
        found.is_empty(),
        "FallbackAdapter must not exist anywhere under crates/orchard/src/ \
         (AC6 / ADR-008 / issue #329). Found in:\n{}",
        found.join("\n")
    );
}

/// No source file under `crates/orchard/src/` may contain the struct field
/// spelling `fallback_kind:`. The field was removed from `RemoteConfig` as
/// part of AC6 (issue #329).
#[test]
fn no_fallback_kind_field_in_src() {
    let needle = fallback_kind_field();
    let mut found: Vec<String> = Vec::new();

    for path in src_files() {
        let src = std::fs::read_to_string(&path).unwrap_or_default();
        if src.contains(&needle) {
            found.push(path.display().to_string());
        }
    }

    assert!(
        found.is_empty(),
        "The `fallback_kind:` field must not exist anywhere under crates/orchard/src/ \
         (AC6 / ADR-008 / issue #329). Found in:\n{}",
        found.join("\n")
    );
}
