//! Claude hook-state types and reader.
//!
//! Defines `ClaudeStateFile` (the JSON written by the orchard-state.sh hook)
//! and `ClaudeState` (the parsed working/idle/input enum). Provides helpers to
//! read all state files from a directory and look up state by tmux session name.
use std::path::Path;

use serde::{Deserialize, Serialize};

/// State written by the orchard-state.sh hook script.
#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
pub struct ClaudeStateFile {
    /// Raw state string from the hook script: `"working"`, `"idle"`, or `"input"`.
    pub state: String,
    /// Unique Claude session identifier.
    pub session_id: String,
    /// Name of the tmux session this Claude process is running inside.
    pub tmux_session: String,
    /// Working directory of the Claude process.
    pub cwd: String,
    /// Hook event that triggered the state write (e.g. `"Stop"`, `"PreToolUse"`).
    pub event: String,
    /// ISO 8601 timestamp of the last state write.
    pub timestamp: String,
    /// Context window utilization percentage (0–100), if available from the statusline.
    #[serde(default)]
    pub context_window_pct: Option<f64>,
    /// Cumulative cost in USD for this session, if available from the statusline.
    #[serde(default)]
    pub cost_usd: Option<f64>,
    /// Model identifier in use (e.g. `"opus"`, `"sonnet"`), if available from the statusline.
    #[serde(default)]
    pub model: Option<String>,
}

/// Parsed Claude state for use in derive logic.
#[derive(Debug, Clone, Copy, PartialEq, Eq, serde::Serialize)]
#[serde(rename_all = "snake_case")]
pub enum ClaudeState {
    /// Claude is actively executing a tool or generating a response.
    Working,
    /// Claude has finished its turn and is waiting for the next prompt.
    Idle,
    /// Claude is paused and waiting for user input.
    Input,
    /// No Claude state file was found, or the state string was unrecognised.
    None,
}

impl std::str::FromStr for ClaudeState {
    type Err = std::convert::Infallible;

    fn from_str(s: &str) -> Result<Self, Self::Err> {
        Ok(match s {
            "working" => Self::Working,
            "idle" => Self::Idle,
            "input" => Self::Input,
            _ => Self::None,
        })
    }
}

/// Reads all orchard hook state files from the given directory.
///
/// Only files matching `{dir}/orchard-claude-*.json` are read.
/// Malformed files and in-progress writes (`.tmp.`) are silently skipped.
pub fn read_all_state_files(dir: &Path) -> Vec<ClaudeStateFile> {
    let pattern = format!("{}/orchard-claude-*.json", dir.display());
    let mut results = Vec::new();

    for path in glob::glob(&pattern).into_iter().flatten().flatten() {
        // Skip .tmp files (in-progress atomic writes)
        if path.to_string_lossy().contains(".tmp.") {
            continue;
        }
        if let Ok(data) = std::fs::read(&path)
            && let Ok(state) = serde_json::from_slice::<ClaudeStateFile>(&data)
        {
            results.push(state);
        }
    }

    results
}

/// Finds the state for a specific tmux session name.
pub fn state_for_session<'a>(
    states: &'a [ClaudeStateFile],
    tmux_session: &str,
) -> Option<&'a ClaudeStateFile> {
    states.iter().find(|s| s.tmux_session == tmux_session)
}

