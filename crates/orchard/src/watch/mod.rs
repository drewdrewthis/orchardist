//! Event-driven watch system with subscription model.
//!
//! The watcher detects state changes by diffing `OrchardState` snapshots,
//! emits `WatchEvent`s, and delivers them to all registered subscribers.

pub mod daemon;
pub mod diff;
pub mod event;
pub mod subscription;
pub mod threshold;

pub use event::{EventKind, WatchEvent};
