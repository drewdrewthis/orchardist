//! Machine identity for `sender_machine` stamping.

/// Returns the current machine's hostname, lowercased and with the
/// `.local` suffix stripped (matching macOS conventions).
///
/// Falls back to `"unknown"` if the syscall fails.
pub fn current_machine() -> String {
    let raw = gethostname::gethostname()
        .into_string()
        .unwrap_or_else(|_| "unknown".to_string());
    let lower = raw.to_lowercase();
    lower.strip_suffix(".local").unwrap_or(&lower).to_string()
}
