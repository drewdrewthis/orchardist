//! Read Claude Code conversation transcripts (`.jsonl`) for the chat
//! viewer in the GUI.
//!
//! The daemon's `Conversation.jsonlPath` field gives us the absolute path;
//! this module is the read-side primitive that gets the bytes back to
//! the renderer. We tail from the end of the file (one big read), cap
//! the byte count to bound RAM, and let the GUI parse the JSONL itself —
//! we don't need to understand Claude's schema here.

use std::fs::File;
use std::io::{Read, Seek, SeekFrom};

const DEFAULT_MAX_BYTES: u64 = 512 * 1024;
const HARD_MAX_BYTES: u64 = 4 * 1024 * 1024;

#[derive(Debug, serde::Serialize)]
pub struct TranscriptChunk {
    pub path: String,
    pub size: u64,
    /// True when `size > requested max` and we returned only the tail.
    pub truncated: bool,
    pub text: String,
}

/// Read up to `max_bytes` from the *end* of the file at `path`. If the
/// file is larger, the prefix is dropped (the renderer cares about the
/// most recent turns). The caller does line splitting + JSON parsing.
#[tauri::command]
pub fn read_transcript_jsonl(
    path: String,
    max_bytes: Option<u64>,
) -> Result<TranscriptChunk, String> {
    let cap = max_bytes.unwrap_or(DEFAULT_MAX_BYTES).min(HARD_MAX_BYTES);

    let mut file = File::open(&path).map_err(|e| format!("open failed: {e}"))?;
    let total = file
        .metadata()
        .map(|m| m.len())
        .map_err(|e| format!("stat failed: {e}"))?;

    let (start, truncated) = if total > cap {
        (total - cap, true)
    } else {
        (0u64, false)
    };

    file.seek(SeekFrom::Start(start))
        .map_err(|e| format!("seek failed: {e}"))?;
    let mut buf = Vec::with_capacity((total - start) as usize);
    file.read_to_end(&mut buf)
        .map_err(|e| format!("read failed: {e}"))?;

    // If we truncated, the first partial line is junk — skip past the
    // first newline so the renderer sees only complete JSON lines.
    let mut text = if truncated {
        match buf.iter().position(|b| *b == b'\n') {
            Some(idx) => String::from_utf8_lossy(&buf[idx + 1..]).into_owned(),
            None => String::new(),
        }
    } else {
        String::from_utf8_lossy(&buf).into_owned()
    };
    if !text.ends_with('\n') {
        text.push('\n');
    }

    Ok(TranscriptChunk {
        path,
        size: total,
        truncated,
        text,
    })
}
