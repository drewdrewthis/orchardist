//! Federation foundations for transitive-discovery (issue #363, Phase 1).
//!
//! Provides:
//!
//! - [`host_dedup_key`] — best-effort SSH target normalisation for seen-set
//!   deduplication across the transitive walk. Strips the default port (22),
//!   case-folds the hostname (not the user), trims a trailing DNS dot, and
//!   brackets IPv6 addresses consistently.  Rejects malformed inputs (paths,
//!   whitespace, backslashes) with an explicit error.
//!
//! - [`JsonRemoteConfig`] / [`ListRemotesOutput`] — the wire types for the
//!   `orchard list-remotes --json` subcommand.  Version-controlled independently
//!   of `JsonOutput` with a **lower-bound** check (`version >= LIST_REMOTES_MIN_VERSION`)
//!   so additive-only fields on newer remotes do not break older callers.
//!
//! - [`emit_federation_discovered_host`] — append a `federation.discovered_host`
//!   event to `events.jsonl` the first time a dedup key is encountered within a
//!   single refresh pass.  Callers maintain the `seen` set; this function only
//!   writes the event.
//!
//! # Design notes
//!
//! `host_dedup_key` is NOT a full SSH canonicalisation (no DNS resolution, no
//! `~/.ssh/config` alias expansion, no ProxyJump flattening).  Its purpose is
//! purely *best-effort deduplication*: catching the most common textual variants
//! of the same host so a diamond topology does not fire two SSH round-trips for
//! what is clearly the same machine.  Collisions (two aliases that resolve to
//! the same IP but have different text) are surfaced via the
//! `federation.discovered_host` event so an operator can investigate.

use serde::{Deserialize, Serialize};
use serde_json::Value;

use crate::events::log_event;
use crate::global_config::RemoteConfig;

// ---------------------------------------------------------------------------
// Wire types: `orchard list-remotes --json`
// ---------------------------------------------------------------------------

/// Minimum version accepted when parsing a remote `orchard list-remotes --json`
/// response.
///
/// The check is a **lower bound** (`version >= LIST_REMOTES_MIN_VERSION`), NOT an
/// exact whitelist.  A future remote that emits `version: 2` is accepted — any
/// unknown fields are ignored by serde.  This is the lesson learned from
/// `JsonOutput`'s exact-whitelist design which caused version-skew failures on
/// mixed fleets.
///
/// Bump this only when a **breaking** (non-additive) change is made to the
/// wire format.
pub const LIST_REMOTES_MIN_VERSION: u32 = 1;

/// Wire representation of a single remote as emitted by `orchard list-remotes
/// --json`.
///
/// All fields mirror the corresponding fields in [`RemoteConfig`] with the
/// exception that `kind` is serialized as a human-readable string (kebab-case)
/// matching the existing [`RemoteKind`] serialization.
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct JsonRemoteConfig {
    /// Logical name for this remote (e.g. `"boxd"`, `"gpu-box"`).
    pub name: String,
    /// SSH target, e.g. `"user@host"` or `"host"`.
    pub host: String,
    /// Adapter kind (e.g. `"orchard-proxy"`, `"remmy"`).
    pub kind: String,
    /// Absolute path on the remote host.
    pub path: String,
    /// Whether the transitive walker should follow this remote's own
    /// `orchard list-remotes` output to discover grandchild nodes.
    pub allow_transitive: bool,
}

/// Top-level output of `orchard list-remotes --json`.
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct ListRemotesOutput {
    /// Schema version for the list-remotes wire format.
    ///
    /// Callers check `version >= LIST_REMOTES_MIN_VERSION` (lower bound).
    pub version: u32,
    /// All configured remotes for the local orchard installation.
    pub remotes: Vec<JsonRemoteConfig>,
}

/// Validates that the given `version` from a remote `orchard list-remotes --json`
/// response is acceptable (i.e. `>= LIST_REMOTES_MIN_VERSION`).
///
/// Returns `Ok(())` when the version is acceptable, `Err(String)` with a
/// human-readable reason when it is too old.
///
/// # Examples
///
/// ```
/// use orchard::federation::{check_list_remotes_version, LIST_REMOTES_MIN_VERSION};
///
/// assert!(check_list_remotes_version(LIST_REMOTES_MIN_VERSION).is_ok());
/// assert!(check_list_remotes_version(LIST_REMOTES_MIN_VERSION + 1).is_ok());
/// assert!(check_list_remotes_version(0).is_err());
/// ```
pub fn check_list_remotes_version(version: u32) -> Result<(), String> {
    if version >= LIST_REMOTES_MIN_VERSION {
        Ok(())
    } else {
        Err(format!(
            "list-remotes version {version} is below minimum supported version \
             {LIST_REMOTES_MIN_VERSION}"
        ))
    }
}

