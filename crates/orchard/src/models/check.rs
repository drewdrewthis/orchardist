//! Canonical CI check types and display group classification.
//!
//! Re-exports `CheckInfo` and `CiChecks` from `ci_state`, and re-exports
//! `DisplayGroup` from `derive` — the single authoritative definition.

// Re-export CI check types from their authoritative location.
pub use crate::ci_state::{CheckInfo, CiChecks};

// Re-export the canonical DisplayGroup from derive.
pub use crate::derive::DisplayGroup;
