//! Types used by the TUI to project daemon-derived ClaudeInstance state.
//!
//! Defines `ClaudeStateFile` (the data shape projected from the daemon's
//! `ClaudeInstance` GraphQL type) and `ClaudeState` (working/idle/input enum).
//! `ClaudeStateFile` is populated exclusively by
//! `daemon::work_view_adapter::claude_instance_to_state_file` — no disk reads.
use serde::{Deserialize, Serialize};

/// Projected state for a Claude instance, derived from daemon GraphQL output.
#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
pub struct ClaudeStateFile {
    /// Raw state string: `"working"`, `"idle"`, or `"input"`.
    pub state: String,
    /// Unique Claude session identifier.
    pub session_id: String,
    /// Name of the tmux session this Claude process is running inside.
    pub tmux_session: String,
    /// Working directory of the Claude process.
    pub cwd: String,
    /// The hook event that produced this state (e.g. `"Stop"`, `"PreToolUse"`).
    pub event: String,
    /// ISO 8601 timestamp of the last state write.
    pub timestamp: String,
    /// Model identifier in use (e.g. `"claude-opus-4-6"`).
    #[serde(default)]
    pub model: Option<String>,
    /// Last tool invoked (cleared when idle).
    #[serde(default)]
    pub last_tool: Option<String>,
    /// First line of the last user prompt, truncated to 80 chars.
    #[serde(default)]
    pub current_task: Option<String>,
    /// Unix epoch seconds when the session started.
    #[serde(default)]
    pub session_start_ts: Option<u64>,
    /// Total input tokens from the most recent assistant message.
    #[serde(default)]
    pub input_tokens: Option<u64>,
    /// Total output tokens from the most recent assistant message.
    #[serde(default)]
    pub output_tokens: Option<u64>,
    /// Cache creation input tokens from the most recent assistant message.
    #[serde(default)]
    pub cache_creation_input_tokens: Option<u64>,
    /// Cache read input tokens from the most recent assistant message.
    #[serde(default)]
    pub cache_read_input_tokens: Option<u64>,
    /// The `stop_reason` field from the `Stop` hook event payload.
    ///
    /// Present only on state files written by a `Stop` event. Values: `"end_turn"`,
    /// `"tool_use"`, `"max_tokens"`, `"other"`. Absent for all other events.
    #[serde(default)]
    pub stop_reason: Option<String>,
    /// Number of in-flight tool calls at the time this state was written.
    ///
    /// Non-zero during tool-execution events.
    #[serde(default)]
    pub inflight_tool_count: Option<u32>,
    /// ISO 8601 timestamp when the state last transitioned.
    ///
    /// Only updated when the state value changes (e.g. working → idle).
    /// Preserved unchanged on events that don't change the state.
    #[serde(default)]
    pub state_changed_at: Option<String>,
}

/// Parsed Claude state for use in derive logic.
#[derive(Debug, Clone, Copy, PartialEq, Eq, serde::Serialize, serde::Deserialize)]
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

/// Finds the state for a specific tmux session name.
pub fn state_for_session<'a>(
    states: &'a [ClaudeStateFile],
    tmux_session: &str,
) -> Option<&'a ClaudeStateFile> {
    states.iter().find(|s| s.tmux_session == tmux_session)
}

/// Telemetry data for the Claude Code status line.
///
/// Merged with `ClaudeStateFile` state in the TUI display layer.
#[derive(Debug, Clone, Serialize, Deserialize, Default)]
pub struct StatusLineFile {
    /// The tmux session name this status line belongs to.
    pub tmux_session: String,
    /// Context window usage percentage (0.0 to 100.0).
    #[serde(default)]
    pub context_window_pct: Option<f64>,
    /// Total cost in USD for this session.
    #[serde(default)]
    pub cost_usd: Option<f64>,
    /// Total duration of the session in milliseconds.
    #[serde(default)]
    pub total_duration_ms: Option<u64>,
    /// 5-hour rate limit usage percentage.
    #[serde(default)]
    pub rate_limit_five_hour_used_pct: Option<f64>,
    /// 5-hour rate limit reset timestamp (ISO 8601).
    #[serde(default)]
    pub rate_limit_five_hour_resets_at: Option<String>,
    /// 7-day rate limit usage percentage.
    #[serde(default)]
    pub rate_limit_seven_day_used_pct: Option<f64>,
    /// 7-day rate limit reset timestamp (ISO 8601).
    #[serde(default)]
    pub rate_limit_seven_day_resets_at: Option<String>,
}

