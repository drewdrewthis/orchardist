//! Events.jsonl tailer for the watch daemon.
//!
//! The tailer tracks a byte offset into events.jsonl and reads only new
//! lines on each poll. It filters for lines with `source="webhook"` and
//! forwards them to the daemon as triggers for an immediate refresh.
//!
//! Correctness rules (see specs/features/webhook-event-stream.feature):
//! - Read rule: `file_size > stored_offset OR mtime_changed`. mtime-only
//!   short-circuit is a BUG at 1s mtime resolution.
//! - Advance rule: offset advances only past the last complete newline.
//!   Partial trailing bytes are re-read on the next iteration.
//! - Truncation: if file_size < stored_offset, reset offset to 0.
//! - Cold start: initial offset = current file size. Historical lines are
//!   not replayed.
//! - Graceful degradation: missing file = silent; unreadable file = single
//!   warning, continue.

use std::fs::{self, OpenOptions};
use std::io::{Read, Seek, SeekFrom};
use std::path::PathBuf;
use std::time::SystemTime;

use serde_json::Value;

/// State for tailing events.jsonl across daemon loop iterations.
pub struct Tailer {
    path: PathBuf,
    offset: u64,
    mtime: Option<SystemTime>,
    /// Whether we've already logged the "unreadable" warning.
    /// Missing file is silent; unreadable existing file warns once.
    unreadable_warned: bool,
}

impl Tailer {
    /// Create a new tailer initialized to the CURRENT end of the file.
    /// Historical lines in events.jsonl at cold-start are NOT replayed.
    /// If the file does not exist, offset=0 and the first append will be read.
    pub fn new(path: PathBuf) -> Self {
        match fs::metadata(&path) {
            Ok(meta) => Tailer {
                offset: meta.len(),
                mtime: meta.modified().ok(),
                path,
                unreadable_warned: false,
            },
            Err(_) => Tailer {
                path,
                offset: 0,
                mtime: None,
                unreadable_warned: false,
            },
        }
    }

    /// Poll for new webhook lines. Returns a Vec of parsed webhook events
    /// (JSON Values with source="webhook"). Non-webhook lines and malformed
    /// JSON are silently skipped.
    ///
    /// Advances the internal offset only past the last complete newline;
    /// any trailing partial bytes are NOT consumed and will be retried.
    ///
    /// If the file is missing, returns an empty Vec silently.
    /// If the file exists but is unreadable, logs a single warning via
    /// the crate logger and returns an empty Vec.
    pub fn poll(&mut self) -> Vec<Value> {
        let meta = match fs::metadata(&self.path) {
            Ok(m) => m,
            Err(e) if e.kind() == std::io::ErrorKind::NotFound => {
                return Vec::new();
            }
            Err(_) => {
                if !self.unreadable_warned {
                    crate::logger::LOG.warn(&format!(
                        "tailer: cannot read metadata for {}",
                        self.path.display()
                    ));
                    self.unreadable_warned = true;
                }
                return Vec::new();
            }
        };

        let size = meta.len();
        let new_mtime = meta.modified().ok();

        // Decide whether to read: size grew OR mtime changed (but only if
        // we have a prior mtime — avoids spurious read on first cold-start
        // where mtime=None).
        let should_read = size > self.offset || (new_mtime != self.mtime && self.mtime.is_some());

        if !should_read {
            return Vec::new();
        }

        // Truncation guard: file shrank since last read.
        if size < self.offset {
            self.offset = 0;
        }

        let mut file = match OpenOptions::new().read(true).open(&self.path) {
            Ok(f) => f,
            Err(_) => {
                if !self.unreadable_warned {
                    crate::logger::LOG.warn(&format!(
                        "tailer: cannot open {} for reading",
                        self.path.display()
                    ));
                    self.unreadable_warned = true;
                }
                return Vec::new();
            }
        };

        if file.seek(SeekFrom::Start(self.offset)).is_err() {
            return Vec::new();
        }

        let mut bytes = Vec::new();
        if file.read_to_end(&mut bytes).is_err() {
            return Vec::new();
        }

        // Find the last newline — only consume complete lines.
        let last_nl = match bytes.iter().rposition(|&b| b == b'\n') {
            Some(pos) => pos,
            None => {
                // No complete lines; update mtime but don't advance offset.
                self.mtime = new_mtime;
                return Vec::new();
            }
        };

        let complete = &bytes[..=last_nl];
        let mut results = Vec::new();

        for line in complete.split(|&b| b == b'\n') {
            if line.is_empty() {
                continue;
            }
            if let Ok(v) = serde_json::from_slice::<Value>(line)
                && v.get("source") == Some(&Value::String("webhook".to_string()))
            {
                results.push(v);
            }
        }

        self.offset += (last_nl + 1) as u64;
        self.mtime = new_mtime;
        self.unreadable_warned = false;
        results
    }
}

#[cfg(test)]
#[path = "tailer_tests.rs"]
mod tests;
