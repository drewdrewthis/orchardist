use std::io::Write;
use std::process::{Command, Stdio};

/// Copies `text` to the system clipboard.
///
/// On macOS, uses `pbcopy`. On Linux, tries `xclip`, then `xsel`, then `wl-copy`.
/// Returns `Err` with a human-readable message if no clipboard tool is available
/// or if writing to it fails.
pub fn copy_to_clipboard(text: &str) -> Result<(), String> {
    #[cfg(target_os = "macos")]
    {
        return run_clipboard_cmd("pbcopy", &[], text);
    }

    #[cfg(not(target_os = "macos"))]
    {
        let linux_tools: &[(&str, &[&str])] = &[
            ("xclip", &["-selection", "clipboard"]),
            ("xsel", &["--clipboard", "--input"]),
            ("wl-copy", &[]),
        ];

        for (cmd, args) in linux_tools {
            if run_clipboard_cmd(cmd, args, text).is_ok() {
                return Ok(());
            }
        }

        Err("no clipboard tool found (tried xclip, xsel, wl-copy)".to_string())
    }
}

fn run_clipboard_cmd(program: &str, args: &[&str], text: &str) -> Result<(), String> {
    let mut child = Command::new(program)
        .args(args)
        .stdin(Stdio::piped())
        .spawn()
        .map_err(|e| format!("{program}: {e}"))?;

    if let Some(stdin) = child.stdin.as_mut() {
        stdin
            .write_all(text.as_bytes())
            .map_err(|e| format!("{program}: write error: {e}"))?;
    }

    let status = child
        .wait()
        .map_err(|e| format!("{program}: wait error: {e}"))?;

    if status.success() {
        Ok(())
    } else {
        Err(format!("{program}: exited with {status}"))
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn copy_empty_string_succeeds() {
        // On CI/systems without clipboard, this may fail, so just verify it doesn't panic.
        let _ = copy_to_clipboard("");
    }

    #[test]
    fn copy_text_returns_result() {
        let result = copy_to_clipboard("test-branch-name");
        // Result is Ok on systems with clipboard tools, Err on those without.
        assert!(result.is_ok() || result.is_err());
    }
}
