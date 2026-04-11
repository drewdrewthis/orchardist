//! JSONL transcript enrichment for the `orchard hook-enrich` subcommand.
//!
//! Reads the tail of a Claude Code JSONL transcript file, finds the most
//! recent non-sidechain assistant message with `message.usage` and
//! `message.model`, and prints a JSON enrichment object to stdout. The
//! shell hook (`orchard-state.sh`) merges this output into the state file.
//!
//! # Tail-read bound
//!
//! To avoid loading entire 20 MB transcripts into memory, at most the last
//! 256 KB of the file is read. The first complete line boundary within that
//! window is found before parsing, so no line is ever split mid-JSON.
//!
//! # Exit behaviour
//!
//! On any error (missing file, empty file, parse failure, no usable message)
//! prints `{}` and exits 0. The hook must never fail.

use std::io::{Read, Seek, SeekFrom};
use std::path::Path;

use serde::Serialize;

/// Maximum number of bytes to read from the tail of the transcript.
const TAIL_BYTES: u64 = 256 * 1024;

/// The JSON enrichment object written to stdout.
#[derive(Debug, Serialize, Default)]
#[serde(rename_all = "camelCase")]
pub struct Enrichment {
    /// Model name from the most recent usable assistant message.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub model: Option<String>,
    /// Input token count from the most recent usable assistant message.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub input_tokens: Option<u64>,
    /// Output token count from the most recent usable assistant message.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub output_tokens: Option<u64>,
    /// Cache creation input tokens from the most recent usable message.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub cache_creation_input_tokens: Option<u64>,
    /// Cache read input tokens from the most recent usable message.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub cache_read_input_tokens: Option<u64>,
}

impl Enrichment {
    /// Returns `true` if no fields are populated.
    fn is_empty(&self) -> bool {
        self.model.is_none()
            && self.input_tokens.is_none()
            && self.output_tokens.is_none()
            && self.cache_creation_input_tokens.is_none()
            && self.cache_read_input_tokens.is_none()
    }
}

/// Reads the tail of the transcript at `path` and returns enrichment data.
///
/// Returns an empty `Enrichment` on any I/O or parse error.
pub fn enrich_from_transcript(path: &Path) -> Enrichment {
    match read_and_parse(path) {
        Ok(e) => e,
        Err(_) => Enrichment::default(),
    }
}

/// Runs the `hook-enrich` subcommand: reads transcript, prints JSON to stdout.
///
/// Called from `main.rs` when the user invokes `orchard hook-enrich --transcript <path>`.
pub fn run(transcript_path: &str) {
    let path = Path::new(transcript_path);
    let enrichment = enrich_from_transcript(path);
    if enrichment.is_empty() {
        println!("{{}}");
    } else {
        // Unwrap is safe: Enrichment is always serializable.
        println!("{}", serde_json::to_string(&enrichment).unwrap_or_else(|_| "{}".to_string()));
    }
}

// ---------------------------------------------------------------------------
// Internal parsing
// ---------------------------------------------------------------------------

fn read_and_parse(path: &Path) -> std::io::Result<Enrichment> {
    let mut file = std::fs::File::open(path)?;
    let file_len = file.seek(SeekFrom::End(0))?;

    if file_len == 0 {
        return Ok(Enrichment::default());
    }

    // Seek to max(0, file_len - TAIL_BYTES).
    let start = file_len.saturating_sub(TAIL_BYTES);
    file.seek(SeekFrom::Start(start))?;

    let mut buf = Vec::new();
    file.read_to_end(&mut buf)?;

    // If we didn't read from the beginning, skip to the first newline to
    // avoid a split mid-JSON line.
    let slice = if start > 0 {
        match buf.iter().position(|&b| b == b'\n') {
            Some(pos) => &buf[pos + 1..],
            None => &buf[..],
        }
    } else {
        &buf[..]
    };

    Ok(parse_enrichment(slice))
}

/// Parses JSONL bytes and returns enrichment from the last usable message.
///
/// "Usable" means: `type == "assistant"`, `isSidechain != true`,
/// `message.model` is present, and `message.usage` is present.
fn parse_enrichment(data: &[u8]) -> Enrichment {
    let text = match std::str::from_utf8(data) {
        Ok(s) => s,
        Err(_) => return Enrichment::default(),
    };

    let mut best: Option<Enrichment> = None;

    for line in text.lines() {
        let line = line.trim();
        if line.is_empty() {
            continue;
        }
        if let Some(e) = try_parse_line(line) {
            best = Some(e);
        }
    }

    best.unwrap_or_default()
}