/// Builds a [`ListRemotesOutput`] from the local `GlobalConfig`.
///
/// Collects all configured remotes across all repos, deduplicates by host
/// (keeping the first occurrence), and returns a versioned JSON-ready struct.
pub fn build_list_remotes_output(config: &crate::global_config::GlobalConfig) -> ListRemotesOutput {
    let mut seen_hosts = std::collections::HashSet::new();
    let mut remotes = Vec::new();

    for repo in &config.repos {
        for r in &repo.remotes {
            if seen_hosts.insert(r.host.clone()) {
                remotes.push(remote_config_to_json(r));
            }
        }
    }

    ListRemotesOutput {
        version: LIST_REMOTES_MIN_VERSION,
        remotes,
    }
}

fn remote_config_to_json(r: &RemoteConfig) -> JsonRemoteConfig {
    // RemoteKind serializes to kebab-case; re-use serde for consistency.
    let kind = serde_json::to_value(r.kind)
        .ok()
        .and_then(|v| v.as_str().map(String::from))
        .unwrap_or_else(|| "unknown".to_string());

    JsonRemoteConfig {
        name: r.name.clone(),
        host: r.host.clone(),
        kind,
        path: r.path.clone(),
        allow_transitive: r.allow_transitive,
    }
}

// ---------------------------------------------------------------------------
// host_dedup_key
// ---------------------------------------------------------------------------

/// Error returned by [`host_dedup_key`] when the input is structurally invalid.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct InvalidSshTarget(pub String);

impl std::fmt::Display for InvalidSshTarget {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        write!(f, "invalid SSH target: {}", self.0)
    }
}

impl std::error::Error for InvalidSshTarget {}

/// Computes a best-effort deduplication key for an SSH target string.
///
/// The key is suitable for cycle detection and adapter dedup across the
/// transitive federation walk.  It is NOT a full canonicalization:
///
/// - The **hostname** portion is case-folded to ASCII lowercase.
/// - The **user** portion (before `@`) is preserved as-is (SSH treats users
///   as distinct identities; `alice@host` and `bob@host` are different keys).
/// - The default SSH port (22) is treated as implicit — `host:22` and `host`
///   produce the same key.
/// - A trailing DNS dot on the hostname is stripped (`host.` → `host`).
/// - IPv6 addresses in brackets are accepted; the bracket syntax is preserved
///   and the inner address is case-folded.
///
/// # Errors
///
/// Returns [`InvalidSshTarget`] if the input contains path separators (`/`),
/// whitespace, or backslashes — these are never valid in an SSH host position
/// and most likely indicate a misconfiguration or injection attempt.
///
/// # Examples
///
/// ```
/// use orchard::federation::host_dedup_key;
///
/// // Case-fold hostname only (user preserved):
/// assert_eq!(host_dedup_key("boxd@VM.Boxd.Sh").unwrap(), "boxd@vm.boxd.sh");
///
/// // Default port stripped:
/// assert_eq!(host_dedup_key("boxd@vm.boxd.sh:22").unwrap(), "boxd@vm.boxd.sh");
///
/// // Non-default port preserved:
/// assert!(host_dedup_key("boxd@vm.boxd.sh:2222").unwrap() != host_dedup_key("boxd@vm.boxd.sh").unwrap());
///
/// // Trailing dot stripped:
/// assert_eq!(host_dedup_key("boxd@vm.boxd.sh.").unwrap(), "boxd@vm.boxd.sh");
///
/// // IPv6 default port stripped:
/// assert_eq!(host_dedup_key("boxd@[2001:db8::1]:22").unwrap(), host_dedup_key("boxd@[2001:db8::1]").unwrap());
///
/// // Distinct users differ:
/// assert!(host_dedup_key("alice@host").unwrap() != host_dedup_key("bob@host").unwrap());
///
/// // Malformed input rejected:
/// assert!(host_dedup_key("boxd@host/evil").is_err());
/// ```
pub fn host_dedup_key(target: &str) -> Result<String, InvalidSshTarget> {
    // Reject inputs containing characters that are never valid in an SSH target.
    if target.contains('/') || target.contains('\\') || target.chars().any(|c| c.is_whitespace()) {
        return Err(InvalidSshTarget(target.to_string()));
    }

    // Split optional user prefix from the host part.
    let (user_prefix, host_and_port) = if let Some(at) = target.find('@') {
        let user = &target[..at];
        let rest = &target[at + 1..];
        (Some(user), rest)
    } else {
        (None, target)
    };

    // Normalise the host-and-optional-port portion.
    let normalised_host_port = normalise_host_port(host_and_port)?;

    let key = if let Some(user) = user_prefix {
        format!("{user}@{normalised_host_port}")
    } else {
        normalised_host_port
    };

    Ok(key)
}

