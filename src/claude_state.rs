use serde::{Deserialize, Serialize};

/// State written by the orchard-state.sh hook script.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ClaudeStateFile {
    pub state: String,           // "working", "idle", "input"
    pub session_id: String,
    pub tmux_session: String,
    pub cwd: String,
    pub event: String,
    pub timestamp: String,       // ISO 8601
    // Optional enrichment from statusline
    #[serde(default)]
    pub context_window_pct: Option<f64>,
    #[serde(default)]
    pub cost_usd: Option<f64>,
    #[serde(default)]
    pub model: Option<String>,
}

/// Parsed Claude state for use in derive logic.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum ClaudeState {
    Working,
    Idle,
    Input,
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

/// Reads all orchard hook state files from the given directory (defaults to `/tmp`).
///
/// Only files matching `orchard-claude-*.json` in `dir` are read.
/// Malformed files and in-progress writes (`.tmp.`) are silently skipped.
pub fn read_all_state_files_in(dir: &str) -> Vec<ClaudeStateFile> {
    let pattern = format!("{}/orchard-claude-*.json", dir);
    let mut results = Vec::new();

    for entry in glob::glob(&pattern).into_iter().flatten() {
        if let Ok(path) = entry {
            // Skip .tmp files (in-progress atomic writes)
            if path.to_string_lossy().contains(".tmp.") {
                continue;
            }
            match std::fs::read(&path) {
                Ok(data) => {
                    match serde_json::from_slice::<ClaudeStateFile>(&data) {
                        Ok(state) => results.push(state),
                        Err(_) => {} // Skip malformed files
                    }
                }
                Err(_) => {} // Skip unreadable files
            }
        }
    }

    results
}

/// Convenience wrapper that reads state files from `/tmp`.
pub fn read_all_state_files() -> Vec<ClaudeStateFile> {
    read_all_state_files_in("/tmp")
}

/// Finds the state for a specific tmux session name.
pub fn state_for_session<'a>(states: &'a [ClaudeStateFile], tmux_session: &str) -> Option<&'a ClaudeStateFile> {
    states.iter().find(|s| s.tmux_session == tmux_session)
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
    fn claude_state_parse_working() {
        assert_eq!("working".parse::<ClaudeState>().unwrap(), ClaudeState::Working);
    }

    #[test]
    fn claude_state_parse_idle() {
        assert_eq!("idle".parse::<ClaudeState>().unwrap(), ClaudeState::Idle);
    }

    #[test]
    fn claude_state_parse_input() {
        assert_eq!("input".parse::<ClaudeState>().unwrap(), ClaudeState::Input);
    }

    #[test]
    fn claude_state_parse_unknown_is_none() {
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
    fn read_all_state_files_in_skips_malformed() {
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("orchard-claude-test.json");
        std::fs::write(&path, b"not json").unwrap();
        let results = read_all_state_files_in(dir.path().to_str().unwrap());
        assert!(results.is_empty(), "malformed files should be skipped");
    }

    #[test]
    fn read_all_state_files_in_reads_valid_files() {
        let dir = tempfile::tempdir().unwrap();
        let state = make_state_file("working", "repo_47");
        let json = serde_json::to_string(&state).unwrap();
        std::fs::write(dir.path().join("orchard-claude-repo_47.json"), json).unwrap();
        let results = read_all_state_files_in(dir.path().to_str().unwrap());
        assert_eq!(results.len(), 1);
        assert_eq!(results[0].state, "working");
    }

    #[test]
    fn read_all_state_files_in_skips_tmp_files() {
        let dir = tempfile::tempdir().unwrap();
        let state = make_state_file("working", "repo_47");
        let json = serde_json::to_string(&state).unwrap();
        // Write as a .tmp. file (in-progress atomic write)
        std::fs::write(dir.path().join("orchard-claude-repo_47.json.tmp.12345"), json).unwrap();
        let results = read_all_state_files_in(dir.path().to_str().unwrap());
        assert!(results.is_empty(), ".tmp files should be skipped");
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