/// Attempts to parse one JSONL line as a usable assistant message.
///
/// Returns `None` on parse failure or if the message doesn't qualify.
fn try_parse_line(line: &str) -> Option<Enrichment> {
    let v: serde_json::Value = serde_json::from_str(line).ok()?;

    // Must be type "assistant"
    if v.get("type")?.as_str()? != "assistant" {
        return None;
    }

    // Skip sidechain messages.
    if v.get("isSidechain").and_then(|b| b.as_bool()).unwrap_or(false) {
        return None;
    }

    let msg = v.get("message")?;
    let model = msg.get("model")?.as_str()?;
    let usage = msg.get("usage")?;

    let input_tokens = usage
        .get("input_tokens")
        .and_then(|v| v.as_u64());
    let output_tokens = usage
        .get("output_tokens")
        .and_then(|v| v.as_u64());
    let cache_creation = usage
        .get("cache_creation_input_tokens")
        .and_then(|v| v.as_u64());
    let cache_read = usage
        .get("cache_read_input_tokens")
        .and_then(|v| v.as_u64());

    // Require at least model to be present (usage fields can all be 0/missing).
    Some(Enrichment {
        model: Some(model.to_string()),
        input_tokens,
        output_tokens,
        cache_creation_input_tokens: cache_creation,
        cache_read_input_tokens: cache_read,
    })
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

#[cfg(test)]
mod tests {
    use super::*;
    use std::io::Write;
    use tempfile::NamedTempFile;

    fn write_jsonl(lines: &[&str]) -> NamedTempFile {
        let mut f = NamedTempFile::new().unwrap();
        for line in lines {
            writeln!(f, "{line}").unwrap();
        }
        f
    }

    // -----------------------------------------------------------------------
    // AC4: hook-enrich reads most recent assistant message
    // -----------------------------------------------------------------------

    /// AC4 scenario: reads the most recent assistant message with all usage fields.
    #[test]
    fn reads_most_recent_assistant_message() {
        let f = write_jsonl(&[
            r#"{"type":"user","message":{"role":"user","content":"hi"}}"#,
            r#"{"type":"assistant","message":{"role":"assistant","model":"claude-opus-4-6","usage":{"input_tokens":1000,"cache_creation_input_tokens":500,"cache_read_input_tokens":20000,"output_tokens":800}}}"#,
        ]);
        let e = enrich_from_transcript(f.path());
        assert_eq!(e.model.as_deref(), Some("claude-opus-4-6"));
        assert_eq!(e.input_tokens, Some(1000));
        assert_eq!(e.output_tokens, Some(800));
        assert_eq!(e.cache_creation_input_tokens, Some(500));
        assert_eq!(e.cache_read_input_tokens, Some(20000));
    }

    /// AC4 scenario: missing transcript file yields empty enrichment.
    #[test]
    fn missing_file_yields_empty() {
        let e = enrich_from_transcript(Path::new("/nonexistent/path/to/transcript.jsonl"));
        assert!(e.is_empty());
    }

    /// AC4 scenario: empty transcript file yields empty enrichment.
    #[test]
    fn empty_file_yields_empty() {
        let f = NamedTempFile::new().unwrap();
        let e = enrich_from_transcript(f.path());
        assert!(e.is_empty());
    }

    /// AC4 scenario: malformed JSONL lines are skipped; valid data is returned.
    #[test]
    fn skips_malformed_lines_and_returns_valid_data() {
        let f = write_jsonl(&[
            r#"{"type":"user","message":{"role":"user","content":"hi"}}"#,
            "not-valid-json",
            r#"{"type":"assistant","message":{"role":"assistant","model":"claude-opus-4-6","usage":{"input_tokens":100,"output_tokens":50}}}"#,
        ]);
        let e = enrich_from_transcript(f.path());
        assert_eq!(e.model.as_deref(), Some("claude-opus-4-6"));
        assert_eq!(e.input_tokens, Some(100));
    }

    /// AC4 scenario: no assistant messages yields empty enrichment.
    #[test]
    fn no_assistant_messages_yields_empty() {
        let f = write_jsonl(&[
            r#"{"type":"user","message":{"role":"user","content":"hi"}}"#,
            r#"{"type":"file-history-snapshot","files":[]}"#,
        ]);
        let e = enrich_from_transcript(f.path());
        assert!(e.is_empty());
    }

    /// AC4 scenario: sidechain messages are skipped; last non-sidechain wins.
    #[test]
    fn sidechain_messages_are_skipped() {
        let f = write_jsonl(&[
            r#"{"type":"assistant","isSidechain":false,"message":{"role":"assistant","model":"claude-opus-4-6","usage":{"input_tokens":100,"output_tokens":50}}}"#,
            r#"{"type":"assistant","isSidechain":true,"message":{"role":"assistant","model":"claude-haiku-4-5","usage":{"input_tokens":200,"output_tokens":10}}}"#,
        ]);
        let e = enrich_from_transcript(f.path());
        assert_eq!(e.model.as_deref(), Some("claude-opus-4-6"));
        assert_eq!(e.input_tokens, Some(100));
    }

    /// AC4 scenario: partial in-flight message with no usage falls back to the
    /// previous complete message.
    #[test]
    fn partial_message_falls_back_to_previous() {
        let f = write_jsonl(&[
            r#"{"type":"assistant","message":{"role":"assistant","model":"claude-opus-4-6","usage":{"input_tokens":500,"output_tokens":200}}}"#,
            r#"{"type":"assistant","message":{"role":"assistant","model":"claude-opus-4-6"}}"#,
        ]);
        let e = enrich_from_transcript(f.path());
        // The second message has no usage → skipped; first message is returned.
        assert_eq!(e.model.as_deref(), Some("claude-opus-4-6"));
        assert_eq!(e.input_tokens, Some(500));
    }

    /// AC4 scenario: transcript paths containing spaces and unicode are handled.
    #[test]
    fn unicode_and_space_paths_work() {
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("my session café.jsonl");
        let mut f = std::fs::File::create(&path).unwrap();
        writeln!(f, r#"{{"type":"assistant","message":{{"role":"assistant","model":"claude-opus-4-6","usage":{{"input_tokens":42,"output_tokens":7}}}}}}"#).unwrap();
        drop(f);
        let e = enrich_from_transcript(&path);
        assert_eq!(e.model.as_deref(), Some("claude-opus-4-6"));
        assert_eq!(e.input_tokens, Some(42));
    }

    /// AC4 scenario: large transcript (>256 KB) tail-reads only the bounded region.
    #[test]
    fn large_transcript_tail_reads_bounded_region() {
        use std::io::Write;
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("large.jsonl");
        let mut f = std::fs::File::create(&path).unwrap();

        // Write ~300 KB of padding lines (unreachable by tail-read).
        let padding_line = format!("{}\n", "x".repeat(200));
        let padding_count = (300 * 1024) / padding_line.len();
        for _ in 0..padding_count {
            f.write_all(padding_line.as_bytes()).unwrap();
        }

        // The final assistant message — will be within the last 256 KB.
        writeln!(f, r#"{{"type":"assistant","message":{{"role":"assistant","model":"claude-opus-4-6","usage":{{"input_tokens":9999,"output_tokens":1}}}}}}"#).unwrap();
        drop(f);

        let meta = std::fs::metadata(&path).unwrap();
        assert!(meta.len() > TAIL_BYTES, "test file must be > 256 KB");

        let e = enrich_from_transcript(&path);
        assert_eq!(e.model.as_deref(), Some("claude-opus-4-6"));
        assert_eq!(e.input_tokens, Some(9999));
    }

    // -----------------------------------------------------------------------
    // run() output format
    // -----------------------------------------------------------------------

    /// run() prints `{}` when no usable message is found.
    #[test]
    fn run_prints_empty_object_for_missing_file() {
        // Capture is awkward in tests; instead verify enrich_from_transcript + is_empty.
        let e = enrich_from_transcript(Path::new("/no/such/file.jsonl"));
        assert!(e.is_empty(), "missing file must produce empty enrichment");
    }

    // -----------------------------------------------------------------------
    // AC5: session_age_sec (tested via json_output helpers — see json_output.rs)
    // -----------------------------------------------------------------------

    // -----------------------------------------------------------------------
    // AC7: enrichment state matrix
    // -----------------------------------------------------------------------

    /// Fresh session — no assistant messages → empty enrichment.
    #[test]
    fn fresh_session_no_assistant_messages_yields_empty() {
        let f = write_jsonl(&[r#"{"type":"user","message":{"role":"user","content":"hello"}}"#]);
        let e = enrich_from_transcript(f.path());
        assert!(e.is_empty());
    }

    /// JSONL parse error — last line truncated; second-to-last valid data returned.
    #[test]
    fn truncated_last_line_falls_back_to_penultimate() {
        let f = write_jsonl(&[
            r#"{"type":"assistant","message":{"role":"assistant","model":"claude-opus-4-6","usage":{"input_tokens":77,"output_tokens":3}}}"#,
            r#"{"type":"assistant","message":{"role":"ass"#, // truncated
        ]);
        let e = enrich_from_transcript(f.path());
        assert_eq!(e.model.as_deref(), Some("claude-opus-4-6"));
        assert_eq!(e.input_tokens, Some(77));
    }
}
