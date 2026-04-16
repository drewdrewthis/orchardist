//! Stale-while-revalidate (SWR) framework for outbound calls.
//!
//! Every outbound call (SSH exec, `gh api`, `ssh boxd.sh list`) flows through
//! this wrapper so cached reads return instantly and revalidation happens in
//! the background when entries are stale. The file layer (on-disk cache in
//! `~/.cache/orchard/*.json`) is the source of truth; SWR adds age-based
//! state classification on top.
//!
//! # State machine
//!
//! For a given `(kind, scope)` cache key:
//!
//! | state          | condition                                          |
//! | -------------- | -------------------------------------------------- |
//! | `Missing`      | no cache file on disk (or corrupted)               |
//! | `Fresh`        | age ≤ ttl                                          |
//! | `Stale`        | ttl < age ≤ max_age, no revalidation in flight     |
//! | `Revalidating` | in-memory flag: a background fetch is running      |
//! | `Expired`      | age > max_age                                      |
//!
//! # Transitions
//!
//! - `Missing` → caller fetches synchronously → `Fresh`
//! - `Fresh` → age exceeds TTL → `Stale`
//! - `Stale` → caller requests → `Revalidating` (spawns background fetch)
//! - `Revalidating` → adapter Ok → `Fresh`
//! - `Revalidating` → adapter Err → `Stale` with retry_at = now + backoff
//! - `Stale` → age exceeds max_age → `Expired`
//! - `Expired` → caller requests → treated as cold start (synchronous refresh)
//!
//! # Clock
//!
//! Age is computed as `Utc::now() - cache_file.last_refreshed`. Negative
//! durations (process clock rolled back, NTP jump, or disk timestamp is in the
//! future) are clamped to the `Stale` transition so the next caller performs
//! a synchronous refresh rather than panicking or trusting a bogus "age".
//!
//! # Corrupted files
//!
//! `cache::read_cache` already maps parse failures to an epoch-dated empty
//! `CacheFile`. This module treats such entries as `Missing` via the age
//! check (epoch is far older than any configurable `max_age`), so corrupted
//! files drive a cold-start refresh rather than an infinite stall.

use std::time::Duration;

use chrono::{DateTime, Utc};
use serde::{Deserialize, Serialize};

// ---------------------------------------------------------------------------
// Kinds
// ---------------------------------------------------------------------------

/// Categorises an outbound call so the SWR layer can apply the right TTL and
/// cache-file suffix.
///
/// Each kind maps to a per-call TTL and `max_age` in [`SwrConfig`]. Callers
/// pick a `kind` once at the call site; tests and debug output can round-trip
/// the value through `kind_str` / `Display`.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Hash, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum SwrKind {
    /// `git worktree list --porcelain` over SSH, per remote.
    RemoteWorktrees,
    /// `tmux list-sessions` over SSH, per remote.
    RemoteSessions,
    /// `ssh <host> true` host reachability probe.
    HostReachable,
    /// `ssh <golden_host> list --json` fork enumeration.
    BoxdListVms,
    /// `gh api` calls for pull requests, per repo.
    GithubPrs,
    /// `gh api` calls for issues, per repo.
    GithubIssues,
}

impl SwrKind {
    /// Returns the stable snake_case string identifier for this kind.
    pub fn as_str(&self) -> &'static str {
        match self {
            SwrKind::RemoteWorktrees => "remote_worktrees",
            SwrKind::RemoteSessions => "remote_sessions",
            SwrKind::HostReachable => "host_reachable",
            SwrKind::BoxdListVms => "boxd_list_vms",
            SwrKind::GithubPrs => "github_prs",
            SwrKind::GithubIssues => "github_issues",
        }
    }
}

impl std::fmt::Display for SwrKind {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.write_str(self.as_str())
    }
}

// ---------------------------------------------------------------------------
// State
// ---------------------------------------------------------------------------

/// The result of classifying a cache entry against the current TTL / max_age.
///
/// `Revalidating` is intentionally NOT a variant here — it is a property of
/// the in-flight task set, not of the on-disk entry. Callers owning the
/// in-flight set (e.g. `cache_sources`) track that separately via an
/// `Arc<Mutex<HashSet<_>>>` keyed by `(kind, scope)`.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum SwrState {
    /// No cache entry — caller must fetch synchronously.
    Missing,
    /// Cache is within its TTL; return cached value, no fetch.
    Fresh,
    /// Cache is past TTL but within max_age; return cached value AND spawn
    /// a background revalidation (unless one is already in flight for this key).
    Stale,
    /// Cache is past max_age; treat as a cold start — caller must fetch
    /// synchronously. Expired entries are NOT returned to callers.
    Expired,
}

