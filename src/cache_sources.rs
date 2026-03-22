use std::process::Command;
use std::sync::OnceLock;

use regex::Regex;

use crate::cache::{
    self, CachedIssue, CachedPr, CachedTmuxSession, CachedWorktree,
};
use crate::global_config::RepoConfig;
use crate::logger::LOG;
use crate::remote;

// ---------------------------------------------------------------------------
// Linked issue extraction
// ---------------------------------------------------------------------------

fn closing_keyword_re() -> &'static Regex {
    static RE: OnceLock<Regex> = OnceLock::new();
    RE.get_or_init(|| {
        Regex::new(r"(?i)(?:close[sd]?|fix(?:e[sd])?|resolve[sd]?)\s+#(\d+)").unwrap()
    })
}

/// Extracts the first linked issue number from a PR body.
///
/// Recognises GitHub closing keywords: "Closes #N", "Fixes #N", "Resolves #N"
/// (and their conjugated forms), case-insensitively.
pub fn extract_linked_issue_from_body(body: &str) -> Option<u32> {
    closing_keyword_re()
        .captures(body)
        .and_then(|caps| caps[1].parse::<u32>().ok())
}

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
            Some(CachedIssue { number, title, state, labels })
        })
        .collect()
}

/// Parses GraphQL PR response JSON into a `Vec<CachedPr>`.
///
/// Expected shape: `{"data":{"repository":{"pullRequests":{"nodes":[...]}}}}`
/// Each node has: number, headRefName, state, reviewDecision, mergeable,
/// reviewThreads, closingIssuesReferences, commits (for status checks).
pub fn parse_prs_graphql(json: &str) -> Vec<CachedPr> {
    let root: serde_json::Value = match serde_json::from_str(json) {
        Ok(v) => v,
        Err(e) => {
            LOG.warn(&format!("cache_sources: failed to parse PRs GraphQL JSON: {e}"));
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

            let checks_state = derive_checks_state_graphql(v);

            let has_conflicts =
                v["mergeable"].as_str().unwrap_or("") == "CONFLICTING";

            let unresolved_threads = v["reviewThreads"]["nodes"]
                .as_array()
                .map(|arr| {
                    arr.iter()
                        .filter(|t| t["isResolved"].as_bool() != Some(true))
                        .count() as u32
                })
                .unwrap_or(0);

            // Use GitHub's closingIssuesReferences (first linked issue).
            let linked_issue = v["closingIssuesReferences"]["nodes"]
                .as_array()
                .and_then(|arr| arr.first())
                .and_then(|issue| issue["number"].as_u64())
                .map(|n| n as u32);

            Some(CachedPr {
                number,
                branch,
                linked_issue,
                state,
                review_decision,
                checks_state,
                has_conflicts,
                unresolved_threads,
            })
        })
        .collect()
}

/// Derives checks state from the GraphQL commit statusCheckRollup.
///
/// Path: `commits.nodes[0].commit.statusCheckRollup.state`
fn derive_checks_state_graphql(pr: &serde_json::Value) -> Option<String> {
    let state = pr["commits"]["nodes"]
        .as_array()?
        .last()?["commit"]["statusCheckRollup"]["state"]
        .as_str()?;

    match state {
        "SUCCESS" | "EXPECTED" => Some("passing".to_string()),
        "FAILURE" | "ERROR" => Some("failing".to_string()),
        "PENDING" => Some("pending".to_string()),
        _ => None,
    }
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
        if path.contains(".git/modules/") {
            if let Ok(out) = std::process::Command::new("git")
                .args(["rev-parse", "--show-toplevel"])
                .current_dir(&path)
                .output()
            {
                let resolved = String::from_utf8_lossy(&out.stdout).trim().to_string();
                if !resolved.is_empty() {
                    path = resolved;
                }
            }
        }

        worktrees.push(CachedWorktree { path, branch, is_bare, is_locked, host: None });
    }

    worktrees
}

