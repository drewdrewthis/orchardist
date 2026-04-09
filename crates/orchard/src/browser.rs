//! System browser launcher.
//!
//! Thin wrapper around the `open` crate that opens a URL in the default
//! system browser. Used by the TUI to open PR and issue URLs on keypress.
/// Opens `url` in the system default browser. Fire-and-forget; errors are silently ignored.
pub fn open_url(url: &str) {
    let _ = open::that(url);
}