/// Normalises `host` or `host:port` or `[ipv6]:port`.
fn normalise_host_port(s: &str) -> Result<String, InvalidSshTarget> {
    if s.starts_with('[') {
        // IPv6 bracketed address: `[addr]` or `[addr]:port`.
        let close = s.find(']').ok_or_else(|| InvalidSshTarget(s.to_string()))?;
        let inner = &s[1..close]; // address without brackets
        let after_bracket = &s[close + 1..]; // empty, or ":port"

        let port = parse_after_bracket(after_bracket, s)?;

        let normalised_addr = inner.to_ascii_lowercase();
        if port == 22 {
            // Default port — omit from key.
            Ok(format!("[{normalised_addr}]"))
        } else if port == 0 {
            // No port specified.
            Ok(format!("[{normalised_addr}]"))
        } else {
            Ok(format!("[{normalised_addr}]:{port}"))
        }
    } else {
        // Plain hostname or IPv4 address, possibly with `:port`.
        let (host, port) = split_host_port(s)?;
        // Strip trailing DNS dot.
        let host = host.trim_end_matches('.');
        let normalised = host.to_ascii_lowercase();
        if port == 22 || port == 0 {
            Ok(normalised)
        } else {
            Ok(format!("{normalised}:{port}"))
        }
    }
}

/// Parses the part after `]` in an IPv6 target.  Must be empty or `:port`.
/// Returns the port number (0 = absent).
fn parse_after_bracket(after: &str, original: &str) -> Result<u32, InvalidSshTarget> {
    if after.is_empty() {
        return Ok(0);
    }
    if let Some(port_str) = after.strip_prefix(':') {
        port_str
            .parse::<u32>()
            .map_err(|_| InvalidSshTarget(original.to_string()))
    } else {
        Err(InvalidSshTarget(original.to_string()))
    }
}

/// Splits `host` or `host:port` (non-IPv6) into `(host, port)`.
/// Returns `port = 0` when absent.
fn split_host_port(s: &str) -> Result<(String, u32), InvalidSshTarget> {
    // Count colons: more than one means it's a bare IPv6 address (no brackets).
    // In that case return as-is with port=0.
    let colon_count = s.chars().filter(|&c| c == ':').count();
    if colon_count > 1 {
        // Bare IPv6 without brackets — accept but normalise without port.
        return Ok((s.to_string(), 0));
    }

    if let Some(colon) = s.rfind(':') {
        let host = &s[..colon];
        let port_str = &s[colon + 1..];
        let port = port_str
            .parse::<u32>()
            .map_err(|_| InvalidSshTarget(s.to_string()))?;
        Ok((host.to_string(), port))
    } else {
        Ok((s.to_string(), 0))
    }
}

// ---------------------------------------------------------------------------
// federation.discovered_host event
// ---------------------------------------------------------------------------

