//! Canonical data model types for Orchard.
//!
//! Defines one struct per entity, collapsing the historical 4-type-per-entity
//! mapping chain (Cached → Info → State → Json) into a single authoritative type.
//! Consumers migrate to these types over subsequent phases; existing types in
//! `cache.rs`, `derive.rs`, `orchard_state.rs`, and `json_output.rs` coexist
//! unchanged until they are deleted in Phase 3.
//!
//! # Module layout
//!
//! | Module | Types |
//! |--------|-------|
//! | `check` | `CheckInfo`, `CiChecks` (re-exported), `DisplayGroup` |
//! | `claude` | `Claude`, `RateLimits`, `RateLimit` |
//! | `issue` | `Issue`, `SubIssue` |
//! | `pr` | `Pr`, `Review` |
//! | `repo` | `Repo`, `FetchError` |
//! | `session` | `Session`, `Window`, `Pane` |
//! | `worktree` | `Worktree`, `AheadBehind` |

pub mod check;
pub mod claude;
pub mod issue;
pub mod pr;
pub mod repo;
pub mod session;
pub mod worktree;

pub use check::{CheckInfo, CiChecks, DisplayGroup};
pub use claude::{Claude, RateLimit, RateLimits};
pub use issue::{Issue, SubIssue};
pub use pr::{Pr, Review};
pub use repo::{FetchError, Repo};
pub use session::{Pane, Session, Window};
pub use worktree::{AheadBehind, Worktree};
