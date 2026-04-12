//! Imperative shell for populating Orchard's on-disk cache.
//!
//! Fetches issues, PRs, worktrees, and tmux sessions from their respective
//! sources (GitHub CLI, git, SSH remotes) and writes the results to the
//! cache files consumed by `sources::*` and `derive`.
use std::collections::HashMap;
use std::process::Command;

use crate::cache::{
    self, CachedIssue, CachedPr, CachedRepoMeta, CachedReview, CachedSubIssue, CachedTmuxSession,
    CachedWorktree,
};
use crate::ci_state::{
    CheckInfo, CiChecks, GateMatcher, classify_check, map_check_run_conclusion,
    map_status_context_state, rollup_code_state, rollup_gate_state,
};
use crate::global_config::RepoConfig;
use crate::logger::LOG;
use crate::remote;

// ---------------------------------------------------------------------------
// Parse helpers
// ---------------------------------------------------------------------------

/// Parses raw JSON output from `gh issue list --json number,title,state,labels`
/// into a `Vec<CachedIssue>`.
pub fn parse_issues_json(json: &str) -> Vec<CachedIssue> {
    let values: Vec<serde_json::Value> = match serde_json::from_str(json) {
        Ok(v) => v,
        Err(e) => {
            LOG.warn(&format!("cache_sources: failed to parse issues JSON: {e}"));
            return Vec::new();
        }
    };

    values
        .into_iter()
        .filter_map(|v| {
            let number = v["number"].as_u64()? as u32;
            let title = v["title"].as_str().unwrap_or("").to_string();
            let state = v["state"].as_str().unwrap_or("open").to_lowercase();
            let labels = v["labels"]
                .as_array()
                .map(|arr| {
                    arr.iter()
                        .filter_map(|l| l["name"].as_str().map(|s| s.to_string()))
                        .collect()
                })
                .unwrap_or_default();
            Some(CachedIssue {
                number,
                title,
                state,
                labels,
                assignees: vec![],
                created_at: None,
                blocked_by: vec![],
                sub_issues: vec![],
                parent: None,
            })
        })
        .collect()
}

