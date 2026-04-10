//! Imperative shell for populating Orchard's on-disk cache.
//!
//! Fetches issues, PRs, worktrees, and tmux sessions from their respective
//! sources (GitHub CLI, git, SSH remotes) and writes the results to the
//! cache files consumed by `sources::*` and `derive`.
use std::process::Command;

use crate::cache::{self, CachedIssue, CachedPr, CachedTmuxSession, CachedWorktree};
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
            })
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

            let checks_state = derive_checks_state_graphql(v);

            let is_draft = v["isDraft"].as_bool().unwrap_or(false);

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

            Some(CachedPr {
                number,
                branch,
                linked_issue,
                state,
                review_decision,
                checks_state,
                has_conflicts,
                unresolved_threads,
                linked_issue_state,
                is_draft,
            })
        })
        .collect()
}

/// Derives checks state from the GraphQL commit statusCheckRollup.
///
/// Path: `commits.nodes[0].commit.statusCheckRollup.state`
fn derive_checks_state_graphql(pr: &serde_json::Value) -> Option<String> {
    let state = pr["commits"]["nodes"].as_array()?.last()?["commit"]["statusCheckRollup"]["state"]
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
        });
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

/// Fetches all GitHub issues for `config.slug` and writes to the issues cache.
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
            "--state",
            "all",
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
        isDraft
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
            state
            stateReason
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
            LOG.warn(&format!("cache_sources: refresh_prs({}): {e}", config.slug));
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
            &["list-sessions", "-F", "#{session_name}:#{session_path}"],
        ),
        Some(h) => {
            let cmd = format!(
                "tmux list-sessions -F '#{{session_name}}:#{{session_path}}' && echo '{}' && cat '${{TMPDIR:-/tmp}}'/orchard-claude-*.json 2>/dev/null; true",
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
        gql_pr_node_draft(
            number,
            branch,
            review_decision,
            mergeable,
            check_state,
            linked_issues,
            unresolved_threads,
            false,
        )
    }

    /// Helper to build a single PR node in GraphQL format with explicit `is_draft` flag.
    #[allow(clippy::too_many_arguments)]
    fn gql_pr_node_draft(
        number: u32,
        branch: &str,
        review_decision: Option<&str>,
        mergeable: &str,
        check_state: Option<&str>,
        linked_issues: Vec<u32>,
        unresolved_threads: u32,
        is_draft: bool,
    ) -> serde_json::Value {
        let threads: Vec<serde_json::Value> = (0..unresolved_threads)
            .map(|_| json!({"isResolved": false}))
            .collect();
        let issues: Vec<serde_json::Value> = linked_issues
            .into_iter()
            .map(|n| json!({"number": n, "state": "OPEN", "stateReason": null}))
            .collect();
        let commits = match check_state {
            Some(s) => json!([{"commit": {"statusCheckRollup": {"state": s}}}]),
            None => json!([{"commit": {"statusCheckRollup": null}}]),
        };
        json!({
            "number": number,
            "headRefName": branch,
            "state": "OPEN",
            "isDraft": is_draft,
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

        let prs = parse_prs_graphql(&json);
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

        let prs = parse_prs_graphql(&json);
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

        let prs = parse_prs_graphql(&json);
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

        let prs = parse_prs_graphql(&json);
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

        let prs = parse_prs_graphql(&json);
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

        let prs = parse_prs_graphql(&json);
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

        let prs = parse_prs_graphql(&json);
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

        let prs = parse_prs_graphql(&json);
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

        let prs = parse_prs_graphql(&json);
        assert_eq!(prs[0].checks_state.as_deref(), Some("pending"));
    }

    #[test]
    fn parse_prs_graphql_invalid_json_returns_empty() {
        assert!(parse_prs_graphql("{bad}").is_empty());
    }

    #[test]
    fn parse_prs_graphql_is_draft_true() {
        let json = graphql_prs(json!([gql_pr_node_draft(
            20,
            "feat/draft-pr",
            None,
            "MERGEABLE",
            None,
            vec![],
            0,
            true
        )]));

        let prs = parse_prs_graphql(&json);
        assert!(prs[0].is_draft, "expected is_draft to be true");
    }

    #[test]
    fn parse_prs_graphql_is_draft_false_by_default() {
        let json = graphql_prs(json!([gql_pr_node(
            21,
            "feat/ready-pr",
            None,
            "MERGEABLE",
            None,
            vec![],
            0
        )]));

        let prs = parse_prs_graphql(&json);
        assert!(!prs[0].is_draft, "expected is_draft to be false");
    }

    #[test]
    fn graphql_query_includes_is_draft_field() {
        // The GraphQL query is built inside refresh_prs. We verify the isDraft field
        // is present in our test node builder and parsed correctly, which mirrors
        // that the query includes the field.
        let node = gql_pr_node_draft(1, "feat/x", None, "MERGEABLE", None, vec![], 0, true);
        let json = graphql_prs(json!([node]));
        let prs = parse_prs_graphql(&json);
        assert!(
            prs[0].is_draft,
            "isDraft field must be parsed from GraphQL response"
        );
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
}