/// Parses the combined output of `tmux list-sessions` and per-session pane
/// information into `Vec<CachedTmuxSession>`.
///
/// `sessions_output` has lines in the format `{session_name}:{session_path}`.
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
        // Format: "{session_name}:{session_path}"
        // Session path may itself contain colons (e.g. Windows paths or
        // absolute paths on some systems), so we split on the first colon only.
        let Some((name, path)) = line.split_once(':') else {
            continue;
        };

        let pane_output = panes_fn(name);
        let (pane_titles, pane_commands) = parse_pane_lines(&pane_output);
        let last_output_lines = content_fn(name);

        sessions.push(CachedTmuxSession {
            name: name.to_string(),
            path: path.to_string(),
            pane_titles,
            pane_commands,
            host: host.map(|h| h.to_string()),
            last_output_lines,
        });
    }

    sessions
}

/// Parses pane info lines in the format `{pane_title}:{pane_current_command}`.
fn parse_pane_lines(output: &str) -> (Vec<String>, Vec<String>) {
    let mut titles = Vec::new();
    let mut commands = Vec::new();

    for line in output.trim().lines() {
        if line.is_empty() {
            continue;
        }
        // Split on first colon; title may contain colons.
        if let Some((title, cmd)) = line.split_once(':') {
            titles.push(title.to_string());
            commands.push(cmd.to_string());
        }
    }

    (titles, commands)
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

/// Fetches open GitHub issues for `config.slug` and writes to the issues cache.
///
/// On API failure the error is logged and the existing cache is left intact.
pub fn refresh_issues(config: &RepoConfig) -> anyhow::Result<()> {
    let path = cache::cache_path(config.owner(), config.repo_name(), "issues");

    let out = run_local(
        "gh",
        &[
            "issue",
            "list",
            "--repo",
            &config.slug,
            "--assignee",
            "@me",
            "--state",
            "open",
            "--limit",
            "100",
            "--json",
            "number,title,state,labels",
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
            let issues = parse_issues_json(&json);
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

/// Fetches open GitHub PRs for `config.slug` via GraphQL and writes to the PRs cache.
///
/// Uses GraphQL to get closingIssuesReferences (linked issues) and reviewThreads,
/// which are not available via `gh pr list --json`.
/// On API failure the error is logged and the existing cache is left intact.
pub fn refresh_prs(config: &RepoConfig) -> anyhow::Result<()> {
    let path = cache::cache_path(config.owner(), config.repo_name(), "prs");

    let query = format!(
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
        reviewThreads(first: 100) {{
          nodes {{
            isResolved
          }}
        }}
        closingIssuesReferences(first: 5) {{
          nodes {{
            number
          }}
        }}
        commits(last: 1) {{
          nodes {{
            commit {{
              statusCheckRollup {{
                state
              }}
            }}
          }}
        }}
      }}
    }}
  }}
}}"#,
        owner = config.owner(),
        name = config.repo_name(),
    );

    let out = run_local("gh", &["api", "graphql", "-f", &format!("query={query}")]);

    match out {
        Err(e) => {
            LOG.warn(&format!(
                "cache_sources: refresh_prs({}): {e}",
                config.slug
            ));
            return Ok(());
        }
        Ok(json) => {
            let prs = parse_prs_graphql(&json);
            cache::write_cache_if_nonempty(&path, &prs)?;
            LOG.info(&format!(
                "cache_sources: refresh_prs({}): wrote {} entries",
                config.slug,
                prs.len()
            ));
        }
    }

    Ok(())
}