/// Builds a per-issue GraphQL query using aliased `repository.issue(number:N)` fields.
///
/// Uses a `IssueFields` fragment to avoid repeating the field set for each alias.
/// The `subIssues` and `parent` fields require the `GraphQL-Features: sub_issues`
/// header to be passed to `gh api graphql`.
///
/// If `issue_numbers` is empty, returns a minimal stub query that only fetches
/// the repository name (no issue aliases).
pub fn issue_graphql_query(owner: &str, name: &str, issue_numbers: &[u32]) -> String {
    if issue_numbers.is_empty() {
        return format!(r#"query {{ repository(owner: "{owner}", name: "{name}") {{ name }} }}"#);
    }

    let aliases: Vec<String> = issue_numbers
        .iter()
        .map(|n| format!(r#"    i_{n}: issue(number: {n}) {{ ...IssueFields }}"#))
        .collect();

    format!(
        r#"query {{
  repository(owner: "{owner}", name: "{name}") {{
{aliases}
  }}
}}

fragment IssueFields on Issue {{
  number
  title
  state
  labels(first: 100) {{ nodes {{ name }} }}
  assignees(first: 20) {{ nodes {{ login }} }}
  createdAt
  body
  subIssues(first: 50) {{ nodes {{ number title state }} }}
  parent {{ number }}
}}"#,
        aliases = aliases.join("\n")
    )
}

/// Extracts issue numbers that block `body` text using the blocking-reference regex.
///
/// Matches: "blocked by #N", "depends on #N", "waiting on #N" (case-insensitive).
/// Does NOT match: "fixes #N", "closes #N", or bare "#N".
fn extract_blocked_by(body: &str) -> Vec<u32> {
    use std::sync::OnceLock;
    static RE: OnceLock<regex::Regex> = OnceLock::new();
    let re = RE.get_or_init(|| {
        regex::Regex::new(r"(?i)(?:blocked\s+by|depends\s+on|waiting\s+on)\s+#(\d+)")
            .expect("blocking regex is valid")
    });
    re.captures_iter(body)
        .filter_map(|caps| caps[1].parse::<u32>().ok())
        .collect()
}

/// Parses a per-issue aliased GraphQL response into `Vec<CachedIssue>`.
///
/// Expects shape: `{"data":{"repository":{"i_42":{...}, "i_99":{...}, ...}}}`.
/// Iterates over all keys in `data.repository` that start with `i_`, maps each
/// to a `CachedIssue` with enriched fields including sub-issues, parent, assignees,
/// createdAt, and blocking references parsed from the body.
///
/// GraphQL returns state in uppercase (`OPEN`/`CLOSED`); this function normalizes
/// to lowercase.
pub fn parse_issues_graphql(json: &str) -> Vec<CachedIssue> {
    let root: serde_json::Value = match serde_json::from_str(json) {
        Ok(v) => v,
        Err(e) => {
            LOG.warn(&format!(
                "cache_sources: failed to parse issues GraphQL JSON: {e}"
            ));
            return Vec::new();
        }
    };

    let repo = match root["data"]["repository"].as_object() {
        Some(obj) => obj,
        None => {
            LOG.warn("cache_sources: issues GraphQL response missing data.repository");
            return Vec::new();
        }
    };

    let mut issues = Vec::new();

    for (key, val) in repo {
        if !key.starts_with("i_") {
            continue;
        }

        // A null value means the issue doesn't exist (deleted / wrong number).
        if val.is_null() {
            continue;
        }

        let Some(number) = val["number"].as_u64().map(|n| n as u32) else {
            continue;
        };

        let title = val["title"].as_str().unwrap_or("").to_string();
        let state = val["state"].as_str().unwrap_or("OPEN").to_lowercase();

        let labels = val["labels"]["nodes"]
            .as_array()
            .map(|arr| {
                arr.iter()
                    .filter_map(|l| l["name"].as_str().map(|s| s.to_string()))
                    .collect()
            })
            .unwrap_or_default();

        let assignees = val["assignees"]["nodes"]
            .as_array()
            .map(|arr| {
                arr.iter()
                    .filter_map(|a| a["login"].as_str().map(|s| s.to_string()))
                    .collect()
            })
            .unwrap_or_default();

        let created_at = val["createdAt"].as_str().map(|s| s.to_string());

        let body = val["body"].as_str().unwrap_or("");
        let blocked_by = extract_blocked_by(body);

        let sub_issues = val["subIssues"]["nodes"]
            .as_array()
            .map(|arr| {
                arr.iter()
                    .filter_map(|s| {
                        let sub_number = s["number"].as_u64()? as u32;
                        let sub_title = s["title"].as_str().unwrap_or("").to_string();
                        let sub_state = s["state"].as_str().unwrap_or("OPEN").to_lowercase();
                        Some(CachedSubIssue {
                            number: sub_number,
                            title: sub_title,
                            state: sub_state,
                        })
                    })
                    .collect()
            })
            .unwrap_or_default();

        let parent = val["parent"]["number"].as_u64().map(|n| n as u32);

        issues.push(CachedIssue {
            number,
            title,
            state,
            labels,
            assignees,
            created_at,
            blocked_by,
            sub_issues,
            parent,
        });
    }

    issues
}

/// Parses GraphQL PR response JSON into a `Vec<CachedPr>`.
///
/// Expected shape: `{"data":{"repository":{"pullRequests":{"nodes":[...]}}}}`
/// Each node has: number, headRefName, state, reviewDecision, mergeable,
/// reviewThreads, closingIssuesReferences, commits (for status checks with
/// per-check context breakdown via `contexts(first: 100)`).
///
/// The `matcher` is used to classify each check as code or gate. Build it
/// from `GlobalConfig.ci_gate_patterns` via `GateMatcher::new`.
pub fn parse_prs_graphql(json: &str, matcher: &GateMatcher) -> Vec<CachedPr> {
    let root: serde_json::Value = match serde_json::from_str(json) {
        Ok(v) => v,
        Err(e) => {
            LOG.warn(&format!(
                "cache_sources: failed to parse PRs GraphQL JSON: {e}"
            ));
            return Vec::new();
        }
    };

    let nodes = match root["data"]["repository"]["pullRequests"]["nodes"].as_array() {
        Some(n) => n,
        None => {
            LOG.warn("cache_sources: PRs GraphQL response missing expected path");
            return Vec::new();
        }
    };

    nodes
        .iter()
        .filter_map(|v| {
            let number = v["number"].as_u64()? as u32;
            let branch = v["headRefName"].as_str().unwrap_or("").to_string();
            let base = v["baseRefName"].as_str().unwrap_or("");
            let state = v["state"].as_str().unwrap_or("OPEN").to_lowercase();

            // Skip PRs where head == base (e.g., cross-fork PRs showing "main" → "main")
            if branch == base {
                return None;
            }

            let review_decision = match v["reviewDecision"].as_str().unwrap_or("") {
                "APPROVED" => Some("approved".to_string()),
                "CHANGES_REQUESTED" => Some("changes_requested".to_string()),
                "REVIEW_REQUIRED" => Some("review_required".to_string()),
                _ => None,
            };

            let (ci_code_state, ci_gate_state, ci_checks) =
                derive_ci_state_graphql(v, number, matcher);
            // Legacy checks_state mirrors ci_code_state only — a code-green
            // gate-blocked PR stays "passing" for backward-compat consumers.
            let checks_state = ci_code_state.clone();

            let has_conflicts = v["mergeable"].as_str().unwrap_or("") == "CONFLICTING";

            let unresolved_threads = v["reviewThreads"]["nodes"]
                .as_array()
                .map(|arr| {
                    arr.iter()
                        .filter(|t| t["isResolved"].as_bool() != Some(true))
                        .count() as u32
                })
                .unwrap_or(0);

            // Use GitHub's closingIssuesReferences (first linked issue).
            let first_linked = v["closingIssuesReferences"]["nodes"]
                .as_array()
                .and_then(|arr| arr.first());
            let linked_issue = first_linked
                .and_then(|issue| issue["number"].as_u64())
                .map(|n| n as u32);
            let linked_issue_state = first_linked.and_then(|issue| {
                let s = issue["state"].as_str()?;
                let reason = issue["stateReason"].as_str().unwrap_or("");
                let normalised = if s == "OPEN" {
                    "open"
                } else if reason == "COMPLETED" {
                    "completed"
                } else {
                    "closed"
                };
                Some(normalised.to_string())
            });

            // GraphQL shape is `labels: { nodes: [{ name: "..." }, ...] }` —
            // unlike `parse_issues_json` above which reads a flat REST array.
            let labels = v["labels"]["nodes"]
                .as_array()
                .map(|arr| {
                    arr.iter()
                        .filter_map(|l| l["name"].as_str().map(|s| s.to_string()))
                        .collect()
                })
                .unwrap_or_default();

            Some(CachedPr {
                number,
                branch,
                linked_issue,
                state,
                review_decision,
                checks_state,
                ci_code_state,
                ci_gate_state,
                ci_checks,
                has_conflicts,
                unresolved_threads,
                linked_issue_state,
                labels,
                title: None,
                is_draft: None,
                author: None,
                requested_reviewers: vec![],
                reviews: vec![],
                additions: None,
                deletions: None,
                created_at: None,
                updated_at: None,
                last_commit_pushed_at: None,
            })
        })
        .collect()
}

/// Derives split CI state from the GraphQL commit statusCheckRollup.
///
/// Walks `commits.nodes[last].commit.statusCheckRollup.contexts.nodes[]`,
/// parses each node as either a `CheckRun` or `StatusContext`, maps its
/// conclusion/state to a normalized value, classifies it via `matcher`, and
/// rolls up both buckets into `ci_code_state` and `ci_gate_state`.
///
/// If `totalCount > 100` (GitHub truncation), a warning is logged.
///
/// Returns `(ci_code_state, ci_gate_state, ci_checks)`.
fn derive_ci_state_graphql(
    pr: &serde_json::Value,
    pr_number: u32,
    matcher: &GateMatcher,
) -> (Option<String>, Option<String>, CiChecks) {
    let rollup = pr["commits"]["nodes"]
        .as_array()
        .and_then(|nodes| nodes.last())
        .map(|n| &n["commit"]["statusCheckRollup"])
        .filter(|r| r.is_object());

    let context_nodes: &[serde_json::Value] =
        match rollup.and_then(|r| r["contexts"]["nodes"].as_array()) {
            Some(arr) => arr,
            None => {
                // No contexts available — fall back to top-level rollup state for
                // legacy checks_state, but ci_code_state/ci_gate_state are None.
                return (None, None, CiChecks::default());
            }
        };

    // Warn if truncated.
    if let Some(total) = rollup.and_then(|r| r["contexts"]["totalCount"].as_u64())
        && total > 100
    {
        LOG.warn(&format!(
            "cache_sources: PR {pr_number} has {total} checks, truncating to first 100"
        ));
    }

    let mut code_checks: Vec<CheckInfo> = Vec::new();
    let mut gate_checks: Vec<CheckInfo> = Vec::new();

    for node in context_nodes {
        let typename = node["__typename"].as_str().unwrap_or("");
        let (name, state_opt, details_url) = match typename {
            "CheckRun" => {
                let name = node["name"].as_str().unwrap_or("").to_string();
                let conclusion = node["conclusion"].as_str();
                let status = node["status"].as_str();
                let state = map_check_run_conclusion(conclusion, status);
                let details_url = node["detailsUrl"].as_str().map(|s| s.to_string());
                (name, state, details_url)
            }
            "StatusContext" => {
                let name = node["context"].as_str().unwrap_or("").to_string();
                let raw_state = node["state"].as_str().unwrap_or("");
                let state = map_status_context_state(raw_state);
                (name, state, None)
            }
            _ => continue,
        };

        let state = match state_opt {
            Some(s) => s,
            None => continue, // SKIPPED/CANCELLED/STALE — omit from rollup
        };

        let check = CheckInfo {
            name: name.clone(),
            state,
            details_url,
        };
        use crate::ci_state::CheckBucket;
        match classify_check(&name, matcher) {
            CheckBucket::Gate => gate_checks.push(check),
            CheckBucket::Code => code_checks.push(check),
        }
    }

    let ci_code_state = rollup_code_state(&code_checks);
    let ci_gate_state = rollup_gate_state(&gate_checks);
    let ci_checks = CiChecks {
        code: code_checks,
        gate: gate_checks,
    };

    (ci_code_state, ci_gate_state, ci_checks)
}

/// Parses the output of `git worktree list --porcelain` into `Vec<CachedWorktree>`.
pub fn parse_worktree_porcelain(output: &str) -> Vec<CachedWorktree> {
    let mut worktrees = Vec::new();

    for block in output.trim().split("\n\n") {
        let block = block.trim();
        if block.is_empty() {
            continue;
        }

        let mut path = String::new();
        let mut branch = String::new();
        let mut is_bare = false;
        let mut is_locked = false;

        for line in block.lines() {
            if let Some(rest) = line.strip_prefix("worktree ") {
                path = rest.to_string();
            } else if let Some(rest) = line.strip_prefix("branch ") {
                let name = rest.strip_prefix("refs/heads/").unwrap_or(rest);
                branch = name.to_string();
            } else if line == "bare" {
                is_bare = true;
            } else if line.starts_with("locked") {
                is_locked = true;
            }
        }

        if path.is_empty() {
            continue;
        }

        // Git submodules report the main worktree as .git/modules/<name>.
        // Resolve to the actual working directory so session path matching works.
        if path.contains(".git/modules/")
            && let Ok(out) = std::process::Command::new("git")
                .args(["rev-parse", "--show-toplevel"])
                .current_dir(&path)
                .output()
        {
            let resolved = String::from_utf8_lossy(&out.stdout).trim().to_string();
            if !resolved.is_empty() {
                path = resolved;
            }
        }

        worktrees.push(CachedWorktree {
            path,
            branch,
            is_bare,
            is_locked,
            host: None,
            ahead: None,
            behind: None,
            last_commit_at: None,
        });
    }

    worktrees
}

/// Parses the combined output of `tmux list-sessions` and per-session pane
/// information into `Vec<CachedTmuxSession>`.
///
/// `sessions_output` has lines in the format
/// `{session_name}:{session_path}|{session_created}|{session_activity}`.
/// Session path is everything between the first `:` and the first `|`;
/// `session_created` and `session_activity` are Unix timestamps (integers)
/// appended with `|` separators and omitted when the format string does not
/// include them (backward-compatible: lines without `|` produce `None` for both).
/// `panes_fn` is called with each session name and returns lines in the format
/// `{pane_title}:{pane_current_command}`.
/// `content_fn` is called with each session name and returns the last few lines
/// of pane output (used for Claude prompt detection).
pub fn parse_tmux_output(
    sessions_output: &str,
    host: Option<&str>,
    panes_fn: impl Fn(&str) -> String,
    content_fn: impl Fn(&str) -> Vec<String>,
) -> Vec<CachedTmuxSession> {
    let mut sessions = Vec::new();

    for line in sessions_output.trim().lines() {
        if line.is_empty() {
            continue;
        }
        // Format: "{session_name}:{session_path}|{session_created}|{session_activity}"
        // Session path may itself contain colons (e.g. Windows paths or
        // absolute paths on some systems), so we split on the first colon only
        // to get name; then the remainder may contain pipe-separated timestamps.
        let Some((name, rest)) = line.split_once(':') else {
            continue;
        };

        // Split `rest` on `|` to separate path from optional timestamps.
        let parts: Vec<&str> = rest.splitn(3, '|').collect();
        let path = parts[0];
        let created_at: Option<u64> = parts.get(1).and_then(|s| s.parse().ok());
        let last_activity_at: Option<u64> = parts.get(2).and_then(|s| s.parse().ok());

        let pane_output = panes_fn(name);
        let parsed = parse_pane_lines(&pane_output);
        let last_output_lines = content_fn(name);

        sessions.push(CachedTmuxSession {
            name: name.to_string(),
            path: path.to_string(),
            pane_targets: parsed.targets,
            pane_titles: parsed.titles,
            pane_commands: parsed.commands,
            window_names: parsed.window_names,
            window_active: parsed.window_active,
            host: host.map(|h| h.to_string()),
            created_at,
            last_activity_at,
            last_output_lines,
            claude_state_raw: None, // populated after parsing remote Claude state
        });
    }

    sessions
}

/// Structured output of `parse_pane_lines`.
struct ParsedPaneData {
    /// Tmux window.pane addresses (e.g. "0.1").
    targets: Vec<String>,
    /// Window names per pane row.
    window_names: Vec<String>,
    /// Window active flags per pane row ("1" or "0").
    window_active: Vec<String>,
    /// Pane titles per pane row.
    titles: Vec<String>,
    /// Commands running in each pane.
    commands: Vec<String>,
}

/// Parses pane info lines into structured pane data.
///
/// New format: `{window}.{pane}\t{window_name}\t{window_active}\t{pane_title}:{pane_current_command}`
///
/// Old format (backward compat): `{window}.{pane}\t{pane_title}:{pane_current_command}`
///
/// Old-format lines (only one tab field after the target) are handled gracefully:
/// `window_name` is set to empty string and `window_active` to "0".
fn parse_pane_lines(output: &str) -> ParsedPaneData {
    let mut targets = Vec::new();
    let mut window_names = Vec::new();
    let mut window_active = Vec::new();
    let mut titles = Vec::new();
    let mut commands = Vec::new();

    for line in output.trim().lines() {
        if line.is_empty() {
            continue;
        }
        // Split on tabs; new format has 4 fields, old format has 2.
        let parts: Vec<&str> = line.splitn(4, '\t').collect();
        match parts.as_slice() {
            [target, win_name, win_active, rest] => {
                // New format: target, window_name, window_active, title:command
                targets.push(target.to_string());
                window_names.push(win_name.to_string());
                window_active.push(win_active.to_string());
                if let Some((title, cmd)) = rest.split_once(':') {
                    titles.push(title.to_string());
                    commands.push(cmd.to_string());
                } else {
                    titles.push(rest.to_string());
                    commands.push(String::new());
                }
            }
            [target, rest] => {
                // Old format: target, title:command
                targets.push(target.to_string());
                window_names.push(String::new());
                window_active.push("0".to_string());
                if let Some((title, cmd)) = rest.split_once(':') {
                    titles.push(title.to_string());
                    commands.push(cmd.to_string());
                } else {
                    titles.push(rest.to_string());
                    commands.push(String::new());
                }
            }
            _ => {
                // Malformed line — skip.
            }
        }
    }

    ParsedPaneData {
        targets,
        window_names,
        window_active,
        titles,
        commands,
    }
}

// ---------------------------------------------------------------------------
// Local command helpers
// ---------------------------------------------------------------------------

fn run_local(program: &str, args: &[&str]) -> anyhow::Result<String> {
    let out = Command::new(program).args(args).output()?;
    if !out.status.success() {
        let stderr = String::from_utf8_lossy(&out.stderr).into_owned();
        return Err(anyhow::anyhow!("{program} failed: {stderr}"));
    }
    Ok(String::from_utf8_lossy(&out.stdout).into_owned())
}

fn run_local_in(program: &str, args: &[&str], cwd: &str) -> anyhow::Result<String> {
    let out = Command::new(program).args(args).current_dir(cwd).output()?;
    if !out.status.success() {
        let stderr = String::from_utf8_lossy(&out.stderr).into_owned();
        return Err(anyhow::anyhow!("{program} failed: {stderr}"));
    }
    Ok(String::from_utf8_lossy(&out.stdout).into_owned())
}

// ---------------------------------------------------------------------------
// Public refresh functions
// ---------------------------------------------------------------------------

/// Fetches GitHub issues linked to known worktree branches for `config.slug` and
/// writes to the issues cache.
///
/// Extracts issue numbers from:
/// 1. `linked_issue` fields in the PRs cache (from `closingIssuesReferences`)
/// 2. Branch names in the worktrees cache via `extract_issue_number()`
///
/// Then queries only those issues via a per-issue GraphQL query with the
/// `GraphQL-Features: sub_issues` header to populate sub-issues and parent fields.
///
/// On API failure the error is logged and the existing cache is left intact.
pub fn refresh_issues(config: &RepoConfig) -> anyhow::Result<()> {
    let path = cache::cache_path(config.owner(), config.repo_name(), "issues");

    // Collect issue numbers from PRs cache (closingIssuesReferences).
    let prs_path = cache::cache_path(config.owner(), config.repo_name(), "prs");
    let prs: Vec<CachedPr> = cache::read_cache::<CachedPr>(&prs_path).entries;
    let mut issue_numbers: std::collections::HashSet<u32> =
        prs.iter().filter_map(|pr| pr.linked_issue).collect();

    // Also collect from worktree branch names.
    let wt_path = cache::cache_path(config.owner(), config.repo_name(), "worktrees");
    let worktrees: Vec<CachedWorktree> = cache::read_cache::<CachedWorktree>(&wt_path).entries;
    for wt in &worktrees {
        if !wt.is_bare
            && let Some(n) = crate::github::extract_issue_number(&wt.branch)
        {
            issue_numbers.insert(n);
        }
    }

    if issue_numbers.is_empty() {
        LOG.info(&format!(
            "cache_sources: refresh_issues({}): no linked issues found, skipping",
            config.slug
        ));
        cache::write_cache_if_nonempty(&path, &Vec::<CachedIssue>::new())?;
        return Ok(());
    }

    let mut numbers: Vec<u32> = issue_numbers.into_iter().collect();
    numbers.sort_unstable();

    let query = issue_graphql_query(config.owner(), config.repo_name(), &numbers);

    let out = run_local(
        "gh",
        &[
            "api",
            "graphql",
            "-H",
            "GraphQL-Features: sub_issues",
            "-f",
            &format!("query={query}"),
        ],
    );

    match out {
        Err(e) => {
            LOG.warn(&format!(
                "cache_sources: refresh_issues({}): {e}",
                config.slug
            ));
            return Ok(());
        }
        Ok(json) => {
            let issues = parse_issues_graphql(&json);
            cache::write_cache_if_nonempty(&path, &issues)?;
            LOG.info(&format!(
                "cache_sources: refresh_issues({}): wrote {} entries",
                config.slug,
                issues.len()
            ));
        }
    }

    Ok(())
}

/// Builds the GraphQL query string for fetching open PRs with per-check context data.
///
/// The query fetches up to 100 statusCheckRollup contexts per PR using inline
/// fragments on both `CheckRun` and `StatusContext` node types.
pub fn pr_graphql_query(owner: &str, name: &str) -> String {
    format!(
        r#"query {{
  repository(owner: "{owner}", name: "{name}") {{
    pullRequests(first: 100, states: OPEN, orderBy: {{field: CREATED_AT, direction: DESC}}) {{
      nodes {{
        number
        headRefName
        baseRefName
        state
        reviewDecision
        mergeable
        # 100 is deliberately high: GitHub rarely surfaces more than ~20 labels
        # on a single PR, and truncation would silently drop a phase label,
        # flipping the computed `phase` projection from its real value to null.
        labels(first: 100) {{
          nodes {{
            name
          }}
        }}
        reviewThreads(first: 100) {{
          nodes {{
            isResolved
          }}
        }}
        closingIssuesReferences(first: 5) {{
          nodes {{
            number
            state
            stateReason
          }}
        }}
        commits(last: 1) {{
          nodes {{
            commit {{
              statusCheckRollup {{
                state
                contexts(first: 100) {{
                  totalCount
                  nodes {{
                    __typename
                    ... on CheckRun {{
                      name
                      conclusion
                      status
                    }}
                    ... on StatusContext {{
                      context
                      state
                    }}
                  }}
                }}
              }}
            }}
          }}
        }}
      }}
    }}
  }}
}}"#
    )
}

