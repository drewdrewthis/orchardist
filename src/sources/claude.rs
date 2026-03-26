//! Claude Code hook state file reader.
//!
//! Reads `/tmp/orchard-claude-*.json` files written by the orchard-state.sh tmux hook.
//! Provides Claude session state (working/idle/input) merged into the workspace model.

/// Reads all orchard hook state files from /tmp.
pub fn read_state_files() -> Vec<crate::claude_state::ClaudeStateFile> {
    crate::claude_state::read_all_state_files("/tmp")
}