// ---------------------------------------------------------------------------
// Per-kind config
// ---------------------------------------------------------------------------

/// Per-kind TTL + `max_age` settings for the SWR wrapper.
///
/// TTLs follow the feature-file defaults (`specs/features/boxd-first-class-
/// backend.feature` AC5, lines 294-301). `max_age` defaults to 24h across all
/// kinds so stale-but-not-expired entries can survive an hour-scale outage.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct SwrConfig {
    /// Max age at which a cache entry is considered `Fresh`.
    pub ttl: Duration,
    /// Upper bound before transitioning to `Expired` (caller re-fetches
    /// synchronously, ignoring cached value).
    pub max_age: Duration,
}

impl SwrConfig {
    /// The feature-file default TTL + max_age for a given `kind`.
    ///
    /// AC5 per-kind defaults:
    /// - `RemoteWorktrees`: 60s — remote worktree list changes on fork churn
    /// - `RemoteSessions`: 30s — sessions start/stop frequently
    /// - `HostReachable`: 30s — fast feedback when a host goes down
    /// - `BoxdListVms`: 30s — fork-per-issue churns hourly
    /// - `GithubPrs`: 60s — gh api is rate-limited
    /// - `GithubIssues`: 120s — issues change less often than PRs
    pub fn default_for(kind: SwrKind) -> Self {
        let ttl = match kind {
            SwrKind::RemoteWorktrees => Duration::from_secs(60),
            SwrKind::RemoteSessions => Duration::from_secs(30),
            SwrKind::HostReachable => Duration::from_secs(30),
            SwrKind::BoxdListVms => Duration::from_secs(30),
            SwrKind::GithubPrs => Duration::from_secs(60),
            SwrKind::GithubIssues => Duration::from_secs(120),
        };
        SwrConfig {
            ttl,
            max_age: Duration::from_secs(24 * 60 * 60),
        }
    }
}

// ---------------------------------------------------------------------------
// Classification
// ---------------------------------------------------------------------------

/// Classifies a cache entry into `Missing | Fresh | Stale | Expired` using the
/// entry's wall-clock `last_refreshed` against the provided `config`.
///
/// `last_refreshed == None` (no cache file, or a corrupted / empty file)
/// returns `Missing`. Negative ages — caused by a disk timestamp in the
/// future, or a wall-clock jump backwards between refreshes — are clamped
/// to `Stale` so the next caller performs a synchronous-fetch rather than
/// trusting the bogus timestamp. The alternative (treating negative age as
/// `Fresh`) would pin the cache and block revalidation indefinitely.
pub fn classify(
    last_refreshed: Option<DateTime<Utc>>,
    now: DateTime<Utc>,
    config: SwrConfig,
) -> SwrState {
    let Some(ts) = last_refreshed else {
        return SwrState::Missing;
    };

    let age_seconds = (now - ts).num_seconds();
    if age_seconds < 0 {
        // Clock skew / filesystem lied. Do not trust the timestamp.
        return SwrState::Stale;
    }
    let age = Duration::from_secs(age_seconds as u64);

    if age > config.max_age {
        SwrState::Expired
    } else if age > config.ttl {
        SwrState::Stale
    } else {
        SwrState::Fresh
    }
}

/// Convenience: classify a `CacheFile<T>`, treating the sentinel epoch
/// timestamp (produced by `cache::read_cache` for missing or corrupted
/// files) as `None` so the caller sees `Missing`, not `Expired`.
pub fn classify_cache_file<T>(
    file: &crate::cache::CacheFile<T>,
    now: DateTime<Utc>,
    config: SwrConfig,
) -> SwrState {
    let ts = file.last_refreshed;
    // `cache::CacheFile::empty` uses `DateTime::from_timestamp(0, 0)` (the
    // Unix epoch). Treat that sentinel as Missing — otherwise a missing or
    // corrupted cache would classify as Expired and pollute the state
    // machine's semantics for legitimately-old entries.
    if ts.timestamp() == 0 {
        return SwrState::Missing;
    }
    classify(Some(ts), now, config)
}

#[cfg(test)]
mod tests {
    use super::*;
    use chrono::TimeZone;

    fn at(secs: i64) -> DateTime<Utc> {
        // Offset from a fixed anchor so negative ages are possible without
        // relying on the Unix-epoch sentinel used by `classify_cache_file`.
        Utc.timestamp_opt(1_700_000_000 + secs, 0).unwrap()
    }

    fn cfg(ttl: u64, max: u64) -> SwrConfig {
        SwrConfig {
            ttl: Duration::from_secs(ttl),
            max_age: Duration::from_secs(max),
        }
    }