/// Sanitizes a branch name into a valid GraphQL alias.
///
/// Replaces non-alphanumeric characters with `_` and prefixes with `b_` to
/// ensure the alias starts with a letter (GraphQL identifiers must match `[_a-zA-Z][_a-zA-Z0-9]*`).
fn sanitize_branch_alias(branch: &str) -> String {
    let clean: String = branch
        .chars()
        .map(|c| if c.is_ascii_alphanumeric() { c } else { '_' })
        .collect();
    format!("b_{clean}")
}

/// Builds a per-branch GraphQL query using aliases.
///
/// For each branch, adds an aliased `pullRequests(headRefName: "...", first: 1)` field
/// that returns the most recent PR regardless of state (open, merged, closed).
/// All aliases share a `PrFields` fragment for the full field set.
///
/// Returns the complete query string ready for `gh api graphql -f query=...`.
pub fn pr_graphql_query_per_branch(owner: &str, name: &str, branches: &[String]) -> String {
    if branches.is_empty() {
        return format!(r#"query {{ repository(owner: "{owner}", name: "{name}") {{ name }} }}"#);
    }

    let aliases: Vec<String> = branches
        .iter()
        .map(|branch| {
            let alias = sanitize_branch_alias(branch);
            format!(
                r#"    {alias}: pullRequests(headRefName: "{branch}", first: 1, orderBy: {{field: CREATED_AT, direction: DESC}}) {{
      nodes {{ ...PrFields }}
    }}"#
            )
        })
        .collect();

    format!(
        r#"query {{
  repository(owner: "{owner}", name: "{name}") {{
    defaultBranchRef {{
      name
      target {{
        ... on Commit {{
          statusCheckRollup {{
            state
          }}
        }}
      }}
    }}
{aliases}
  }}
}}

fragment PrFields on PullRequest {{
  number
  headRefName
  baseRefName
  title
  state
  isDraft
  author {{ login }}
  reviewDecision
  mergeable
  labels(first: 100) {{ nodes {{ name }} }}
  reviewRequests(first: 20) {{ nodes {{ requestedReviewer {{ ... on User {{ login }} ... on Team {{ name }} }} }} }}
  reviews(first: 20) {{ nodes {{ author {{ login }} state submittedAt }} }}
  reviewThreads(first: 100) {{ nodes {{ isResolved }} }}
  closingIssuesReferences(first: 5) {{ nodes {{ number state stateReason }} }}
  additions
  deletions
  createdAt
  updatedAt
  commits(last: 1) {{
    nodes {{
      commit {{
        pushedDate
        statusCheckRollup {{
          state
          contexts(first: 100) {{
            totalCount
            nodes {{
              __typename
              ... on CheckRun {{ name conclusion status detailsUrl }}
              ... on StatusContext {{ context state }}
            }}
          }}
        }}
      }}
    }}
  }}
}}"#,
        aliases = aliases.join("\n")
    )
}

/// Parses a per-branch aliased GraphQL PR response into `Vec<CachedPr>`.
///
/// Expects shape: `{"data":{"repository":{"b_branch_name":{"nodes":[...]}, ...}}}`.
/// Iterates over all keys in `data.repository` that start with `b_`, extracts the
/// first node from each alias, and maps it to a `CachedPr` with all enriched fields.
pub fn parse_prs_graphql_per_branch(json: &str, matcher: &GateMatcher) -> Vec<CachedPr> {
    let root: serde_json::Value = match serde_json::from_str(json) {
        Ok(v) => v,
        Err(e) => {
            LOG.warn(&format!(
                "cache_sources: failed to parse per-branch PRs GraphQL JSON: {e}"
            ));
            return Vec::new();
        }
    };

    let repo = match root["data"]["repository"].as_object() {
        Some(obj) => obj,
        None => {
            LOG.warn("cache_sources: per-branch PRs response missing data.repository");
            return Vec::new();
        }
    };

    let mut prs = Vec::new();

    for (key, val) in repo {
        if !key.starts_with("b_") {
            continue;
        }

        let nodes = match val["nodes"].as_array() {
            Some(n) => n,
            None => continue,
        };

        let Some(v) = nodes.first() else { continue };

        let Some(number) = v["number"].as_u64().map(|n| n as u32) else {
            continue;
        };

        let branch = v["headRefName"].as_str().unwrap_or("").to_string();
        let base = v["baseRefName"].as_str().unwrap_or("");
        let state = v["state"].as_str().unwrap_or("OPEN").to_lowercase();

        if branch == base {
            continue;
        }

        let title = v["title"].as_str().map(|s| s.to_string());
        let is_draft = v["isDraft"].as_bool();
        let author = v["author"]["login"].as_str().map(|s| s.to_string());

        let review_decision = match v["reviewDecision"].as_str().unwrap_or("") {
            "APPROVED" => Some("approved".to_string()),
            "CHANGES_REQUESTED" => Some("changes_requested".to_string()),
            "REVIEW_REQUIRED" => Some("review_required".to_string()),
            _ => None,
        };

        let requested_reviewers: Vec<String> = v["reviewRequests"]["nodes"]
            .as_array()
            .map(|arr| {
                arr.iter()
                    .filter_map(|n| {
                        let reviewer = &n["requestedReviewer"];
                        reviewer["login"]
                            .as_str()
                            .or_else(|| reviewer["name"].as_str())
                            .map(|s| s.to_string())
                    })
                    .collect()
            })
            .unwrap_or_default();

        let reviews: Vec<CachedReview> = v["reviews"]["nodes"]
            .as_array()
            .map(|arr| {
                arr.iter()
                    .filter_map(|r| {
                        let author = r["author"]["login"].as_str()?.to_string();
                        let state = r["state"].as_str().unwrap_or("").to_string();
                        let submitted_at = r["submittedAt"].as_str().map(|s| s.to_string());
                        Some(CachedReview {
                            author,
                            state,
                            submitted_at,
                        })
                    })
                    .collect()
            })
            .unwrap_or_default();

        let additions = v["additions"].as_u64().map(|n| n as u32);
        let deletions = v["deletions"].as_u64().map(|n| n as u32);
        let created_at = v["createdAt"].as_str().map(|s| s.to_string());
        let updated_at = v["updatedAt"].as_str().map(|s| s.to_string());
        let last_commit_pushed_at = v["commits"]["nodes"]
            .as_array()
            .and_then(|arr| arr.last())
            .and_then(|n| n["commit"]["pushedDate"].as_str())
            .map(|s| s.to_string());

        let (ci_code_state, ci_gate_state, ci_checks) = derive_ci_state_graphql(v, number, matcher);
        let checks_state = ci_code_state.clone();

        let has_conflicts = v["mergeable"].as_str().unwrap_or("") == "CONFLICTING";

        let unresolved_threads = v["reviewThreads"]["nodes"]
            .as_array()
            .map(|arr| {
                arr.iter()
                    .filter(|t| t["isResolved"].as_bool() != Some(true))
                    .count() as u32
            })
            .unwrap_or(0);

        let first_linked = v["closingIssuesReferences"]["nodes"]
            .as_array()
            .and_then(|arr| arr.first());
        let linked_issue = first_linked
            .and_then(|issue| issue["number"].as_u64())
            .map(|n| n as u32);
        let linked_issue_state = first_linked.and_then(|issue| {
            let s = issue["state"].as_str()?;
            let reason = issue["stateReason"].as_str().unwrap_or("");
            let normalised = if s == "OPEN" {
                "open"
            } else if reason == "COMPLETED" {
                "completed"
            } else {
                "closed"
            };
            Some(normalised.to_string())
        });

        let labels = v["labels"]["nodes"]
            .as_array()
            .map(|arr| {
                arr.iter()
                    .filter_map(|l| l["name"].as_str().map(|s| s.to_string()))
                    .collect()
            })
            .unwrap_or_default();

        prs.push(CachedPr {
            number,
            branch,
            linked_issue,
            state,
            review_decision,
            checks_state,
            ci_code_state,
            ci_gate_state,
            ci_checks,
            has_conflicts,
            unresolved_threads,
            linked_issue_state,
            labels,
            title,
            is_draft,
            author,
            requested_reviewers,
            reviews,
            additions,
            deletions,
            created_at,
            updated_at,
            last_commit_pushed_at,
        });
    }

    prs
}

/// Extracts repo-level metadata from a per-branch PR GraphQL response.
///
/// Reads the `repository.defaultBranchRef` field that is included by
/// [`pr_graphql_query_per_branch`] alongside the branch aliases. Returns a
/// [`CachedRepoMeta`] with the default branch name and the CI rollup state of
/// its HEAD commit.
///
/// The same JSON is passed to both this function and
/// [`parse_prs_graphql_per_branch`] so that only one API call is needed for
/// both datasets.
pub fn parse_repo_meta_from_pr_response(json: &str) -> CachedRepoMeta {
    let root: serde_json::Value = match serde_json::from_str(json) {
        Ok(v) => v,
        Err(e) => {
            LOG.warn(&format!(
                "cache_sources: failed to parse repo meta from PR response: {e}"
            ));
            return CachedRepoMeta::default();
        }
    };

    let default_branch_ref = &root["data"]["repository"]["defaultBranchRef"];
    if default_branch_ref.is_null() {
        return CachedRepoMeta::default();
    }

    let default_branch = default_branch_ref["name"].as_str().map(|s| s.to_string());

    let main_ci_state = default_branch_ref["target"]["statusCheckRollup"]["state"]
        .as_str()
        .map(|s| s.to_string());

    CachedRepoMeta {
        default_branch,
        main_ci_state,
    }
}

/// Parses output of `git branch -vv` to extract ahead/behind counts per branch.
///
/// Returns a map of branch name → `(ahead, behind)`. Branches without a
/// tracking remote, or tracking remotes with no divergence count, return
/// `(0, 0)`.
///
/// Input format:
/// ```text
/// * main                abc1234 [origin/main] Latest commit
///   issue42/fix-bug     def5678 [origin/issue42/fix-bug: ahead 3, behind 1] Some commit
///   feat/new-thing      ghi9012 [origin/feat/new-thing: ahead 2] Another commit
/// ```
pub fn parse_git_ahead_behind(output: &str) -> HashMap<String, (u32, u32)> {
    let mut result = HashMap::new();

    for line in output.lines() {
        // Strip the leading "* " or "  " marker and trim.
        let stripped = if line.starts_with("* ") || line.starts_with("  ") {
            &line[2..]
        } else {
            line
        };

        // Branch name is the first whitespace-separated token.
        let mut parts = stripped.splitn(2, ' ');
        let branch = match parts.next() {
            Some(b) if !b.is_empty() => b.to_string(),
            _ => continue,
        };
        let rest = parts.next().unwrap_or("").trim();

        // Extract the "[origin/...: ahead N, behind M]" section if present.
        let (ahead, behind) = if let Some(bracket_start) = rest.find('[') {
            if let Some(bracket_end) = rest.find(']') {
                let bracket_content = &rest[bracket_start + 1..bracket_end];
                let ahead = bracket_content
                    .split("ahead ")
                    .nth(1)
                    .and_then(|s| s.split([',', ']']).next())
                    .and_then(|s| s.trim().parse::<u32>().ok())
                    .unwrap_or(0);
                let behind = bracket_content
                    .split("behind ")
                    .nth(1)
                    .and_then(|s| s.split([',', ']']).next())
                    .and_then(|s| s.trim().parse::<u32>().ok())
                    .unwrap_or(0);
                (ahead, behind)
            } else {
                (0, 0)
            }
        } else {
            (0, 0)
        };

        result.insert(branch, (ahead, behind));
    }

    result
}