/// Fetches git worktrees for `config.path` and writes to the worktrees cache.
///
/// On failure the error is logged and the existing cache is left intact.
pub fn refresh_worktrees(config: &RepoConfig) -> anyhow::Result<()> {
    let path = cache::cache_path(config.owner(), config.repo_name(), "worktrees");

    let out = run_local_in(
        "git",
        &["worktree", "list", "--porcelain"],
        &config.path,
    );

    match out {
        Err(e) => {
            LOG.warn(&format!(
                "cache_sources: refresh_worktrees({}): {e}",
                config.slug
            ));
            return Ok(());
        }
        Ok(porcelain) => {
            let worktrees = parse_worktree_porcelain(&porcelain);
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

/// Fetches tmux sessions (local or remote) and writes to the tmux sessions cache.
///
/// Pass `None` for local sessions, or `Some("user@host")` for remote sessions.
/// On failure the error is logged and the existing cache is left intact.
pub fn refresh_tmux_sessions(host: Option<&str>) -> anyhow::Result<()> {
    let cache_path = cache::tmux_cache_path(host);

    let sessions_out = match host {
        None => run_local(
            "tmux",
            &["list-sessions", "-F", "#{session_name}:#{session_path}"],
        ),
        Some(h) => remote::ssh_exec(
            h,
            "tmux list-sessions -F '#{session_name}:#{session_path}'",
        ),
    };

    let sessions_output = match sessions_out {
        Err(e) => {
            LOG.warn(&format!(
                "cache_sources: refresh_tmux_sessions(host={:?}): {e}",
                host
            ));
            return Ok(());
        }
        Ok(s) => s,
    };

    let sessions = parse_tmux_output(
        &sessions_output,
        host,
        |session_name| {
            let pane_fmt = "#{pane_title}:#{pane_current_command}";
            match host {
                None => run_local(
                    "tmux",
                    &["list-panes", "-t", session_name, "-F", pane_fmt],
                )
                .unwrap_or_default(),
                Some(h) => {
                    let cmd = format!(
                        "tmux list-panes -t {} -F '{}'",
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

    cache::write_cache_if_nonempty(&cache_path, &sessions)?;
    LOG.info(&format!(
        "cache_sources: refresh_tmux_sessions(host={:?}): wrote {} entries",
        host,
        sessions.len()
    ));

    Ok(())
}

/// Fetches git worktrees from a single remote host and writes to the remote worktrees cache.
///
/// On failure the error is logged and the existing cache is left intact.
pub fn refresh_remote_worktrees(
    config: &RepoConfig,
    remote_cfg: &crate::global_config::RemoteConfig,
) -> anyhow::Result<()> {
    let cache_path =
        cache::cache_path(config.owner(), config.repo_name(), "remote_worktrees");

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
                config.slug, remote_cfg.host, worktrees.len(),
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

    // -- extract_linked_issue_from_body --------------------------------------

    #[test]
    fn extract_closes_keyword() {
        assert_eq!(extract_linked_issue_from_body("Closes #47"), Some(47));
    }

    #[test]
    fn extract_fixes_keyword() {
        assert_eq!(extract_linked_issue_from_body("Fixes #12"), Some(12));
    }

    #[test]
    fn extract_resolves_keyword() {
        assert_eq!(extract_linked_issue_from_body("Resolves #99"), Some(99));
    }

    #[test]
    fn extract_case_insensitive() {
        assert_eq!(extract_linked_issue_from_body("closes #5"), Some(5));
        assert_eq!(extract_linked_issue_from_body("FIXES #200"), Some(200));
    }

    #[test]
    fn extract_closed_conjugation() {
        assert_eq!(extract_linked_issue_from_body("Closed #10"), Some(10));
    }

    #[test]
    fn extract_fixed_conjugation() {
        assert_eq!(extract_linked_issue_from_body("Fixed #77"), Some(77));
    }

    #[test]
    fn extract_no_match_returns_none() {
        assert_eq!(extract_linked_issue_from_body("This PR does some stuff"), None);
    }

    #[test]
    fn extract_from_longer_body() {
        let body = "## Summary\nThis implements the feature.\n\nFixes #42\n\nSome trailing text.";
        assert_eq!(extract_linked_issue_from_body(body), Some(42));
    }

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

    // -- parse_prs_graphql ---------------------------------------------------

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
    fn gql_pr_node(
        number: u32,
        branch: &str,
        review_decision: Option<&str>,
        mergeable: &str,
        check_state: Option<&str>,
        linked_issues: Vec<u32>,
        unresolved_threads: u32,
    ) -> serde_json::Value {
        let threads: Vec<serde_json::Value> = (0..unresolved_threads)
            .map(|_| json!({"isResolved": false}))
            .collect();
        let issues: Vec<serde_json::Value> = linked_issues
            .into_iter()
            .map(|n| json!({"number": n}))
            .collect();
        let commits = match check_state {
            Some(s) => json!([{"commit": {"statusCheckRollup": {"state": s}}}]),
            None => json!([{"commit": {"statusCheckRollup": null}}]),
        };
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
        let json = graphql_prs(json!([
            gql_pr_node(55, "feat/task-centric", Some("APPROVED"), "MERGEABLE", Some("SUCCESS"), vec![47], 0)
        ]));

        let prs = parse_prs_graphql(&json);
        assert_eq!(prs.len(), 1);

        let pr = &prs[0];
        assert_eq!(pr.number, 55);
        assert_eq!(pr.branch, "feat/task-centric");
        assert_eq!(pr.linked_issue, Some(47));
        assert_eq!(pr.state, "open");
        assert_eq!(pr.review_decision.as_deref(), Some("approved"));
        assert_eq!(pr.checks_state.as_deref(), Some("passing"));
        assert!(!pr.has_conflicts);
        assert_eq!(pr.unresolved_threads, 0);
    }

    #[test]
    fn parse_prs_graphql_changes_requested() {
        let json = graphql_prs(json!([
            gql_pr_node(10, "fix/something", Some("CHANGES_REQUESTED"), "MERGEABLE", None, vec![], 0)
        ]));

        let prs = parse_prs_graphql(&json);
        assert_eq!(prs[0].review_decision.as_deref(), Some("changes_requested"));
    }

    #[test]
    fn parse_prs_graphql_has_conflicts_when_conflicting() {
        let json = graphql_prs(json!([
            gql_pr_node(10, "fix/conflict", None, "CONFLICTING", None, vec![], 0)
        ]));

        let prs = parse_prs_graphql(&json);
        assert!(prs[0].has_conflicts);
    }

    #[test]
    fn parse_prs_graphql_counts_unresolved_threads() {
        let json = graphql_prs(json!([
            gql_pr_node(10, "fix/threads", None, "MERGEABLE", None, vec![], 3)
        ]));

        let prs = parse_prs_graphql(&json);
        assert_eq!(prs[0].unresolved_threads, 3);
    }

    #[test]
    fn parse_prs_graphql_no_linked_issue_when_empty() {
        let json = graphql_prs(json!([
            gql_pr_node(99, "chore/no-issue", None, "MERGEABLE", None, vec![], 0)
        ]));

        let prs = parse_prs_graphql(&json);
        assert_eq!(prs[0].linked_issue, None);
    }

    #[test]
    fn parse_prs_graphql_checks_state_failing() {
        let json = graphql_prs(json!([
            gql_pr_node(10, "feat/ci-fail", None, "MERGEABLE", Some("FAILURE"), vec![], 0)
        ]));

        let prs = parse_prs_graphql(&json);
        assert_eq!(prs[0].checks_state.as_deref(), Some("failing"));
    }

    #[test]
    fn parse_prs_graphql_checks_state_pending() {
        let json = graphql_prs(json!([
            gql_pr_node(10, "feat/ci-pending", None, "MERGEABLE", Some("PENDING"), vec![], 0)
        ]));

        let prs = parse_prs_graphql(&json);
        assert_eq!(prs[0].checks_state.as_deref(), Some("pending"));
    }

    #[test]
    fn parse_prs_graphql_invalid_json_returns_empty() {
        assert!(parse_prs_graphql("{bad}").is_empty());
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
        let input =
            "worktree /home/user/repo-feat\nHEAD abc\nbranch refs/heads/feat/my-work\n";
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
        let input = "worktree /home/user/repo-locked\nHEAD abc\nbranch refs/heads/main\nlocked reason\n";
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
                "zsh:zsh\nbash:bash\n".to_string()
            },
            |_| vec![],
        );
        assert_eq!(result.len(), 1);
        assert_eq!(result[0].name, "my-session");
        assert_eq!(result[0].path, "/home/user/repo");
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
}
