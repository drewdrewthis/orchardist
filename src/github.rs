//! GitHub API client built on the `gh` CLI.
//!
//! Provides repo metadata, `gh` availability checks, and issue-number
//! extraction from branch names. All network I/O goes through `gh`
//! subprocesses rather than a direct HTTP client.
use std::process::Command;
use std::sync::OnceLock;

use anyhow::anyhow;
use regex::Regex;

// ---------------------------------------------------------------------------
// Repo cache
// ---------------------------------------------------------------------------

// We store a String-encoded error so the value is Sync.
static REPO_CACHE: OnceLock<Result<(String, String), String>> = OnceLock::new();

/// Returns `(owner, name)` for the current GitHub repository.
/// The result is cached after the first successful call.
pub fn get_repo() -> anyhow::Result<(String, String)> {
    let cached = REPO_CACHE.get_or_init(|| {
        let out = Command::new("gh")
            .args(["repo", "view", "--json", "owner,name"])
            .output()
            .map_err(|e| e.to_string())?;
        let v: serde_json::Value =
            serde_json::from_slice(&out.stdout).map_err(|e| e.to_string())?;
        let owner = v["owner"]["login"]
            .as_str()
            .ok_or_else(|| "missing owner.login".to_string())?
            .to_string();
        let name = v["name"]
            .as_str()
            .ok_or_else(|| "missing name".to_string())?
            .to_string();
        Ok((owner, name))
    });
    match cached {
        Ok(pair) => Ok(pair.clone()),
        Err(e) => Err(anyhow!("{}", e)),
    }
}

// ---------------------------------------------------------------------------
// gh availability
// ---------------------------------------------------------------------------

/// Reports whether the `gh` CLI is authenticated and available.
pub fn is_gh_available() -> bool {
    Command::new("gh")
        .args(["auth", "status"])
        .output()
        .map(|o| o.status.success())
        .unwrap_or(false)
}

// ---------------------------------------------------------------------------
// Issue number extraction
// ---------------------------------------------------------------------------

fn issue_keyword_re() -> &'static Regex {
    static RE: OnceLock<Regex> = OnceLock::new();
    RE.get_or_init(|| Regex::new(r"(?i)issue[/\-]?(\d+)").unwrap())
}

fn leading_number_re() -> &'static Regex {
    static RE: OnceLock<Regex> = OnceLock::new();
    RE.get_or_init(|| Regex::new(r"^(\d+)-").unwrap())
}

fn embedded_number_re() -> &'static Regex {
    static RE: OnceLock<Regex> = OnceLock::new();
    RE.get_or_init(|| Regex::new(r"-(\d+)(?:-|$)").unwrap())
}

fn strip_prefix_re() -> &'static Regex {
    static RE: OnceLock<Regex> = OnceLock::new();
    RE.get_or_init(|| Regex::new(r"^[a-zA-Z][a-zA-Z0-9]*[/_]").unwrap())
}

/// Attempts to extract a GitHub issue number from a branch name.
/// Strips common prefixes (e.g. `feat/`, `fix/`) before matching.
pub fn extract_issue_number(branch: &str) -> Option<u32> {
    let stripped = strip_prefix_re().replace(branch, "").into_owned();

    // Keyword pattern on original and stripped.
    for candidate in &[branch, stripped.as_str()] {
        if let Some(caps) = issue_keyword_re().captures(candidate)
            && let Ok(n) = caps[1].parse::<u32>()
            && n >= 1
        {
            return Some(n);
        }
    }

    // Leading number (>= 100) on stripped.
    if let Some(caps) = leading_number_re().captures(&stripped)
        && let Ok(n) = caps[1].parse::<u32>()
        && n >= 100
    {
        return Some(n);
    }

    // Embedded number (>= 100) on stripped.
    if let Some(caps) = embedded_number_re().captures(&stripped)
        && let Ok(n) = caps[1].parse::<u32>()
        && n >= 100
    {
        return Some(n);
    }

    None
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

#[cfg(test)]
mod tests {
    use super::*;

    // --- extract_issue_number ---

    #[test]
    fn extracts_issue_keyword_with_slash() {
        assert_eq!(extract_issue_number("issue/42"), Some(42));
    }

    #[test]
    fn extracts_issue_keyword_case_insensitive() {
        assert_eq!(extract_issue_number("feat/Issue-123-some-thing"), Some(123));
    }

    #[test]
    fn extracts_leading_number_above_100() {
        assert_eq!(extract_issue_number("feat/200-my-feature"), Some(200));
    }

    #[test]
    fn extracts_embedded_number_above_100() {
        assert_eq!(extract_issue_number("fix/something-150-desc"), Some(150));
    }

    #[test]
    fn returns_none_for_small_leading_number() {
        assert_eq!(extract_issue_number("feat/42-small"), None);
    }

    #[test]
    fn returns_none_for_plain_branch() {
        assert_eq!(extract_issue_number("main"), None);
    }
}
