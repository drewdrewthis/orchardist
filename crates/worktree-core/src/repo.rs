//! Repository root and name resolution.
//!
//! Both functions are best-effort — they return an empty string on failure
//! rather than an `Err`. Callers depend on this contract; the TUI uses
//! `find_repo_root()` to display "[no repo]" when invoked outside a git tree.

use std::path::Path;
use std::process::Command;

/// Returns the absolute path of the git repository root, or an empty string on failure.
pub fn find_repo_root() -> String {
    Command::new("git")
        .args(["rev-parse", "--show-toplevel"])
        .output()
        .ok()
        .filter(|o| o.status.success())
        .map(|o| String::from_utf8_lossy(&o.stdout).trim().to_string())
        .unwrap_or_default()
}

/// Returns the directory name of the git repository root, or an empty string
/// when no repo is detected.
pub fn get_repo_name() -> String {
    let root = find_repo_root();
    Path::new(&root)
        .file_name()
        .and_then(|n| n.to_str())
        .unwrap_or("")
        .to_string()
}
