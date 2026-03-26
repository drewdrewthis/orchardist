/// Reads all orchard hook state files from /tmp.
pub fn read_state_files() -> Vec<crate::claude_state::ClaudeStateFile> {
    crate::claude_state::read_all_state_files("/tmp")
}