/// Finds the status line telemetry for a specific tmux session name.
pub fn statusline_for_session<'a>(
    files: &'a [StatusLineFile],
    tmux_session: &str,
) -> Option<&'a StatusLineFile> {
    files.iter().find(|s| s.tmux_session == tmux_session)
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
            model: None,
            last_tool: None,
            current_task: None,
            session_start_ts: None,
            input_tokens: None,
            output_tokens: None,
            cache_creation_input_tokens: None,
            cache_read_input_tokens: None,
            stop_reason: None,
            inflight_tool_count: None,
            state_changed_at: None,
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

    // -- StatusLineFile / statusline_for_session ---

    fn make_statusline_file(tmux_session: &str) -> StatusLineFile {
        StatusLineFile {
            tmux_session: tmux_session.to_string(),
            context_window_pct: Some(42.5),
            cost_usd: Some(0.25),
            total_duration_ms: Some(60000),
            rate_limit_five_hour_used_pct: Some(10.0),
            rate_limit_five_hour_resets_at: Some("2026-04-13T00:00:00Z".to_string()),
            rate_limit_seven_day_used_pct: Some(5.0),
            rate_limit_seven_day_resets_at: Some("2026-04-18T00:00:00Z".to_string()),
        }
    }

    #[test]
    fn statusline_for_session_finds_matching_entry() {
        let files = vec![
            make_statusline_file("repo_47_claude"),
            make_statusline_file("repo_48_main"),
        ];
        let found = statusline_for_session(&files, "repo_47_claude");
        assert!(found.is_some());
        assert_eq!(found.unwrap().tmux_session, "repo_47_claude");
    }

    #[test]
    fn statusline_for_session_returns_none_when_not_found() {
        let files = vec![make_statusline_file("repo_48_main")];
        let found = statusline_for_session(&files, "repo_47_claude");
        assert!(found.is_none());
    }

    #[test]
    fn statusline_file_deserializes_with_all_optional_fields_missing() {
        let json = r#"{"tmux_session":"repo_47"}"#;
        let sl: StatusLineFile = serde_json::from_str(json).unwrap();
        assert_eq!(sl.tmux_session, "repo_47");
        assert!(sl.context_window_pct.is_none());
        assert!(sl.cost_usd.is_none());
        assert!(sl.total_duration_ms.is_none());
        assert!(sl.rate_limit_five_hour_used_pct.is_none());
        assert!(sl.rate_limit_five_hour_resets_at.is_none());
        assert!(sl.rate_limit_seven_day_used_pct.is_none());
        assert!(sl.rate_limit_seven_day_resets_at.is_none());
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
            "model": "claude-opus-4-6",
            "last_tool": "Bash",
            "current_task": "fix the bug",
            "session_start_ts": 1700000000,
            "input_tokens": 1000,
            "output_tokens": 50
        }"#;
        let sf: ClaudeStateFile = serde_json::from_str(json).unwrap();
        assert_eq!(sf.state, "working");
        assert_eq!(sf.model.as_deref(), Some("claude-opus-4-6"));
        assert_eq!(sf.last_tool.as_deref(), Some("Bash"));
        assert_eq!(sf.current_task.as_deref(), Some("fix the bug"));
        assert_eq!(sf.session_start_ts, Some(1700000000));
        assert_eq!(sf.input_tokens, Some(1000));
        assert_eq!(sf.output_tokens, Some(50));
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
        assert!(sf.model.is_none());
        assert!(sf.last_tool.is_none());
        assert!(sf.current_task.is_none());
        assert!(sf.session_start_ts.is_none());
        assert!(sf.input_tokens.is_none());
    }

    #[test]
    fn state_file_deserializes_stop_reason_and_inflight_tool_count() {
        let json = r#"{
            "state": "idle",
            "session_id": "abc",
            "tmux_session": "repo_47",
            "cwd": "/workspace",
            "event": "Stop",
            "timestamp": "2026-03-25T10:00:00Z",
            "stop_reason": "end_turn",
            "inflight_tool_count": 0
        }"#;
        let sf: ClaudeStateFile = serde_json::from_str(json).unwrap();
        assert_eq!(sf.stop_reason.as_deref(), Some("end_turn"));
        assert_eq!(sf.inflight_tool_count, Some(0));
    }

    #[test]
    fn state_file_stop_reason_and_inflight_tool_count_default_to_none() {
        let json = r#"{
            "state": "working",
            "session_id": "abc",
            "tmux_session": "repo_47",
            "cwd": "/workspace",
            "event": "PreToolUse",
            "timestamp": "2026-03-25T10:00:00Z"
        }"#;
        let sf: ClaudeStateFile = serde_json::from_str(json).unwrap();
        assert!(sf.stop_reason.is_none());
        assert!(sf.inflight_tool_count.is_none());
    }

    #[test]
    fn state_file_deserializes_state_changed_at() {
        let json = r#"{
            "state": "working",
            "session_id": "abc",
            "tmux_session": "repo_47",
            "cwd": "/workspace",
            "event": "PreToolUse",
            "timestamp": "2026-04-13T10:00:00Z",
            "state_changed_at": "2026-04-13T10:00:00Z"
        }"#;
        let sf: ClaudeStateFile = serde_json::from_str(json).unwrap();
        assert_eq!(sf.state_changed_at.as_deref(), Some("2026-04-13T10:00:00Z"));
    }

    #[test]
    fn state_file_state_changed_at_defaults_to_none_when_absent() {
        let json = r#"{
            "state": "idle",
            "session_id": "abc",
            "tmux_session": "repo_47",
            "cwd": "/workspace",
            "event": "Stop",
            "timestamp": "2026-04-13T10:00:00Z"
        }"#;
        let sf: ClaudeStateFile = serde_json::from_str(json).unwrap();
        assert!(sf.state_changed_at.is_none());
    }
}