    #[test]
    fn missing_when_no_timestamp() {
        assert_eq!(classify(None, at(0), cfg(60, 3600)), SwrState::Missing);
    }

    #[test]
    fn fresh_when_age_within_ttl() {
        let now = at(100);
        let stamp = at(50); // age 50s
        assert_eq!(classify(Some(stamp), now, cfg(60, 3600)), SwrState::Fresh);
    }

    #[test]
    fn stale_when_age_past_ttl_but_within_max_age() {
        let now = at(200);
        let stamp = at(100); // age 100s
        assert_eq!(classify(Some(stamp), now, cfg(60, 3600)), SwrState::Stale);
    }

    #[test]
    fn expired_when_age_past_max_age() {
        let now = at(4000);
        let stamp = at(0); // age 4000s
        assert_eq!(classify(Some(stamp), now, cfg(60, 3600)), SwrState::Expired);
    }

    #[test]
    fn negative_age_clamped_to_stale_not_fresh() {
        let now = at(0);
        let future_stamp = at(1_000); // stamp is 1000s ahead of now
        assert_eq!(
            classify(Some(future_stamp), now, cfg(60, 3600)),
            SwrState::Stale
        );
    }

    #[test]
    fn default_ttls_match_feature_file() {
        assert_eq!(
            SwrConfig::default_for(SwrKind::RemoteWorktrees).ttl,
            Duration::from_secs(60)
        );
        assert_eq!(
            SwrConfig::default_for(SwrKind::RemoteSessions).ttl,
            Duration::from_secs(30)
        );
        assert_eq!(
            SwrConfig::default_for(SwrKind::HostReachable).ttl,
            Duration::from_secs(30)
        );
        assert_eq!(
            SwrConfig::default_for(SwrKind::BoxdListVms).ttl,
            Duration::from_secs(30)
        );
        assert_eq!(
            SwrConfig::default_for(SwrKind::GithubPrs).ttl,
            Duration::from_secs(60)
        );
        assert_eq!(
            SwrConfig::default_for(SwrKind::GithubIssues).ttl,
            Duration::from_secs(120)
        );
    }

    #[test]
    fn default_max_age_is_24_hours() {
        for kind in [
            SwrKind::RemoteWorktrees,
            SwrKind::RemoteSessions,
            SwrKind::HostReachable,
            SwrKind::BoxdListVms,
            SwrKind::GithubPrs,
            SwrKind::GithubIssues,
        ] {
            assert_eq!(
                SwrConfig::default_for(kind).max_age,
                Duration::from_secs(24 * 60 * 60),
                "max_age default differs for {kind}"
            );
        }
    }

    #[test]
    fn kind_as_str_round_trips() {
        for (kind, s) in [
            (SwrKind::RemoteWorktrees, "remote_worktrees"),
            (SwrKind::RemoteSessions, "remote_sessions"),
            (SwrKind::HostReachable, "host_reachable"),
            (SwrKind::BoxdListVms, "boxd_list_vms"),
            (SwrKind::GithubPrs, "github_prs"),
            (SwrKind::GithubIssues, "github_issues"),
        ] {
            assert_eq!(kind.as_str(), s);
            assert_eq!(format!("{kind}"), s);
        }
    }

    #[test]
    fn classify_cache_file_fresh_within_ttl() {
        let now = at(50);
        let file: crate::cache::CacheFile<u32> = crate::cache::CacheFile {
            last_refreshed: at(20), // 30s old
            entries: vec![1, 2, 3],
        };
        assert_eq!(
            classify_cache_file(&file, now, cfg(60, 3600)),
            SwrState::Fresh
        );
    }

    #[test]
    fn classify_cache_file_stale_beyond_ttl() {
        let now = at(200);
        let file: crate::cache::CacheFile<u32> = crate::cache::CacheFile {
            last_refreshed: at(20), // 180s old
            entries: vec![1],
        };
        assert_eq!(
            classify_cache_file(&file, now, cfg(60, 3600)),
            SwrState::Stale
        );
    }

    #[test]
    fn classify_cache_file_treats_epoch_sentinel_as_missing() {
        let empty: crate::cache::CacheFile<u32> = crate::cache::CacheFile {
            last_refreshed: DateTime::from_timestamp(0, 0).unwrap(),
            entries: vec![],
        };
        // A 24h max_age would otherwise classify this as Expired — but
        // callers need Missing so they trigger a cold-start fetch.
        assert_eq!(
            classify_cache_file(&empty, at(0), cfg(60, 3600)),
            SwrState::Missing
        );
    }
}