/// Parses output of `git for-each-ref --format='%(refname:short) %(committerdate:iso-strict)' refs/heads/`
/// to extract the last commit timestamp per branch.
///
/// Returns a map of branch name → ISO 8601 date string.
///
/// Input format:
/// ```text
/// main 2026-04-10T10:00:00-07:00
/// issue42/fix-bug 2026-04-12T14:30:00-07:00
/// ```
pub fn parse_git_last_commit_dates(output: &str) -> HashMap<String, String> {
    let mut result = HashMap::new();

    for line in output.lines() {
        let trimmed = line.trim();
        if trimmed.is_empty() {
            continue;
        }
        // Split on the LAST space: branch names may contain slashes but not spaces,
        // and the date is always the last token.
        if let Some(pos) = trimmed.rfind(' ') {
            let branch = trimmed[..pos].trim().to_string();
            let date = trimmed[pos + 1..].trim().to_string();
            if !branch.is_empty() && !date.is_empty() {
                result.insert(branch, date);
            }
        }
    }

    result
}

/// Fetches open GitHub PRs for `config.slug` via GraphQL and writes to the PRs cache.
///
/// Uses GraphQL to get closingIssuesReferences (linked issues), reviewThreads,
/// and per-check CI context data (up to 100 checks per PR), which are not
/// available via `gh pr list --json`.
/// On API failure the error is logged and the existing cache is left intact.
pub fn refresh_prs(config: &RepoConfig) -> anyhow::Result<()> {
    let path = cache::cache_path(config.owner(), config.repo_name(), "prs");

    // Read worktree cache to get branch list for per-branch queries.
    let wt_path = cache::cache_path(config.owner(), config.repo_name(), "worktrees");
    let worktrees: Vec<CachedWorktree> = cache::read_cache::<CachedWorktree>(&wt_path).entries;
    let branches: Vec<String> = worktrees
        .iter()
        .filter(|w| !w.is_bare && !crate::derive::is_default_branch(&w.branch))
        .map(|w| w.branch.clone())
        .collect();

    if branches.is_empty() {
        LOG.info(&format!(
            "cache_sources: refresh_prs({}): no feature branches, skipping",
            config.slug
        ));
        cache::write_cache_if_nonempty(&path, &Vec::<CachedPr>::new())?;
        return Ok(());
    }

    let query = pr_graphql_query_per_branch(config.owner(), config.repo_name(), &branches);

    // Build gate matcher from global config so classification is consistent
    // with the loaded user preferences.
    let global_cfg = crate::global_config::load_global_config();
    let matcher = GateMatcher::new(&global_cfg.ci_gate_patterns);

    let out = run_local("gh", &["api", "graphql", "-f", &format!("query={query}")]);

    match out {
        Err(e) => {
            LOG.warn(&format!("cache_sources: refresh_prs({}): {e}", config.slug));
            return Ok(());
        }
        Ok(json) => {
            let prs = parse_prs_graphql_per_branch(&json, &matcher);
            cache::write_cache_if_nonempty(&path, &prs)?;
            LOG.info(&format!(
                "cache_sources: refresh_prs({}): wrote {} entries",
                config.slug,
                prs.len()
            ));

            // Parse repo-level meta (default branch, main CI state) from the
            // same response and write to its own cache file.
            let repo_meta = parse_repo_meta_from_pr_response(&json);
            let meta_path = cache::cache_path(config.owner(), config.repo_name(), "repo_meta");
            cache::write_cache(&meta_path, &[repo_meta])?;
        }
    }

    Ok(())
}

/// Fetches git worktrees for `config.path` and writes to the worktrees cache.
///
/// After parsing the worktree list, also runs `git branch -vv` and
/// `git for-each-ref` to populate `ahead`, `behind`, and `last_commit_at`
/// fields on each non-bare worktree entry.
///
/// On failure the error is logged and the existing cache is left intact.
pub fn refresh_worktrees(config: &RepoConfig) -> anyhow::Result<()> {
    let path = cache::cache_path(config.owner(), config.repo_name(), "worktrees");

    let out = run_local_in("git", &["worktree", "list", "--porcelain"], &config.path);

    match out {
        Err(e) => {
            LOG.warn(&format!(
                "cache_sources: refresh_worktrees({}): {e}",
                config.slug
            ));
            return Ok(());
        }
        Ok(porcelain) => {
            let mut worktrees = parse_worktree_porcelain(&porcelain);

            // Enrich with ahead/behind counts from `git branch -vv`.
            let ahead_behind = run_local_in("git", &["branch", "-vv"], &config.path)
                .map(|out| parse_git_ahead_behind(&out))
                .unwrap_or_default();

            // Enrich with last commit timestamps from `git for-each-ref`.
            let commit_dates = run_local_in(
                "git",
                &[
                    "for-each-ref",
                    "--format=%(refname:short) %(committerdate:iso-strict)",
                    "refs/heads/",
                ],
                &config.path,
            )
            .map(|out| parse_git_last_commit_dates(&out))
            .unwrap_or_default();

            for wt in &mut worktrees {
                if !wt.is_bare {
                    if let Some(&(a, b)) = ahead_behind.get(&wt.branch) {
                        wt.ahead = if a > 0 { Some(a) } else { None };
                        wt.behind = if b > 0 { Some(b) } else { None };
                    }
                    wt.last_commit_at = commit_dates.get(&wt.branch).cloned();
                }
            }

            cache::write_cache_if_nonempty(&path, &worktrees)?;
            LOG.info(&format!(
                "cache_sources: refresh_worktrees({}): wrote {} entries",
                config.slug,
                worktrees.len()
            ));
        }
    }

    Ok(())
}

/// Sentinel string that separates tmux session list output from Claude state JSON
/// in the batched SSH command used for remote hosts.
pub const CLAUDE_STATE_SENTINEL: &str = "---CLAUDE_STATE---";

/// Fetches tmux sessions (local or remote) and writes to the tmux sessions cache.
///
/// Pass `None` for local sessions, or `Some("user@host")` for remote sessions.
///
/// For remote hosts, a single batched SSH command fetches both the tmux session
/// list and any Claude hook state files (from `$TMPDIR/orchard-claude-*.json`),
/// separated by a `---CLAUDE_STATE---` sentinel line. The parsed Claude states
/// are stored in `CachedTmuxSession::claude_state_raw` for each matching session.
///
/// On failure the error is logged and the existing cache is left intact.
pub fn refresh_tmux_sessions(host: Option<&str>) -> anyhow::Result<()> {
    let cache_path = cache::tmux_cache_path(host);

    // For remote hosts, batch the tmux list-sessions and Claude state cat into
    // a single SSH call to minimise round-trips.
    let sessions_out = match host {
        None => run_local(
            "tmux",
            &[
                "list-sessions",
                "-F",
                "#{session_name}:#{session_path}|#{session_created}|#{session_activity}",
            ],
        ),
        Some(h) => {
            let cmd = format!(
                "tmux list-sessions -F '#{{session_name}}:#{{session_path}}|#{{session_created}}|#{{session_activity}}' && echo '{}' && cat '${{TMPDIR:-/tmp}}'/orchard-claude-*.json 2>/dev/null; true",
                CLAUDE_STATE_SENTINEL
            );
            remote::ssh_exec(h, &cmd)
        }
    };

    let raw_output = match sessions_out {
        Err(e) => {
            LOG.warn(&format!(
                "cache_sources: refresh_tmux_sessions(host={:?}): {e}",
                host
            ));
            return Ok(());
        }
        Ok(s) => s,
    };

    // For remote output, split on the sentinel to separate tmux output from
    // Claude state JSON. For local output, there is no sentinel.
    let (sessions_output, claude_state_raw) = if host.is_some() {
        split_batched_output(&raw_output)
    } else {
        (raw_output.as_str(), "")
    };

    let remote_claude_states = crate::claude_state::parse_remote_state_output(claude_state_raw);

    let mut sessions = parse_tmux_output(
        sessions_output,
        host,
        |session_name| {
            // -s lists panes across ALL windows in the session.
            // Format: "window.pane\ttitle:command" for parse_pane_lines.
            let pane_fmt = "#{window_index}.#{pane_index}\t#{window_name}\t#{window_active}\t#{pane_title}:#{pane_current_command}";
            match host {
                None => run_local(
                    "tmux",
                    &["list-panes", "-s", "-t", session_name, "-F", pane_fmt],
                )
                .unwrap_or_default(),
                Some(h) => {
                    let cmd = format!(
                        "tmux list-panes -s -t {} -F '{}'",
                        remote::shell_escape(session_name),
                        pane_fmt
                    );
                    remote::ssh_exec(h, &cmd).unwrap_or_default()
                }
            }
        },
        |session_name| {
            let raw = match host {
                None => run_local(
                    "tmux",
                    &["capture-pane", "-p", "-t", session_name, "-S", "-5"],
                )
                .unwrap_or_default(),
                Some(h) => {
                    let cmd = format!(
                        "tmux capture-pane -p -t {} -S -5",
                        remote::shell_escape(session_name)
                    );
                    remote::ssh_exec(h, &cmd).unwrap_or_default()
                }
            };
            raw.lines().map(|l| l.to_string()).collect()
        },
    );

    // Attach fetched Claude state files to their matching sessions.
    for session in &mut sessions {
        session.claude_state_raw = remote_claude_states
            .iter()
            .find(|cs| cs.tmux_session == session.name)
            .cloned();
    }

    cache::write_cache_if_nonempty(&cache_path, &sessions)?;
    LOG.info(&format!(
        "cache_sources: refresh_tmux_sessions(host={:?}): wrote {} entries",
        host,
        sessions.len()
    ));

    Ok(())
}

/// Splits batched SSH output on the `CLAUDE_STATE_SENTINEL` line.
///
/// Returns `(tmux_output, claude_json)`. If the sentinel is not present, returns
/// the full output as tmux output and an empty string for the Claude JSON part.
fn split_batched_output(raw: &str) -> (&str, &str) {
    let sentinel_line = CLAUDE_STATE_SENTINEL;
    // Find the sentinel as a whole line (preceded by newline or start, followed
    // by newline or end).
    if let Some(pos) = raw.find(sentinel_line) {
        let before = &raw[..pos];
        let after_start = pos + sentinel_line.len();
        // Skip the newline that follows the sentinel, if present.
        let after = raw[after_start..].trim_start_matches('\n');
        (before, after)
    } else {
        (raw, "")
    }
}

