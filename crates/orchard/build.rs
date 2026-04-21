//! Build script for the `orchard` crate.
//!
//! Generates `schema.json` at the crate root from the `JsonOutput` wire-format
//! type definitions in `src/json_output_types.rs`. The schema is committed to
//! the repository so agents and scripts can reference it without running the
//! binary, and embedded into the binary via `include_str!` for `--schema`.
//!
//! Idempotent: the file is only written when its contents change, preventing
//! spurious rebuild loops.

use schemars::schema_for;
use std::{env, fs, path::PathBuf};

#[allow(dead_code)]
mod types {
    use schemars::JsonSchema;
    use serde::{Deserialize, Serialize};
    use std::collections::HashMap;
    include!("src/json_output_types.rs");
}

fn main() {
    println!("cargo:rerun-if-changed=src/json_output_types.rs");
    println!("cargo:rerun-if-changed=build.rs");

    let schema = schema_for!(types::JsonOutput);
    let pretty = serde_json::to_string_pretty(&schema).expect("schema serialization failed");

    // Write next to Cargo.toml (crate root = CARGO_MANIFEST_DIR).
    let crate_dir = PathBuf::from(env::var("CARGO_MANIFEST_DIR").unwrap());
    let out = crate_dir.join("schema.json");

    // Only write if contents differ to avoid spurious rebuild loops.
    let changed = match fs::read_to_string(&out) {
        Ok(existing) => existing != pretty,
        Err(_) => true,
    };
    if changed {
        fs::write(&out, &pretty).expect("failed to write schema.json");
    }
}
