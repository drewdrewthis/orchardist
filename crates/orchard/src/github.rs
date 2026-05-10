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

/// Returns true if the digit run at `match_start..match_end` (in `s`) looks
/// like the year of an ISO date — e.g. `-2026-04-25-...` or `-2026-04` at end.
/// We skip these so date-shaped branch names (`audit/foo-2026-04-25`) don't
/// pin to a phantom issue 2026.
fn looks_like_year_in_date(s: &str, match_start: usize, match_end: usize) -> bool {
    if match_end - match_start != 4 {
        return false;
    }
    let n: u32 = match s[match_start..match_end].parse() {
        Ok(n) => n,
        Err(_) => return false,
    };
    if !(1900..=2099).contains(&n) {
        return false;
    }
    // Look for `-MM` or `-MM-DD...` immediately after the year.
    let after = &s[match_end..];
    let mut bytes = after.bytes();
    if bytes.next() != Some(b'-') {
        return false;
    }
    let d1 = bytes.next();
    let d2 = bytes.next();
    let (Some(d1), Some(d2)) = (d1, d2) else {
        return false;
    };
    if !d1.is_ascii_digit() || !d2.is_ascii_digit() {
        return false;
    }
    let month = (d1 - b'0') * 10 + (d2 - b'0');
    if !(1..=12).contains(&month) {
        return false;
    }
    // After `-MM`, accept end-of-string or another non-digit-running boundary.
    match bytes.next() {
        None => true,
        Some(b'-') => true,
        Some(c) if !c.is_ascii_digit() => true,
        _ => false,
    }
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

    // Leading number (>= 100) on stripped. Skip year-in-date matches.
    if let Some(caps) = leading_number_re().captures(&stripped)
        && let Some(m) = caps.get(1)
        && let Ok(n) = m.as_str().parse::<u32>()
        && n >= 100
        && !looks_like_year_in_date(&stripped, m.start(), m.end())
    {
        return Some(n);
    }

    // Embedded number (>= 100) on stripped. Skip year-in-date matches and
    // continue scanning so a real issue number after the date still wins.
    for caps in embedded_number_re().captures_iter(&stripped) {
        let Some(m) = caps.get(1) else { continue };
        let Ok(n) = m.as_str().parse::<u32>() else {
            continue;
        };
        if n < 100 {
            continue;
        }
        if looks_like_year_in_date(&stripped, m.start(), m.end()) {
            continue;
        }
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

    // Regression: branches like `audit/unimpl-suites-2026-04-25` used to
    // resolve to issue 2026 because the embedded-number regex matched the
    // year. See #379.
    #[test]
    fn ignores_iso_date_year_in_branch_name() {
        assert_eq!(extract_issue_number("audit/unimpl-suites-2026-04-25"), None);
    }

    #[test]
    fn ignores_iso_date_year_at_branch_end() {
        assert_eq!(extract_issue_number("snapshot/2026-04"), None);
    }

    #[test]
    fn keeps_real_issue_after_date_segment() {
        assert_eq!(
            extract_issue_number("audit/2026-04-25-followup-150"),
            Some(150)
        );
    }

    #[test]
    fn does_not_skip_non_year_four_digit_numbers() {
        assert_eq!(extract_issue_number("feat/some-3055-thing"), Some(3055));
    }

    #[test]
    fn skips_leading_year_with_month() {
        assert_eq!(extract_issue_number("2026-04-25-thing"), None);
    }
}
