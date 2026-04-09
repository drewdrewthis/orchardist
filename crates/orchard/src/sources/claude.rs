//! Claude Code hook state file reader.
//!
//! Reads `orchard-claude-*.json` files from the system temp directory, written by the
//! orchard-state.sh tmux hook. Provides Claude session state (working/idle/input) merged
//! into the workspace model.

/// Reads all orchard hook state files from the system temp directory.
pub fn read_state_files() -> Vec<crate::claude_state::ClaudeStateFile> {
    crate::claude_state::read_all_state_files(&std::env::temp_dir())
}