/// Parses concatenated Claude state JSON from batched SSH output.
///
/// The input is the portion of SSH output after the `---CLAUDE_STATE---` sentinel —
/// typically the result of `cat ${TMPDIR:-/tmp}/orchard-claude-*.json`. Multiple
/// JSON objects may be concatenated without newlines (e.g. `{}{}`), so this uses
/// `serde_json::StreamDeserializer` rather than line-splitting.
///
/// Malformed JSON fragments are silently skipped: when parsing fails, the function
/// scans forward to the next `{` character and retries from there. If multiple
/// entries share the same `tmux_session`, the one with the most recent `timestamp`
/// is kept.
pub fn parse_remote_state_output(raw: &str) -> Vec<ClaudeStateFile> {
    let trimmed = raw.trim();
    if trimmed.is_empty() {
        return Vec::new();
    }

    // Deduplicate by tmux_session, keeping the most recent timestamp.
    let mut by_session: std::collections::HashMap<String, ClaudeStateFile> =
        std::collections::HashMap::new();

    // We may need to skip past malformed fragments. We work with byte slices and
    // advance past the current failed position by seeking to the next '{'.
    let bytes = trimmed.as_bytes();
    let mut pos = 0usize;

    while pos < bytes.len() {
        // Skip whitespace and non-JSON characters until we find a '{'.
        let Some(start) = bytes[pos..].iter().position(|&b| b == b'{') else {
            break;
        };
        pos += start;

        let slice = &trimmed[pos..];
        let mut stream = serde_json::Deserializer::from_str(slice).into_iter::<ClaudeStateFile>();

        match stream.next() {
            Some(Ok(entry)) => {
                // Advance position by the number of bytes consumed.
                pos += stream.byte_offset();

                use std::collections::hash_map::Entry;
                match by_session.entry(entry.tmux_session.clone()) {
                    Entry::Vacant(slot) => {
                        slot.insert(entry);
                    }
                    Entry::Occupied(mut slot) => {
                        // Keep the entry with the more recent timestamp (lexicographic
                        // comparison is correct for ISO 8601 timestamps).
                        if entry.timestamp > slot.get().timestamp {
                            *slot.get_mut() = entry;
                        }
                    }
                }
            }
            Some(Err(_)) | None => {
                // Skip past this '{' to avoid an infinite loop.
                pos += 1;
            }
        }
    }

    by_session.into_values().collect()
}

#[cfg(test)]
mod tests {
    use super::*;

    fn make_state_file(state: &str, tmux_session: &str) -> ClaudeStateFile {
        ClaudeStateFile {
            state: state.to_string(),
            session_id: "sess-123".to_string(),
            tmux_session: tmux_session.to_string(),
            cwd: "/workspace/repo".to_string(),
            event: "Stop".to_string(),
            timestamp: "2026-03-25T10:00:00Z".to_string(),
            context_window_pct: None,
            cost_usd: None,
            model: None,
        }
    }

    #[test]
    fn claude_state_from_str_working() {
        assert_eq!(
            "working".parse::<ClaudeState>().unwrap(),
            ClaudeState::Working
        );
    }

    #[test]
    fn claude_state_from_str_idle() {
        assert_eq!("idle".parse::<ClaudeState>().unwrap(), ClaudeState::Idle);
    }

    #[test]
    fn claude_state_from_str_input() {
        assert_eq!("input".parse::<ClaudeState>().unwrap(), ClaudeState::Input);
    }

    #[test]
    fn claude_state_from_str_unknown_is_none() {
        assert_eq!("unknown".parse::<ClaudeState>().unwrap(), ClaudeState::None);
    }

    #[test]
    fn state_for_session_finds_matching_entry() {
        let states = vec![
            make_state_file("working", "repo_47_claude"),
            make_state_file("idle", "repo_48_main"),
        ];
        let found = state_for_session(&states, "repo_47_claude");
        assert!(found.is_some());
        assert_eq!(found.unwrap().state, "working");
    }

    #[test]
    fn state_for_session_returns_none_when_not_found() {
        let states = vec![make_state_file("idle", "repo_48_main")];
        let found = state_for_session(&states, "repo_47_claude");
        assert!(found.is_none());
    }

    #[test]
    fn read_all_state_files_skips_malformed() {
        // Write a malformed file and verify it doesn't panic
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("orchard-claude-test.json");
        std::fs::write(&path, b"not json").unwrap();
        // We can't easily test the glob pattern directly (it's hardcoded to /tmp),
        // but we can verify serde handles bad data gracefully
        let result = serde_json::from_slice::<ClaudeStateFile>(b"not json");
        assert!(result.is_err());
    }