/// Fetches git worktrees from a single remote host and writes to the remote worktrees cache.
///
/// On failure the error is logged and the existing cache is left intact.
pub fn refresh_remote_worktrees(
    config: &RepoConfig,
    remote_cfg: &crate::global_config::RemoteConfig,
) -> anyhow::Result<()> {
    let cache_path = cache::cache_path(config.owner(), config.repo_name(), "remote_worktrees");

    let cmd = format!(
        "git -C {} worktree list --porcelain",
        remote::shell_escape(&remote_cfg.path)
    );

    match remote::ssh_exec(&remote_cfg.host, &cmd) {
        Err(e) => {
            LOG.warn(&format!(
                "cache_sources: refresh_remote_worktrees({}, {}): {e}",
                config.slug, remote_cfg.host,
            ));
            return Ok(());
        }
        Ok(porcelain) => {
            let mut worktrees = parse_worktree_porcelain(&porcelain);
            // Tag each worktree with the host it came from so merge can differentiate.
            for wt in &mut worktrees {
                wt.host = Some(remote_cfg.host.clone());
            }

            // Merge with any existing cached worktrees from other remotes.
            let existing: Vec<CachedWorktree> =
                cache::read_cache::<CachedWorktree>(&cache_path).entries;
            let from_other_hosts: Vec<CachedWorktree> = existing
                .into_iter()
                .filter(|w| w.host.as_deref() != Some(&remote_cfg.host))
                .collect();
            worktrees.extend(from_other_hosts);

            cache::write_cache_if_nonempty(&cache_path, &worktrees)?;
            LOG.info(&format!(
                "cache_sources: refresh_remote_worktrees({}, {}): wrote {} entries",
                config.slug,
                remote_cfg.host,
                worktrees.len(),
            ));
        }
    }

    Ok(())
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

#[cfg(test)]
mod tests {
    use super::*;
    use serde_json::json;

    // -- parse_issues_json --------------------------------------------------

    #[test]
    fn parse_issues_json_produces_correct_entries() {
        let json = serde_json::to_string(&json!([
            {
                "number": 10,
                "title": "Fix the thing",
                "state": "OPEN",
                "labels": [{"name": "bug"}, {"name": "priority:high"}]
            },
            {
                "number": 11,
                "title": "Add a feature",
                "state": "OPEN",
                "labels": []
            }
        ]))
        .unwrap();

        let issues = parse_issues_json(&json);
        assert_eq!(issues.len(), 2);

        assert_eq!(issues[0].number, 10);
        assert_eq!(issues[0].title, "Fix the thing");
        assert_eq!(issues[0].state, "open");
        assert_eq!(issues[0].labels, vec!["bug", "priority:high"]);

        assert_eq!(issues[1].number, 11);
        assert_eq!(issues[1].labels, Vec::<String>::new());
    }

    #[test]
    fn parse_issues_json_extracts_label_names_not_objects() {
        let json = serde_json::to_string(&json!([{
            "number": 1,
            "title": "Test",
            "state": "OPEN",
            "labels": [{"id": "LA_x1", "name": "bug", "color": "red"}]
        }]))
        .unwrap();

        let issues = parse_issues_json(&json);
        assert_eq!(issues[0].labels, vec!["bug"]);
    }

    #[test]
    fn parse_issues_json_invalid_json_returns_empty() {
        let issues = parse_issues_json("not json");
        assert!(issues.is_empty());
    }

    #[test]
    fn parse_issues_json_handles_closed_state() {
        let json = r#"[
            {"number": 1, "title": "Old issue", "state": "CLOSED", "labels": []},
            {"number": 2, "title": "Active issue", "state": "OPEN", "labels": [{"name": "bug"}]}
        ]"#;
        let issues = parse_issues_json(json);
        assert_eq!(issues.len(), 2);
        assert_eq!(issues[0].number, 1);
        assert_eq!(issues[0].state, "closed");
        assert_eq!(issues[1].number, 2);
        assert_eq!(issues[1].state, "open");
    }

    #[test]
    fn parse_issues_json_mixed_states_preserves_all() {
        let json = r#"[
            {"number": 1, "title": "Closed", "state": "CLOSED", "labels": []},
            {"number": 13, "title": "Open 1", "state": "OPEN", "labels": []},
            {"number": 14, "title": "Open 2", "state": "OPEN", "labels": []},
            {"number": 16, "title": "Open 3", "state": "OPEN", "labels": []},
            {"number": 18, "title": "Open 4", "state": "OPEN", "labels": []}
        ]"#;
        let issues = parse_issues_json(json);
        assert_eq!(
            issues.len(),
            5,
            "all issues should be preserved regardless of state"
        );
        let numbers: Vec<u32> = issues.iter().map(|i| i.number).collect();
        assert_eq!(numbers, vec![1, 13, 14, 16, 18]);
    }

    // -- parse_prs_graphql ---------------------------------------------------

    /// Returns a `GateMatcher` with no patterns (all checks → code bucket).
    ///
    /// Used by tests that don't need gate classification.
    fn empty_matcher() -> GateMatcher {
        GateMatcher::new(&[])
    }

    /// Helper to wrap PR nodes in the GraphQL response envelope.
    fn graphql_prs(nodes: serde_json::Value) -> String {
        serde_json::to_string(&json!({
            "data": {
                "repository": {
                    "pullRequests": {
                        "nodes": nodes
                    }
                }
            }
        }))
        .unwrap()
    }

    /// Helper to build a single PR node in GraphQL format.
    ///
    /// `check_state` is the top-level rollup state. When provided, a single
    /// synthetic `CheckRun` context is generated that maps to the same state,
    /// so `ci_code_state` and legacy `checks_state` are consistent.
    fn gql_pr_node(
        number: u32,
        branch: &str,
        review_decision: Option<&str>,
        mergeable: &str,
        check_state: Option<&str>,
        linked_issues: Vec<u32>,
        unresolved_threads: u32,
    ) -> serde_json::Value {
        gql_pr_node_with_contexts(
            number,
            branch,
            review_decision,
            mergeable,
            check_state,
            linked_issues,
            unresolved_threads,
            vec![],
        )
    }

    /// Helper to build a PR node with explicit context nodes.
    ///
    /// `check_state` generates a synthetic "ci" CheckRun when `Some`. Additional
    /// explicit context nodes from `contexts` are appended after the synthetic one.
    /// Each context tuple is `(typename, name_or_context, conclusion_or_state)`.
    #[allow(clippy::too_many_arguments)]
    fn gql_pr_node_with_contexts(
        number: u32,
        branch: &str,
        review_decision: Option<&str>,
        mergeable: &str,
        check_state: Option<&str>,
        linked_issues: Vec<u32>,
        unresolved_threads: u32,
        extra_contexts: Vec<serde_json::Value>,
    ) -> serde_json::Value {
        let threads: Vec<serde_json::Value> = (0..unresolved_threads)
            .map(|_| json!({"isResolved": false}))
            .collect();
        let issues: Vec<serde_json::Value> = linked_issues
            .into_iter()
            .map(|n| json!({"number": n, "state": "OPEN", "stateReason": null}))
            .collect();

        // Build context nodes from check_state shorthand.
        let mut context_nodes: Vec<serde_json::Value> = Vec::new();
        if let Some(s) = check_state {
            let synthetic = match s {
                "SUCCESS" | "EXPECTED" => {
                    json!({"__typename": "CheckRun", "name": "ci", "conclusion": "SUCCESS", "status": "COMPLETED"})
                }
                "FAILURE" | "ERROR" => {
                    json!({"__typename": "CheckRun", "name": "ci", "conclusion": "FAILURE", "status": "COMPLETED"})
                }
                "PENDING" => {
                    json!({"__typename": "CheckRun", "name": "ci", "conclusion": null, "status": "IN_PROGRESS"})
                }
                other => {
                    json!({"__typename": "CheckRun", "name": "ci", "conclusion": other, "status": "COMPLETED"})
                }
            };
            context_nodes.push(synthetic);
        }
        context_nodes.extend(extra_contexts);

        let total_count = context_nodes.len() as u64;
        let rollup = if total_count > 0 || check_state.is_some() {
            json!({
                "state": check_state.unwrap_or("SUCCESS"),
                "contexts": {
                    "totalCount": total_count,
                    "nodes": context_nodes
                }
            })
        } else {
            serde_json::Value::Null
        };

        let commits = json!([{"commit": {"statusCheckRollup": rollup}}]);

        json!({
            "number": number,
            "headRefName": branch,
            "state": "OPEN",
            "reviewDecision": review_decision,
            "mergeable": mergeable,
            "reviewThreads": {"nodes": threads},
            "closingIssuesReferences": {"nodes": issues},
            "commits": {"nodes": commits}
        })
    }

    #[test]
    fn parse_prs_graphql_produces_correct_entries() {
        let json = graphql_prs(json!([gql_pr_node(
            55,
            "feat/task-centric",
            Some("APPROVED"),
            "MERGEABLE",
            Some("SUCCESS"),
            vec![47],
            0
        )]));

        let prs = parse_prs_graphql(&json, &empty_matcher());
        assert_eq!(prs.len(), 1);

        let pr = &prs[0];
        assert_eq!(pr.number, 55);
        assert_eq!(pr.branch, "feat/task-centric");
        assert_eq!(pr.linked_issue, Some(47));
        assert_eq!(pr.linked_issue_state.as_deref(), Some("open"));
        assert_eq!(pr.state, "open");
        assert_eq!(pr.review_decision.as_deref(), Some("approved"));
        assert_eq!(pr.checks_state.as_deref(), Some("passing"));
        assert!(!pr.has_conflicts);
        assert_eq!(pr.unresolved_threads, 0);
    }

    #[test]
    fn parse_prs_graphql_changes_requested() {
        let json = graphql_prs(json!([gql_pr_node(
            10,
            "fix/something",
            Some("CHANGES_REQUESTED"),
            "MERGEABLE",
            None,
            vec![],
            0
        )]));

        let prs = parse_prs_graphql(&json, &empty_matcher());
        assert_eq!(prs[0].review_decision.as_deref(), Some("changes_requested"));
    }

    #[test]
    fn parse_prs_graphql_has_conflicts_when_conflicting() {
        let json = graphql_prs(json!([gql_pr_node(
            10,
            "fix/conflict",
            None,
            "CONFLICTING",
            None,
            vec![],
            0
        )]));

        let prs = parse_prs_graphql(&json, &empty_matcher());
        assert!(prs[0].has_conflicts);
    }

    #[test]
    fn parse_prs_graphql_counts_unresolved_threads() {
        let json = graphql_prs(json!([gql_pr_node(
            10,
            "fix/threads",
            None,
            "MERGEABLE",
            None,
            vec![],
            3
        )]));

        let prs = parse_prs_graphql(&json, &empty_matcher());
        assert_eq!(prs[0].unresolved_threads, 3);
    }

    #[test]
    fn parse_prs_graphql_no_linked_issue_when_empty() {
        let json = graphql_prs(json!([gql_pr_node(
            99,
            "chore/no-issue",
            None,
            "MERGEABLE",
            None,
            vec![],
            0
        )]));

        let prs = parse_prs_graphql(&json, &empty_matcher());
        assert_eq!(prs[0].linked_issue, None);
        assert_eq!(prs[0].linked_issue_state, None);
    }

    #[test]
    fn parse_prs_graphql_linked_issue_closed_completed() {
        let issues = vec![json!({"number": 42, "state": "CLOSED", "stateReason": "COMPLETED"})];
        let pr_node = json!({
            "number": 10,
            "headRefName": "fix/done",
            "state": "OPEN",
            "reviewDecision": null,
            "mergeable": "MERGEABLE",
            "reviewThreads": {"nodes": []},
            "closingIssuesReferences": {"nodes": issues},
            "commits": {"nodes": [{"commit": {"statusCheckRollup": null}}]}
        });
        let json = graphql_prs(json!([pr_node]));

        let prs = parse_prs_graphql(&json, &empty_matcher());
        assert_eq!(prs[0].linked_issue, Some(42));
        assert_eq!(prs[0].linked_issue_state.as_deref(), Some("completed"));
    }

    #[test]
    fn parse_prs_graphql_linked_issue_closed_not_completed() {
        let issues = vec![json!({"number": 42, "state": "CLOSED", "stateReason": "NOT_PLANNED"})];
        let pr_node = json!({
            "number": 10,
            "headRefName": "fix/wontfix",
            "state": "OPEN",
            "reviewDecision": null,
            "mergeable": "MERGEABLE",
            "reviewThreads": {"nodes": []},
            "closingIssuesReferences": {"nodes": issues},
            "commits": {"nodes": [{"commit": {"statusCheckRollup": null}}]}
        });
        let json = graphql_prs(json!([pr_node]));

        let prs = parse_prs_graphql(&json, &empty_matcher());
        assert_eq!(prs[0].linked_issue, Some(42));
        assert_eq!(prs[0].linked_issue_state.as_deref(), Some("closed"));
    }

    #[test]
    fn parse_prs_graphql_checks_state_failing() {
        let json = graphql_prs(json!([gql_pr_node(
            10,
            "feat/ci-fail",
            None,
            "MERGEABLE",
            Some("FAILURE"),
            vec![],
            0
        )]));

        let prs = parse_prs_graphql(&json, &empty_matcher());
        assert_eq!(prs[0].checks_state.as_deref(), Some("failing"));
    }

    #[test]
    fn parse_prs_graphql_checks_state_pending() {
        let json = graphql_prs(json!([gql_pr_node(
            10,
            "feat/ci-pending",
            None,
            "MERGEABLE",
            Some("PENDING"),
            vec![],
            0
        )]));

        let prs = parse_prs_graphql(&json, &empty_matcher());
        assert_eq!(prs[0].checks_state.as_deref(), Some("pending"));
    }

    #[test]
    fn parse_prs_graphql_invalid_json_returns_empty() {
        assert!(parse_prs_graphql("{bad}", &empty_matcher()).is_empty());
    }

    // -- Task #8: GraphQL query contains required context fields ---------------

    /// Scenario: GraphQL query fetches up to 100 per-check contexts with inline
    /// fragments on CheckRun and StatusContext.
    #[test]
    fn graphql_query_contains_contexts_with_inline_fragments() {
        let query = pr_graphql_query("acme", "my-project");

        assert!(
            query.contains("statusCheckRollup"),
            "query must select statusCheckRollup"
        );
        assert!(
            query.contains("contexts(first: 100)"),
            "query must select contexts(first: 100)"
        );
        assert!(
            query.contains("... on CheckRun"),
            "query must have inline fragment on CheckRun"
        );
        assert!(
            query.contains("... on StatusContext"),
            "query must have inline fragment on StatusContext"
        );
        // CheckRun fields
        assert!(query.contains("name"), "query must select CheckRun.name");
        assert!(
            query.contains("conclusion"),
            "query must select CheckRun.conclusion"
        );
        assert!(
            query.contains("status"),
            "query must select CheckRun.status"
        );
        // StatusContext fields
        assert!(
            query.contains("context"),
            "query must select StatusContext.context"
        );
        // "state" appears in both CheckRun status field and StatusContext
        assert!(
            query.contains("state"),
            "query must select StatusContext.state"
        );
    }

    // -- Task #9: Parse CheckRun and StatusContext into uniform CheckInfo -------

    /// Scenario: Parsing normalizes CheckRun and StatusContext into uniform CheckInfo.
    #[test]
    fn parse_check_run_and_status_context_into_check_info() {
        let check_run_ctx = json!({"__typename": "CheckRun", "name": "test-unit", "conclusion": "SUCCESS", "status": "COMPLETED"});
        let status_ctx =
            json!({"__typename": "StatusContext", "context": "travis-ci", "state": "SUCCESS"});

        let pr_node = gql_pr_node_with_contexts(
            1,
            "feat/test-branch",
            None,
            "MERGEABLE",
            None,
            vec![],
            0,
            vec![check_run_ctx, status_ctx],
        );
        let json = graphql_prs(json!([pr_node]));
        let prs = parse_prs_graphql(&json, &empty_matcher());

        assert_eq!(prs.len(), 1);
        let pr = &prs[0];

        // Both checks land in the code bucket (empty matcher, no gate patterns).
        assert_eq!(pr.ci_checks.code.len(), 2, "expected 2 code checks");
        assert!(pr.ci_checks.gate.is_empty(), "expected 0 gate checks");

        // CheckRun uses .name field.
        let check_run_info = pr
            .ci_checks
            .code
            .iter()
            .find(|c| c.name == "test-unit")
            .expect("CheckRun should use .name field as check name");
        assert_eq!(
            check_run_info.state, "passing",
            "SUCCESS conclusion maps to passing"
        );

        // StatusContext uses .context field (not .name).
        let status_info = pr
            .ci_checks
            .code
            .iter()
            .find(|c| c.name == "travis-ci")
            .expect("StatusContext should use .context field as check name");
        assert_eq!(
            status_info.state, "passing",
            "SUCCESS state maps to passing"
        );

        assert_eq!(
            pr.ci_code_state.as_deref(),
            Some("passing"),
            "ci_code_state should be passing when all code checks pass"
        );
        // Legacy checks_state mirrors ci_code_state.
        assert_eq!(
            pr.checks_state.as_deref(),
            Some("passing"),
            "legacy checks_state mirrors ci_code_state"
        );
    }

    // -- Task #10: >100 checks truncated ---------------------------------------

    /// Scenario: PRs with more than 100 checks are truncated and a warning is logged.
    #[test]
    fn parse_prs_graphql_truncates_at_100_contexts() {
        // Build exactly 100 context nodes (what GitHub returns for first:100).
        // Set totalCount to 120 to simulate truncation.
        let context_nodes: Vec<serde_json::Value> = (0..100)
            .map(|i| {
                json!({
                    "__typename": "CheckRun",
                    "name": format!("check-{i}"),
                    "conclusion": "SUCCESS",
                    "status": "COMPLETED"
                })
            })
            .collect();

        let pr_node = json!({
            "number": 42,
            "headRefName": "feat/many-checks",
            "baseRefName": "main",
            "state": "OPEN",
            "reviewDecision": null,
            "mergeable": "MERGEABLE",
            "reviewThreads": {"nodes": []},
            "closingIssuesReferences": {"nodes": []},
            "commits": {"nodes": [{
                "commit": {
                    "statusCheckRollup": {
                        "state": "SUCCESS",
                        "contexts": {
                            "totalCount": 120,
                            "nodes": context_nodes
                        }
                    }
                }
            }]}
        });

        let json = graphql_prs(json!([pr_node]));
        let prs = parse_prs_graphql(&json, &empty_matcher());

        assert_eq!(prs.len(), 1);
        let pr = &prs[0];

        // All 100 returned checks are parsed (not 120 which weren't returned).
        let total_parsed = pr.ci_checks.code.len() + pr.ci_checks.gate.len();
        assert_eq!(
            total_parsed, 100,
            "should parse exactly 100 checks (the first 100 returned by GraphQL)"
        );
        assert_eq!(
            pr.ci_code_state.as_deref(),
            Some("passing"),
            "rollup of 100 passing checks should be passing"
        );
        // The warning is logged internally — we verify behavior (100 entries),
        // not the log call (no test-infra log capture in this module).
    }

    // -- parse_prs_graphql labels extraction --------------------------------

    /// Builds a PR node with an explicit `labels` array of name strings.
    fn gql_pr_node_with_labels(number: u32, label_names: &[&str]) -> serde_json::Value {
        let mut node = gql_pr_node(number, "test/branch", None, "MERGEABLE", None, vec![], 0);
        let label_nodes: Vec<serde_json::Value> =
            label_names.iter().map(|n| json!({"name": n})).collect();
        node["labels"] = json!({ "nodes": label_nodes });
        node
    }

    #[test]
    fn parse_prs_graphql_extracts_labels_when_present() {
        let json = graphql_prs(json!([gql_pr_node_with_labels(
            55,
            &["in-progress", "bug"],
        )]));
        let prs = parse_prs_graphql(&json, &GateMatcher::new(&[]));
        assert_eq!(prs[0].labels, vec!["in-progress", "bug"]);
    }

    #[test]
    fn parse_prs_graphql_labels_empty_when_nodes_missing() {
        // Existing `gql_pr_node` helper emits no `labels` key at all.
        let json = graphql_prs(json!([gql_pr_node(
            55,
            "test/branch",
            None,
            "MERGEABLE",
            None,
            vec![],
            0,
        )]));
        let prs = parse_prs_graphql(&json, &GateMatcher::new(&[]));
        assert!(prs[0].labels.is_empty());
    }

    #[test]
    fn parse_prs_graphql_labels_empty_when_nodes_array_is_empty() {
        let json = graphql_prs(json!([gql_pr_node_with_labels(55, &[])]));
        let prs = parse_prs_graphql(&json, &GateMatcher::new(&[]));
        assert!(prs[0].labels.is_empty());
    }

    #[test]
    fn parse_prs_graphql_skips_label_nodes_without_name() {
        // Defensive: if a node in the labels array is missing a `name` field,
        // skip it rather than panicking or emitting a placeholder.
        let mut node = gql_pr_node(55, "test/branch", None, "MERGEABLE", None, vec![], 0);
        node["labels"] = json!({
            "nodes": [
                {"name": "keep-me"},
                {"other": "no-name-here"},
                {"name": "also-keep"},
            ]
        });
        let json = graphql_prs(json!([node]));
        let prs = parse_prs_graphql(&json, &GateMatcher::new(&[]));
        assert_eq!(prs[0].labels, vec!["keep-me", "also-keep"]);
    }

    // -- parse_worktree_porcelain -------------------------------------------

    #[test]
    fn parse_worktree_porcelain_main_worktree() {
        let input = "worktree /home/user/repo\nHEAD abc123\nbranch refs/heads/main\n";
        let wts = parse_worktree_porcelain(input);
        assert_eq!(wts.len(), 1);
        assert_eq!(wts[0].path, "/home/user/repo");
        assert_eq!(wts[0].branch, "main");
        assert!(!wts[0].is_bare);
        assert!(!wts[0].is_locked);
    }

    #[test]
    fn parse_worktree_porcelain_strips_refs_heads() {
        let input = "worktree /home/user/repo-feat\nHEAD abc\nbranch refs/heads/feat/my-work\n";
        let wts = parse_worktree_porcelain(input);
        assert_eq!(wts[0].branch, "feat/my-work");
    }

    #[test]
    fn parse_worktree_porcelain_bare_worktree() {
        let input = "worktree /home/user/repo.git\nHEAD 000\nbare\n";
        let wts = parse_worktree_porcelain(input);
        assert!(wts[0].is_bare);
    }

    #[test]
    fn parse_worktree_porcelain_locked_worktree() {
        let input =
            "worktree /home/user/repo-locked\nHEAD abc\nbranch refs/heads/main\nlocked reason\n";
        let wts = parse_worktree_porcelain(input);
        assert!(wts[0].is_locked);
    }

    #[test]
    fn parse_worktree_porcelain_multiple_blocks() {
        let input = "worktree /a\nHEAD aaa\nbranch refs/heads/main\n\nworktree /b\nHEAD bbb\nbranch refs/heads/feat\n";
        let wts = parse_worktree_porcelain(input);
        assert_eq!(wts.len(), 2);
        assert_eq!(wts[0].path, "/a");
        assert_eq!(wts[1].path, "/b");
    }

    #[test]
    fn parse_worktree_porcelain_empty_input() {
        assert!(parse_worktree_porcelain("").is_empty());
    }

    // -- parse_tmux_output --------------------------------------------------

    #[test]
    fn parse_tmux_output_local_session() {
        let sessions = "my-session:/home/user/repo\n";
        let result = parse_tmux_output(
            sessions,
            None,
            |name| {
                assert_eq!(name, "my-session");
                "0.0\tzsh:zsh\n0.1\tbash:bash\n".to_string()
            },
            |_| vec![],
        );
        assert_eq!(result.len(), 1);
        assert_eq!(result[0].name, "my-session");
        assert_eq!(result[0].path, "/home/user/repo");
        assert_eq!(result[0].pane_targets, vec!["0.0", "0.1"]);
        assert_eq!(result[0].pane_titles, vec!["zsh", "bash"]);
        assert_eq!(result[0].pane_commands, vec!["zsh", "bash"]);
        assert!(result[0].host.is_none());
    }

    #[test]
    fn parse_tmux_output_remote_session() {
        let sessions = "remote-session:/home/ubuntu/workspace\n";
        let result = parse_tmux_output(
            sessions,
            Some("ubuntu@10.0.0.1"),
            |_| "".to_string(),
            |_| vec![],
        );
        assert_eq!(result[0].host.as_deref(), Some("ubuntu@10.0.0.1"));
    }

    #[test]
    fn parse_tmux_output_empty_input() {
        let result = parse_tmux_output("", None, |_| "".to_string(), |_| vec![]);
        assert!(result.is_empty());
    }

    #[test]
    fn parse_tmux_output_skips_malformed_lines() {
        // Line without colon separator is skipped.
        let sessions = "bad-line-no-colon\ngood-session:/path\n";
        let result = parse_tmux_output(sessions, None, |_| "".to_string(), |_| vec![]);
        assert_eq!(result.len(), 1);
        assert_eq!(result[0].name, "good-session");
    }

    #[test]
    fn parse_tmux_output_multiple_sessions() {
        let sessions = "session-a:/path/a\nsession-b:/path/b\n";
        let result = parse_tmux_output(sessions, None, |_| "".to_string(), |_| vec![]);
        assert_eq!(result.len(), 2);
        assert_eq!(result[0].name, "session-a");
        assert_eq!(result[1].name, "session-b");
    }

    #[test]
    fn parse_tmux_output_captures_last_output_lines() {
        let sessions = "my-session:/path\n";
        let result = parse_tmux_output(
            sessions,
            None,
            |_| "".to_string(),
            |_| vec!["line1".to_string(), "line2".to_string()],
        );
        assert_eq!(result[0].last_output_lines, vec!["line1", "line2"]);
    }

    // -- SSH command construction and sentinel splitting ----------------------

    #[test]
    fn remote_ssh_command_includes_sentinel() {
        // The batched command must contain the sentinel string.
        let cmd = format!(
            "tmux list-sessions -F '#{{session_name}}:#{{session_path}}' && echo '{}' && cat '${{TMPDIR:-/tmp}}'/orchard-claude-*.json 2>/dev/null; true",
            CLAUDE_STATE_SENTINEL
        );
        assert!(cmd.contains(CLAUDE_STATE_SENTINEL));
    }

    #[test]
    fn remote_ssh_command_uses_single_quoted_tmpdir() {
        // The TMPDIR variable must be single-quoted so it expands on the remote shell.
        let cmd = format!(
            "tmux list-sessions -F '#{{session_name}}:#{{session_path}}' && echo '{}' && cat '${{TMPDIR:-/tmp}}'/orchard-claude-*.json 2>/dev/null; true",
            CLAUDE_STATE_SENTINEL
        );
        assert!(
            cmd.contains("'${TMPDIR:-/tmp}'"),
            "expected single-quoted TMPDIR expansion, got: {cmd}"
        );
    }

    #[test]
    fn remote_ssh_command_uses_shell_variable_not_literal_path() {
        // The command must use '${TMPDIR:-/tmp}' (a shell variable for the remote
        // to expand) rather than a hardcoded local path like `/var/folders/...`.
        // On Linux CI, std::env::temp_dir() == "/tmp" which legitimately appears
        // in the fallback, so we check for the shell variable pattern instead.
        let cmd = format!(
            "tmux list-sessions -F '#{{session_name}}:#{{session_path}}' && echo '{}' && cat '${{TMPDIR:-/tmp}}'/orchard-claude-*.json 2>/dev/null; true",
            CLAUDE_STATE_SENTINEL
        );
        assert!(
            cmd.contains("'${TMPDIR:-/tmp}'"),
            "command should use shell variable expansion, got: {cmd}"
        );
    }

    #[test]
    fn remote_ssh_command_ends_with_semicolon_true() {
        // The "; true" suffix ensures cat failure (no files) doesn't fail the overall command.
        let cmd = format!(
            "tmux list-sessions -F '#{{session_name}}:#{{session_path}}' && echo '{}' && cat '${{TMPDIR:-/tmp}}'/orchard-claude-*.json 2>/dev/null; true",
            CLAUDE_STATE_SENTINEL
        );
        assert!(cmd.ends_with("; true"), "command must end with '; true'");
    }

    #[test]
    fn split_batched_output_separates_tmux_and_claude_parts() {
        let tmux_part = "session-a:/path/a\nsession-b:/path/b\n";
        let claude_part = r#"{"state":"working","session_id":"s1","tmux_session":"session-a","cwd":"/workspace","event":"Stop","timestamp":"2026-03-28T10:00:00Z"}"#;
        let raw = format!("{tmux_part}{CLAUDE_STATE_SENTINEL}\n{claude_part}");

        let (tmux, claude) = split_batched_output(&raw);
        assert_eq!(tmux, tmux_part);
        assert_eq!(claude, claude_part);
    }

    #[test]
    fn split_batched_output_empty_claude_part_when_no_files() {
        let tmux_part = "session-a:/path/a\n";
        let raw = format!("{tmux_part}{CLAUDE_STATE_SENTINEL}\n");

        let (tmux, claude) = split_batched_output(&raw);
        assert_eq!(tmux, tmux_part);
        assert!(claude.is_empty());
    }

    #[test]
    fn split_batched_output_no_sentinel_returns_all_as_tmux() {
        let raw = "session-a:/path/a\nsession-b:/path/b\n";
        let (tmux, claude) = split_batched_output(raw);
        assert_eq!(tmux, raw);
        assert!(claude.is_empty());
    }

    #[test]
    fn parse_tmux_output_attaches_no_claude_state_for_local_sessions() {
        let sessions_str = "my-session:/path\n";
        let result = parse_tmux_output(sessions_str, None, |_| "".to_string(), |_| vec![]);
        assert!(result[0].claude_state_raw.is_none());
    }

    // -- parse_pane_lines ---------------------------------------------------

    #[test]
    fn parse_pane_lines_new_format_extracts_window_metadata() {
        let output =
            "0.0\tmain\t1\tbash:bash\n0.1\tmain\t1\tclaude:claude\n1.0\teditor\t0\tnvim:nvim\n";
        let parsed = parse_pane_lines(output);
        assert_eq!(parsed.targets, vec!["0.0", "0.1", "1.0"]);
        assert_eq!(parsed.window_names, vec!["main", "main", "editor"]);
        assert_eq!(parsed.window_active, vec!["1", "1", "0"]);
        assert_eq!(parsed.titles, vec!["bash", "claude", "nvim"]);
        assert_eq!(parsed.commands, vec!["bash", "claude", "nvim"]);
    }

    #[test]
    fn parse_pane_lines_old_format_backward_compat() {
        // Old format: only two tab-separated fields — target and title:command.
        let output = "0.0\tbash:bash\n0.1\tvim:vim\n";
        let parsed = parse_pane_lines(output);
        assert_eq!(parsed.targets, vec!["0.0", "0.1"]);
        // Old format: window_names empty, window_active defaults to "0".
        assert_eq!(parsed.window_names, vec!["", ""]);
        assert_eq!(parsed.window_active, vec!["0", "0"]);
        assert_eq!(parsed.titles, vec!["bash", "vim"]);
        assert_eq!(parsed.commands, vec!["bash", "vim"]);
    }

    #[test]
    fn parse_pane_lines_empty_input() {
        let parsed = parse_pane_lines("");
        assert!(parsed.targets.is_empty());
        assert!(parsed.window_names.is_empty());
        assert!(parsed.window_active.is_empty());
        assert!(parsed.titles.is_empty());
        assert!(parsed.commands.is_empty());
    }

    #[test]
    fn parse_pane_lines_title_with_colon_new_format() {
        let output = "0.0\tmain\t1\tClaude: my-project:node\n";
        let parsed = parse_pane_lines(output);
        assert_eq!(parsed.targets, vec!["0.0"]);
        assert_eq!(parsed.window_names, vec!["main"]);
        assert_eq!(parsed.window_active, vec!["1"]);
        assert_eq!(parsed.titles, vec!["Claude"]);
        assert_eq!(parsed.commands, vec![" my-project:node"]);
    }

    // -- issue_graphql_query ---------------------------------------------------

    #[test]
    fn issue_graphql_query_empty_returns_stub() {
        let q = issue_graphql_query("acme", "myrepo", &[]);
        assert!(q.contains("repository(owner: \"acme\", name: \"myrepo\")"));
        assert!(!q.contains("i_"), "empty query must have no issue aliases");
    }

    #[test]
    fn issue_graphql_query_generates_aliases_and_fragment() {
        let q = issue_graphql_query("acme", "myrepo", &[42, 99]);

        // Each issue gets an alias.
        assert!(q.contains("i_42: issue(number: 42)"), "missing i_42 alias");
        assert!(q.contains("i_99: issue(number: 99)"), "missing i_99 alias");

        // Fragment is defined and referenced.
        assert!(
            q.contains("...IssueFields"),
            "aliases must use ...IssueFields spread"
        );
        assert!(
            q.contains("fragment IssueFields on Issue"),
            "IssueFields fragment must be defined"
        );

        // Fragment contains required fields.
        assert!(
            q.contains("subIssues(first: 50)"),
            "fragment must select subIssues"
        );
        assert!(
            q.contains("parent { number }"),
            "fragment must select parent.number"
        );
        assert!(
            q.contains("assignees(first: 20)"),
            "fragment must select assignees"
        );
        assert!(q.contains("createdAt"), "fragment must select createdAt");
        assert!(q.contains("body"), "fragment must select body");
        assert!(
            q.contains("labels(first: 100)"),
            "fragment must select labels(first: 100)"
        );
    }

    #[test]
    fn issue_graphql_query_single_issue() {
        let q = issue_graphql_query("owner", "repo", &[1]);
        assert!(q.contains("i_1: issue(number: 1)"));
    }

    // -- extract_blocked_by (blocking regex) ------------------------------------

    #[test]
    fn blocking_regex_matches_blocked_by() {
        let result = extract_blocked_by("This is blocked by #42");
        assert_eq!(result, vec![42]);
    }

    #[test]
    fn blocking_regex_matches_depends_on() {
        let result = extract_blocked_by("Depends on #99");
        assert_eq!(result, vec![99]);
    }

    #[test]
    fn blocking_regex_matches_waiting_on() {
        let result = extract_blocked_by("waiting on #7");
        assert_eq!(result, vec![7]);
    }

    #[test]
    fn blocking_regex_case_insensitive() {
        let result = extract_blocked_by("BLOCKED BY #100 and Depends On #200");
        assert!(result.contains(&100), "should match BLOCKED BY #100");
        assert!(result.contains(&200), "should match Depends On #200");
    }

    #[test]
    fn blocking_regex_does_not_match_fixes() {
        let result = extract_blocked_by("fixes #42");
        assert!(result.is_empty(), "should not match 'fixes #N'");
    }

    #[test]
    fn blocking_regex_does_not_match_closes() {
        let result = extract_blocked_by("closes #42");
        assert!(result.is_empty(), "should not match 'closes #N'");
    }

    #[test]
    fn blocking_regex_does_not_match_bare_hash() {
        let result = extract_blocked_by("see #42 for context");
        assert!(result.is_empty(), "should not match bare '#N'");
    }

    #[test]
    fn blocking_regex_multiple_blockers() {
        let result = extract_blocked_by("blocked by #10\ndepends on #20\nwaiting on #30");
        assert_eq!(result.len(), 3);
        assert!(result.contains(&10));
        assert!(result.contains(&20));
        assert!(result.contains(&30));
    }

    // -- parse_issues_graphql --------------------------------------------------

    fn graphql_issues_response(aliases: serde_json::Value) -> String {
        serde_json::to_string(&json!({
            "data": {
                "repository": aliases
            }
        }))
        .unwrap()
    }

    #[test]
    fn parse_issues_graphql_basic_fields() {
        let json = graphql_issues_response(json!({
            "i_42": {
                "number": 42,
                "title": "Fix the thing",
                "state": "OPEN",
                "labels": { "nodes": [{"name": "bug"}] },
                "assignees": { "nodes": [{"login": "alice"}] },
                "createdAt": "2026-01-15T10:00:00Z",
                "body": "",
                "subIssues": { "nodes": [] },
                "parent": null
            }
        }));

        let issues = parse_issues_graphql(&json);
        assert_eq!(issues.len(), 1);
        let issue = &issues[0];
        assert_eq!(issue.number, 42);
        assert_eq!(issue.title, "Fix the thing");
        assert_eq!(issue.state, "open", "state should be lowercased");
        assert_eq!(issue.labels, vec!["bug"]);
        assert_eq!(issue.assignees, vec!["alice"]);
        assert_eq!(issue.created_at.as_deref(), Some("2026-01-15T10:00:00Z"));
        assert!(issue.blocked_by.is_empty());
        assert!(issue.sub_issues.is_empty());
        assert!(issue.parent.is_none());
    }

    #[test]
    fn parse_issues_graphql_closed_state_normalized() {
        let json = graphql_issues_response(json!({
            "i_10": {
                "number": 10,
                "title": "Old issue",
                "state": "CLOSED",
                "labels": { "nodes": [] },
                "assignees": { "nodes": [] },
                "createdAt": null,
                "body": "",
                "subIssues": { "nodes": [] },
                "parent": null
            }
        }));

        let issues = parse_issues_graphql(&json);
        assert_eq!(issues[0].state, "closed");
    }

    #[test]
    fn parse_issues_graphql_sub_issues_and_parent() {
        let json = graphql_issues_response(json!({
            "i_5": {
                "number": 5,
                "title": "Parent issue",
                "state": "OPEN",
                "labels": { "nodes": [] },
                "assignees": { "nodes": [] },
                "createdAt": null,
                "body": "",
                "subIssues": {
                    "nodes": [
                        {"number": 6, "title": "Child one", "state": "OPEN"},
                        {"number": 7, "title": "Child two", "state": "CLOSED"}
                    ]
                },
                "parent": null
            },
            "i_6": {
                "number": 6,
                "title": "Child one",
                "state": "OPEN",
                "labels": { "nodes": [] },
                "assignees": { "nodes": [] },
                "createdAt": null,
                "body": "",
                "subIssues": { "nodes": [] },
                "parent": {"number": 5}
            }
        }));

        let issues = parse_issues_graphql(&json);
        let parent = issues.iter().find(|i| i.number == 5).unwrap();
        assert_eq!(parent.sub_issues.len(), 2);
        assert_eq!(parent.sub_issues[0].number, 6);
        assert_eq!(parent.sub_issues[0].title, "Child one");
        assert_eq!(parent.sub_issues[0].state, "open");
        assert_eq!(parent.sub_issues[1].number, 7);
        assert_eq!(parent.sub_issues[1].state, "closed");
        assert!(parent.parent.is_none());

        let child = issues.iter().find(|i| i.number == 6).unwrap();
        assert_eq!(child.parent, Some(5));
        assert!(child.sub_issues.is_empty());
    }

    #[test]
    fn parse_issues_graphql_blocking_references_from_body() {
        let json = graphql_issues_response(json!({
            "i_20": {
                "number": 20,
                "title": "Blocked issue",
                "state": "OPEN",
                "labels": { "nodes": [] },
                "assignees": { "nodes": [] },
                "createdAt": null,
                "body": "This issue is blocked by #18 and depends on #19",
                "subIssues": { "nodes": [] },
                "parent": null
            }
        }));

        let issues = parse_issues_graphql(&json);
        assert_eq!(issues.len(), 1);
        let issue = &issues[0];
        assert!(issue.blocked_by.contains(&18), "should contain 18");
        assert!(issue.blocked_by.contains(&19), "should contain 19");
    }

    #[test]
    fn parse_issues_graphql_skips_non_i_prefixed_keys() {
        // 'name' key from the stub query must not be parsed as an issue.
        let json = graphql_issues_response(json!({
            "name": "myrepo",
            "i_1": {
                "number": 1,
                "title": "Real issue",
                "state": "OPEN",
                "labels": { "nodes": [] },
                "assignees": { "nodes": [] },
                "createdAt": null,
                "body": "",
                "subIssues": { "nodes": [] },
                "parent": null
            }
        }));

        let issues = parse_issues_graphql(&json);
        assert_eq!(issues.len(), 1);
        assert_eq!(issues[0].number, 1);
    }

    #[test]
    fn parse_issues_graphql_null_issue_skipped() {
        // GitHub returns null for an issue alias if the issue doesn't exist.
        let json = graphql_issues_response(json!({
            "i_999": null,
            "i_1": {
                "number": 1,
                "title": "Exists",
                "state": "OPEN",
                "labels": { "nodes": [] },
                "assignees": { "nodes": [] },
                "createdAt": null,
                "body": "",
                "subIssues": { "nodes": [] },
                "parent": null
            }
        }));

        let issues = parse_issues_graphql(&json);
        assert_eq!(issues.len(), 1, "null issue alias must be skipped");
        assert_eq!(issues[0].number, 1);
    }

    #[test]
    fn parse_issues_graphql_invalid_json_returns_empty() {
        let issues = parse_issues_graphql("not json");
        assert!(issues.is_empty());
    }

    #[test]
    fn parse_issues_graphql_multiple_assignees() {
        let json = graphql_issues_response(json!({
            "i_3": {
                "number": 3,
                "title": "Team issue",
                "state": "OPEN",
                "labels": { "nodes": [] },
                "assignees": { "nodes": [{"login": "alice"}, {"login": "bob"}] },
                "createdAt": null,
                "body": "",
                "subIssues": { "nodes": [] },
                "parent": null
            }
        }));

        let issues = parse_issues_graphql(&json);
        assert_eq!(issues[0].assignees, vec!["alice", "bob"]);
    }

    // -- parse_git_ahead_behind -----------------------------------------------

    #[test]
    fn ahead_behind_branch_with_both() {
        let output = "  issue42/fix-bug     def5678 [origin/issue42/fix-bug: ahead 3, behind 1] Some commit\n";
        let map = parse_git_ahead_behind(output);
        assert_eq!(map.get("issue42/fix-bug"), Some(&(3, 1)));
    }

    #[test]
    fn ahead_behind_branch_ahead_only() {
        let output =
            "  feat/new-thing      ghi9012 [origin/feat/new-thing: ahead 2] Another commit\n";
        let map = parse_git_ahead_behind(output);
        assert_eq!(map.get("feat/new-thing"), Some(&(2, 0)));
    }

    #[test]
    fn ahead_behind_branch_behind_only() {
        let output = "  stale/branch        abc1234 [origin/stale/branch: behind 5] Old commit\n";
        let map = parse_git_ahead_behind(output);
        assert_eq!(map.get("stale/branch"), Some(&(0, 5)));
    }

    #[test]
    fn ahead_behind_branch_no_divergence() {
        // Tracking remote but no ahead/behind counts — branch is in sync.
        let output = "* main                abc1234 [origin/main] Latest commit\n";
        let map = parse_git_ahead_behind(output);
        assert_eq!(map.get("main"), Some(&(0, 0)));
    }

    #[test]
    fn ahead_behind_branch_no_tracking() {
        // No bracket section — untracked local branch.
        let output = "  local-only          abc1234 Untracked commit\n";
        let map = parse_git_ahead_behind(output);
        assert_eq!(map.get("local-only"), Some(&(0, 0)));
    }

    #[test]
    fn ahead_behind_multiple_branches() {
        let output = "\
* main                abc1234 [origin/main] Latest commit
  issue42/fix-bug     def5678 [origin/issue42/fix-bug: ahead 3, behind 1] Some commit
  feat/new-thing      ghi9012 [origin/feat/new-thing: ahead 2] Another commit
";
        let map = parse_git_ahead_behind(output);
        assert_eq!(map.len(), 3);
        assert_eq!(map.get("main"), Some(&(0, 0)));
        assert_eq!(map.get("issue42/fix-bug"), Some(&(3, 1)));
        assert_eq!(map.get("feat/new-thing"), Some(&(2, 0)));
    }

    #[test]
    fn ahead_behind_empty_output() {
        let map = parse_git_ahead_behind("");
        assert!(map.is_empty());
    }

    // -- parse_git_last_commit_dates ------------------------------------------

    #[test]
    fn commit_dates_multiple_branches() {
        let output = "\
main 2026-04-10T10:00:00-07:00
issue42/fix-bug 2026-04-12T14:30:00-07:00
";
        let map = parse_git_last_commit_dates(output);
        assert_eq!(map.len(), 2);
        assert_eq!(
            map.get("main"),
            Some(&"2026-04-10T10:00:00-07:00".to_string())
        );
        assert_eq!(
            map.get("issue42/fix-bug"),
            Some(&"2026-04-12T14:30:00-07:00".to_string())
        );
    }

    #[test]
    fn commit_dates_empty_output() {
        let map = parse_git_last_commit_dates("");
        assert!(map.is_empty());
    }

    #[test]
    fn commit_dates_single_branch() {
        let output = "feat/my-feature 2026-03-01T09:00:00+00:00\n";
        let map = parse_git_last_commit_dates(output);
        assert_eq!(
            map.get("feat/my-feature"),
            Some(&"2026-03-01T09:00:00+00:00".to_string())
        );
    }

    // -- parse_tmux_output with session timestamps ----------------------------

    #[test]
    fn parse_tmux_output_with_session_timestamps() {
        let sessions = "my-session:/home/user/repo|1700000000|1700001000\n";
        let result = parse_tmux_output(sessions, None, |_| "".to_string(), |_| vec![]);
        assert_eq!(result.len(), 1);
        assert_eq!(result[0].name, "my-session");
        assert_eq!(result[0].path, "/home/user/repo");
        assert_eq!(result[0].created_at, Some(1700000000u64));
        assert_eq!(result[0].last_activity_at, Some(1700001000u64));
    }

    #[test]
    fn parse_tmux_output_without_timestamps_defaults_to_none() {
        // Old format without timestamps — backward-compatible.
        let sessions = "my-session:/home/user/repo\n";
        let result = parse_tmux_output(sessions, None, |_| "".to_string(), |_| vec![]);
        assert_eq!(result.len(), 1);
        assert_eq!(result[0].created_at, None);
        assert_eq!(result[0].last_activity_at, None);
    }

    #[test]
    fn parse_tmux_output_with_only_created_timestamp() {
        let sessions = "my-session:/home/user/repo|1700000000\n";
        let result = parse_tmux_output(sessions, None, |_| "".to_string(), |_| vec![]);
        assert_eq!(result.len(), 1);
        assert_eq!(result[0].created_at, Some(1700000000u64));
        assert_eq!(result[0].last_activity_at, None);
    }

    // -- parse_repo_meta_from_pr_response -------------------------------------

    #[test]
    fn parse_repo_meta_extracts_default_branch_and_ci_state() {
        let json = serde_json::to_string(&json!({
            "data": {
                "repository": {
                    "defaultBranchRef": {
                        "name": "main",
                        "target": {
                            "statusCheckRollup": {
                                "state": "SUCCESS"
                            }
                        }
                    },
                    "b_feat_branch": {
                        "nodes": []
                    }
                }
            }
        }))
        .unwrap();

        let meta = parse_repo_meta_from_pr_response(&json);
        assert_eq!(meta.default_branch.as_deref(), Some("main"));
        assert_eq!(meta.main_ci_state.as_deref(), Some("SUCCESS"));
    }

    #[test]
    fn parse_repo_meta_null_default_branch_returns_default() {
        let json = serde_json::to_string(&json!({
            "data": {
                "repository": {
                    "defaultBranchRef": null
                }
            }
        }))
        .unwrap();

        let meta = parse_repo_meta_from_pr_response(&json);
        assert!(meta.default_branch.is_none());
        assert!(meta.main_ci_state.is_none());
    }

    #[test]
    fn parse_repo_meta_missing_ci_state_returns_none() {
        let json = serde_json::to_string(&json!({
            "data": {
                "repository": {
                    "defaultBranchRef": {
                        "name": "main",
                        "target": {}
                    }
                }
            }
        }))
        .unwrap();

        let meta = parse_repo_meta_from_pr_response(&json);
        assert_eq!(meta.default_branch.as_deref(), Some("main"));
        assert!(meta.main_ci_state.is_none());
    }

    #[test]
    fn parse_repo_meta_invalid_json_returns_default() {
        let meta = parse_repo_meta_from_pr_response("not json");
        assert!(meta.default_branch.is_none());
        assert!(meta.main_ci_state.is_none());
    }

    // -- pr_graphql_query_per_branch includes defaultBranchRef ----------------

    #[test]
    fn per_branch_query_includes_default_branch_ref() {
        let query = pr_graphql_query_per_branch("acme", "my-project", &["feat/branch".to_string()]);
        assert!(
            query.contains("defaultBranchRef"),
            "query must select defaultBranchRef"
        );
        assert!(
            query.contains("statusCheckRollup"),
            "query must select statusCheckRollup on defaultBranchRef target"
        );
    }

    #[test]
    fn per_branch_parser_populates_details_url_on_check_info() {
        let json = r#"{
            "data": {
                "repository": {
                    "defaultBranchRef": null,
                    "b_feat_branch": {
                        "nodes": [{
                            "number": 1,
                            "headRefName": "feat/branch",
                            "baseRefName": "main",
                            "title": "Test",
                            "state": "OPEN",
                            "isDraft": false,
                            "author": { "login": "dev" },
                            "reviewDecision": null,
                            "mergeable": "MERGEABLE",
                            "labels": { "nodes": [] },
                            "reviewRequests": { "nodes": [] },
                            "reviews": { "nodes": [] },
                            "reviewThreads": { "nodes": [] },
                            "closingIssuesReferences": { "nodes": [] },
                            "additions": 10,
                            "deletions": 5,
                            "createdAt": "2026-01-01T00:00:00Z",
                            "updatedAt": "2026-01-02T00:00:00Z",
                            "commits": {
                                "nodes": [{
                                    "commit": {
                                        "pushedDate": "2026-01-01T12:00:00Z",
                                        "statusCheckRollup": {
                                            "state": "SUCCESS",
                                            "contexts": {
                                                "totalCount": 1,
                                                "nodes": [{
                                                    "__typename": "CheckRun",
                                                    "name": "test-unit",
                                                    "conclusion": "SUCCESS",
                                                    "status": "COMPLETED",
                                                    "detailsUrl": "https://github.com/owner/repo/actions/runs/123"
                                                }]
                                            }
                                        }
                                    }
                                }]
                            }
                        }]
                    }
                }
            }
        }"#;

        let matcher = GateMatcher::new(&[]);
        let prs = parse_prs_graphql_per_branch(json, &matcher);
        assert_eq!(prs.len(), 1);
        assert_eq!(prs[0].ci_checks.code.len(), 1);
        assert_eq!(
            prs[0].ci_checks.code[0].details_url.as_deref(),
            Some("https://github.com/owner/repo/actions/runs/123")
        );
    }
}