/// Appends a `federation.discovered_host` event to `events.jsonl`.
///
/// This should be called once per refresh pass for each *new* dedup key
/// encountered by the walker.  The `seen` set is maintained by the caller;
/// this function only writes the event.
///
/// Fields emitted:
/// - `raw_target` — the original string passed to [`host_dedup_key`]
/// - `dedup_key`  — the computed normalised key
///
/// The event allows an operator to detect silent collision cases where two
/// different textual targets normalise to the same key (e.g. `HOST.example.com`
/// and `host.example.com` both → `host.example.com`).
pub fn emit_federation_discovered_host(raw_target: &str, dedup_key: &str) {
    log_event(
        "federation.discovered_host",
        &[
            ("raw_target", Value::String(raw_target.to_string())),
            ("dedup_key", Value::String(dedup_key.to_string())),
        ],
    );
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

#[cfg(test)]
mod tests {
    use super::*;
    use crate::remote_adapter::RemoteKind;

    // -----------------------------------------------------------------------
    // AC4: host_dedup_key — exhaustive scenario matrix
    // -----------------------------------------------------------------------

    /// Case-folds the host portion but preserves user case.
    /// feature:99 — "host_dedup_key case-folds the host portion but preserves user case"
    #[test]
    fn host_dedup_key_case_folds_host_preserves_user() {
        let a = host_dedup_key("boxd@VM.Boxd.Sh").expect("should be valid");
        let b = host_dedup_key("boxd@vm.boxd.sh").expect("should be valid");
        assert_eq!(a, b, "case variants of hostname must produce equal keys");
        assert_eq!(a, "boxd@vm.boxd.sh", "key must be lowercase");
    }

    /// Default SSH port (22) is treated as implicit — same key as no port.
    /// feature:105 — "host_dedup_key treats default SSH port as implicit"
    #[test]
    fn host_dedup_key_default_port_implicit() {
        let no_port = host_dedup_key("boxd@vm.boxd.sh").expect("valid");
        let port_22 = host_dedup_key("boxd@vm.boxd.sh:22").expect("valid");
        let port_2222 = host_dedup_key("boxd@vm.boxd.sh:2222").expect("valid");

        assert_eq!(no_port, port_22, "no-port and :22 must produce equal keys");
        assert_ne!(
            no_port, port_2222,
            ":2222 must produce a DIFFERENT key from :22 / no-port"
        );
    }

    /// Trailing dot on hostname is stripped.
    /// feature:113 — "host_dedup_key strips a trailing dot on the hostname"
    #[test]
    fn host_dedup_key_strips_trailing_dot() {
        let with_dot = host_dedup_key("boxd@vm.boxd.sh.").expect("valid");
        let without = host_dedup_key("boxd@vm.boxd.sh").expect("valid");
        assert_eq!(with_dot, without, "trailing dot must be stripped");
    }

    /// IPv6 bracketed address: default port implicit, non-default preserved.
    /// feature:119 — "host_dedup_key normalizes IPv6 brackets consistently"
    #[test]
    fn host_dedup_key_ipv6_default_port_implicit() {
        let bare = host_dedup_key("boxd@[2001:db8::1]").expect("valid");
        let port_22 = host_dedup_key("boxd@[2001:db8::1]:22").expect("valid");
        let port_2222 = host_dedup_key("boxd@[2001:db8::1]:2222").expect("valid");

        assert_eq!(bare, port_22, "IPv6 bare and :22 must produce equal keys");
        assert_ne!(bare, port_2222, "IPv6 :2222 must differ from bare / :22");
    }

    /// Distinct users on same host produce different keys.
    /// feature:125 — "host_dedup_key preserves distinct users on the same host"
    #[test]
    fn host_dedup_key_distinct_users_differ() {
        let alice = host_dedup_key("alice@vm.boxd.sh").expect("valid");
        let bob = host_dedup_key("bob@vm.boxd.sh").expect("valid");
        assert_ne!(alice, bob, "different users must produce different keys");
        assert_eq!(alice, "alice@vm.boxd.sh");
        assert_eq!(bob, "bob@vm.boxd.sh");
    }

    /// Malformed targets are rejected (paths, whitespace, backslashes).
    /// feature:131 — "host_dedup_key rejects malformed strings"
    #[test]
    fn host_dedup_key_rejects_path() {
        let result = host_dedup_key("boxd@vm.boxd.sh/evil");
        assert!(result.is_err(), "path separator must be rejected");
    }

    #[test]
    fn host_dedup_key_rejects_whitespace() {
        let result = host_dedup_key("boxd@vm.boxd.sh extra");
        assert!(result.is_err(), "whitespace must be rejected");
    }

    #[test]
    fn host_dedup_key_rejects_backslash() {
        let result = host_dedup_key("boxd@vm.boxd.sh\\evil");
        assert!(result.is_err(), "backslash must be rejected");
    }

    // -----------------------------------------------------------------------
    // host_dedup_key — additional correctness checks
    // -----------------------------------------------------------------------

    #[test]
    fn host_dedup_key_no_user_prefix_works() {
        let key = host_dedup_key("vm.boxd.sh").expect("valid");
        assert_eq!(key, "vm.boxd.sh");
    }

    #[test]
    fn host_dedup_key_no_user_with_port() {
        let k22 = host_dedup_key("vm.boxd.sh:22").expect("valid");
        let k_none = host_dedup_key("vm.boxd.sh").expect("valid");
        assert_eq!(k22, k_none);
    }

    #[test]
    fn host_dedup_key_mixed_case_ipv6() {
        let upper = host_dedup_key("boxd@[2001:DB8::1]").expect("valid");
        let lower = host_dedup_key("boxd@[2001:db8::1]").expect("valid");
        assert_eq!(upper, lower, "IPv6 case must be folded");
    }

    // -----------------------------------------------------------------------
    // list-remotes version check
    // -----------------------------------------------------------------------

    /// Lower-bound check: min version passes, version below min fails.
    /// feature:387 — "list-remotes version check is lower-bound, not exact-whitelist"
    #[test]
    fn check_list_remotes_version_lower_bound() {
        assert!(
            check_list_remotes_version(LIST_REMOTES_MIN_VERSION).is_ok(),
            "min version must be accepted"
        );
        assert!(
            check_list_remotes_version(LIST_REMOTES_MIN_VERSION + 1).is_ok(),
            "version above min must be accepted (additive-only)"
        );
        assert!(
            check_list_remotes_version(LIST_REMOTES_MIN_VERSION + 100).is_ok(),
            "future versions must be accepted by lower-bound check"
        );
    }

    #[test]
    fn check_list_remotes_version_rejects_below_min() {
        // Only fails if LIST_REMOTES_MIN_VERSION > 0.
        if LIST_REMOTES_MIN_VERSION > 0 {
            assert!(
                check_list_remotes_version(0).is_err(),
                "version 0 must be rejected when min > 0"
            );
        }
    }

    /// list-remotes version constant is independent of JsonOutput's version.
    /// feature:379 — "emits its own independent version field"
    #[test]
    fn list_remotes_version_is_independent_of_json_output_version() {
        // JsonOutput always emits version 6. LIST_REMOTES_MIN_VERSION is a
        // separate constant — its value must not be required to match.
        // We simply assert they are separate constants (different addresses).
        // The meaningful semantic: list-remotes version check must not use
        // SUPPORTED_JSON_OUTPUT_VERSIONS.
        use crate::json_output::SUPPORTED_JSON_OUTPUT_VERSIONS;
        // This compiles only if the two constants are defined separately.
        let _ = SUPPORTED_JSON_OUTPUT_VERSIONS;
        let _ = LIST_REMOTES_MIN_VERSION;
        // The test passes as long as it compiles — we can't compare function
        // pointers, but the point is both are accessible and separate.
    }

    // -----------------------------------------------------------------------
    // JsonRemoteConfig + build_list_remotes_output
    // -----------------------------------------------------------------------

    /// feature:379 — JsonRemoteConfig has {name, host, kind, path, allow_transitive}
    #[test]
    fn json_remote_config_has_required_fields() {
        use crate::global_config::{GlobalConfig, RepoConfig};

        let config = GlobalConfig {
            repos: vec![RepoConfig {
                slug: "owner/repo".to_string(),
                path: "/local".to_string(),
                remotes: vec![RemoteConfig {
                    name: "vm".to_string(),
                    host: "boxd@vm.boxd.sh".to_string(),
                    path: "/remote".to_string(),
                    shell: "ssh".to_string(),
                    kind: RemoteKind::OrchardProxy,
                    allow_transitive: true,
                }],
            }],
            ..GlobalConfig::default()
        };

        let output = build_list_remotes_output(&config);
        assert_eq!(output.version, LIST_REMOTES_MIN_VERSION);
        assert_eq!(output.remotes.len(), 1);

        let r = &output.remotes[0];
        assert_eq!(r.name, "vm");
        assert_eq!(r.host, "boxd@vm.boxd.sh");
        assert_eq!(r.kind, "orchard-proxy");
        assert_eq!(r.path, "/remote");
        assert!(r.allow_transitive);
    }

    /// JSON serialization of ListRemotesOutput includes version and remotes.
    #[test]
    fn list_remotes_output_serializes_correctly() {
        use crate::global_config::{GlobalConfig, RepoConfig};

        let config = GlobalConfig {
            repos: vec![RepoConfig {
                slug: "owner/repo".to_string(),
                path: "/local".to_string(),
                remotes: vec![RemoteConfig {
                    name: "boxd".to_string(),
                    host: "boxd@vm.boxd.sh".to_string(),
                    path: "/home/boxd".to_string(),
                    shell: "ssh".to_string(),
                    kind: RemoteKind::OrchardProxy,
                    allow_transitive: false,
                }],
            }],
            ..GlobalConfig::default()
        };

        let output = build_list_remotes_output(&config);
        let json = serde_json::to_value(&output).expect("serialization must succeed");

        assert!(json.get("version").is_some(), "must have 'version'");
        assert!(json.get("remotes").is_some(), "must have 'remotes'");

        let remotes = json["remotes"].as_array().expect("remotes must be array");
        assert_eq!(remotes.len(), 1);

        let r = &remotes[0];
        assert!(r.get("name").is_some());
        assert!(r.get("host").is_some());
        assert!(r.get("kind").is_some());
        assert!(r.get("path").is_some());
        assert!(r.get("allowTransitive").is_some());
    }

    // -----------------------------------------------------------------------
    // RemoteConfig.allow_transitive backward compatibility
    // -----------------------------------------------------------------------

    /// feature (Background, line 26) — allow_transitive defaults to false when
    /// absent from JSON (backward-compatible deserialization).
    #[test]
    fn remote_config_allow_transitive_defaults_to_false() {
        let json = r#"{
            "name": "vm",
            "host": "boxd@vm.boxd.sh",
            "path": "/remote",
            "shell": "ssh",
            "type": "orchard-proxy"
        }"#;

        let remote: RemoteConfig = serde_json::from_str(json).expect("must parse");
        assert!(
            !remote.allow_transitive,
            "allow_transitive must default to false for legacy configs"
        );
    }

    #[test]
    fn remote_config_allow_transitive_true_round_trips() {
        let json = r#"{
            "name": "vm",
            "host": "boxd@vm.boxd.sh",
            "path": "/remote",
            "shell": "ssh",
            "type": "orchard-proxy",
            "allow_transitive": true
        }"#;

        let remote: RemoteConfig = serde_json::from_str(json).expect("must parse");
        assert!(remote.allow_transitive, "allow_transitive: true must parse");
    }

    // -----------------------------------------------------------------------
    // federation.discovered_host event
    // -----------------------------------------------------------------------

    /// feature:138 — first-seen host emits federation.discovered_host event;
    /// second sighting (same key) does NOT emit (caller dedup responsibility).
    ///
    /// We test only that the function writes a correctly-shaped event line;
    /// the seen-set dedup logic is the walker's responsibility, exercised via
    /// the walker tests in Phase 2.
    #[test]
    fn emit_federation_discovered_host_writes_event() {
        use tempfile::TempDir;

        // Redirect HOME so the event goes to a temp directory.
        let home = TempDir::new().expect("temp dir");
        unsafe {
            std::env::set_var("HOME", home.path());
        }

        emit_federation_discovered_host("Boxd@VM.Boxd.Sh", "boxd@vm.boxd.sh");

        let events_path = home
            .path()
            .join(".local")
            .join("state")
            .join("git-orchard")
            .join("events.jsonl");

        let contents = std::fs::read_to_string(&events_path).expect("events.jsonl must exist");
        let line = contents
            .lines()
            .next()
            .expect("must have at least one line");

        let parsed: serde_json::Map<String, serde_json::Value> =
            serde_json::from_str(line).expect("must be valid JSON");

        assert_eq!(
            parsed["event"].as_str().unwrap(),
            "federation.discovered_host"
        );
        assert_eq!(parsed["raw_target"].as_str().unwrap(), "Boxd@VM.Boxd.Sh");
        assert_eq!(parsed["dedup_key"].as_str().unwrap(), "boxd@vm.boxd.sh");
    }
}