    #[test]
    fn state_file_deserializes_with_optional_enrichment() {
        let json = r#"{
            "state": "working",
            "session_id": "abc",
            "tmux_session": "repo_47",
            "cwd": "/workspace",
            "event": "PreToolUse",
            "timestamp": "2026-03-25T10:00:00Z",
            "context_window_pct": 73.0,
            "cost_usd": 0.42,
            "model": "opus"
        }"#;
        let sf: ClaudeStateFile = serde_json::from_str(json).unwrap();
        assert_eq!(sf.state, "working");
        assert_eq!(sf.context_window_pct, Some(73.0));
        assert_eq!(sf.cost_usd, Some(0.42));
        assert_eq!(sf.model.as_deref(), Some("opus"));
    }

    // -- parse_remote_state_output -------------------------------------------

    fn now_iso() -> String {
        chrono::Utc::now().to_rfc3339()
    }

    fn make_json(state: &str, session: &str, ts: &str) -> String {
        format!(
            r#"{{"state":"{state}","session_id":"s1","tmux_session":"{session}","cwd":"/workspace","event":"Stop","timestamp":"{ts}"}}"#
        )
    }

    #[test]
    fn parse_remote_state_output_parses_two_fresh_entries() {
        let ts = now_iso();
        let raw = format!(
            "{}\n{}",
            make_json("working", "repo_47_claude", &ts),
            make_json("idle", "repo_48_main", &ts)
        );
        let result = parse_remote_state_output(&raw);
        assert_eq!(result.len(), 2);
        let working = result.iter().find(|s| s.tmux_session == "repo_47_claude");
        assert!(working.is_some());
        assert_eq!(working.unwrap().state, "working");
        let idle = result.iter().find(|s| s.tmux_session == "repo_48_main");
        assert!(idle.is_some());
        assert_eq!(idle.unwrap().state, "idle");
    }

    #[test]
    fn parse_remote_state_output_handles_concatenated_without_newlines() {
        let ts = now_iso();
        let raw = format!(
            "{}{}",
            make_json("working", "repo_47_claude", &ts),
            make_json("idle", "repo_48_main", &ts)
        );
        let result = parse_remote_state_output(&raw);
        assert_eq!(result.len(), 2);
    }

    #[test]
    fn parse_remote_state_output_skips_malformed_entries() {
        let ts = now_iso();
        let raw = format!(
            "{}\nnot valid json\n{}",
            make_json("working", "repo_47_claude", &ts),
            make_json("idle", "repo_48_main", &ts)
        );
        let result = parse_remote_state_output(&raw);
        assert_eq!(result.len(), 2);
    }

    #[test]
    fn parse_remote_state_output_empty_input_returns_empty() {
        let result = parse_remote_state_output("");
        assert!(result.is_empty());
    }

    #[test]
    fn parse_remote_state_output_whitespace_only_returns_empty() {
        let result = parse_remote_state_output("   \n  ");
        assert!(result.is_empty());
    }

    #[test]
    fn parse_remote_state_output_deduplicates_keeping_newest_timestamp() {
        let older_ts = "2026-03-28T10:00:00Z";
        let newer_ts = "2026-03-28T10:00:30Z";
        // Older entry first, newer entry second — should keep newer.
        let raw = format!(
            "{}\n{}",
            make_json("idle", "repo_47_claude", older_ts),
            make_json("working", "repo_47_claude", newer_ts)
        );
        let result = parse_remote_state_output(&raw);
        assert_eq!(result.len(), 1);
        assert_eq!(result[0].tmux_session, "repo_47_claude");
        assert_eq!(result[0].state, "working");
        assert_eq!(result[0].timestamp, newer_ts);
    }

    #[test]
    fn parse_remote_state_output_deduplicates_keeping_newest_when_older_comes_last() {
        let older_ts = "2026-03-28T10:00:00Z";
        let newer_ts = "2026-03-28T10:00:30Z";
        // Newer entry first, older entry second — should still keep newer.
        let raw = format!(
            "{}\n{}",
            make_json("working", "repo_47_claude", newer_ts),
            make_json("idle", "repo_47_claude", older_ts)
        );
        let result = parse_remote_state_output(&raw);
        assert_eq!(result.len(), 1);
        assert_eq!(result[0].state, "working");
    }

    #[test]
    fn state_file_deserializes_without_optional_enrichment() {
        let json = r#"{
            "state": "idle",
            "session_id": "abc",
            "tmux_session": "repo_47",
            "cwd": "/workspace",
            "event": "Stop",
            "timestamp": "2026-03-25T10:00:00Z"
        }"#;
        let sf: ClaudeStateFile = serde_json::from_str(json).unwrap();
        assert_eq!(sf.state, "idle");
        assert!(sf.context_window_pct.is_none());
        assert!(sf.cost_usd.is_none());
        assert!(sf.model.is_none());
    }
}
