//! PTY backend for the GUI's embedded terminal.
//!
//! Each `pty_spawn` call starts a real PTY child (typically `tmux
//! attach-session -t <name>` or `ssh host 'tmux attach -t <name>'`). A
//! reader thread streams stdout chunks back to the renderer as
//! `pty-data-<id>` Tauri events; `pty_write` and `pty_resize` go the
//! other way.
//!
//! The state object holds `(MasterPty, Writer)` keyed by id. Spawned
//! children are owned by a dedicated waiter thread that fires a
//! `pty-exit-<id>` event on exit and removes the entry.
//!
//! Shape kept minimal — anything fancier (multiplexing, history,
//! reconnect) belongs in a separate module.

use std::collections::HashMap;
use std::io::{Read, Write};
use std::sync::atomic::{AtomicU64, Ordering};
use std::sync::Mutex;

use portable_pty::{native_pty_system, CommandBuilder, MasterPty, PtySize};
use serde::{Deserialize, Serialize};
use tauri::{AppHandle, Emitter, Manager, State};

/// Per-session bridge: holds the PTY master + a writer to push bytes
/// into the child's stdin. The reader thread owns the read half via
/// `try_clone_reader`; the master is kept alive here so the FD stays open.
pub struct PtySession {
    master: Box<dyn MasterPty + Send>,
    writer: Box<dyn Write + Send>,
}

#[derive(Default)]
pub struct PtyState {
    sessions: Mutex<HashMap<u64, PtySession>>,
}

static NEXT_ID: AtomicU64 = AtomicU64::new(1);

/// JSON args to `pty_spawn`. `argv[0]` is the program; the rest are
/// arguments. `cwd` is optional; defaults to inheriting the daemon's cwd.
#[derive(Debug, Deserialize)]
pub struct SpawnArgs {
    pub argv: Vec<String>,
    pub cwd: Option<String>,
    pub cols: Option<u16>,
    pub rows: Option<u16>,
}

/// Returned to the GUI: the session id used for subsequent calls + event names.
#[derive(Debug, Serialize)]
pub struct SpawnResult {
    pub id: u64,
    pub data_event: String,
    pub exit_event: String,
}

/// Emitted on every output chunk from the PTY child.
#[derive(Debug, Clone, Serialize)]
pub struct DataEvent {
    pub id: u64,
    /// Raw bytes from the PTY, base64-encoded so binary/control sequences
    /// survive JSON without corruption.
    pub b64: String,
}

#[tauri::command]
pub fn pty_spawn(
    app: AppHandle,
    state: State<'_, PtyState>,
    args: SpawnArgs,
) -> Result<SpawnResult, String> {
    if args.argv.is_empty() {
        return Err("argv must be non-empty".into());
    }
    let cols = args.cols.unwrap_or(120);
    let rows = args.rows.unwrap_or(32);

    let pty_system = native_pty_system();
    let pair = pty_system
        .openpty(PtySize {
            rows,
            cols,
            pixel_width: 0,
            pixel_height: 0,
        })
        .map_err(|e| format!("openpty failed: {e}"))?;

    let mut cmd = CommandBuilder::new(&args.argv[0]);
    for a in args.argv.iter().skip(1) {
        cmd.arg(a);
    }
    if let Some(cwd) = args.cwd.as_ref() {
        cmd.cwd(cwd);
    }
    // Sensible defaults so terminal apps don't fall back to dumb mode.
    cmd.env("TERM", "xterm-256color");
    cmd.env("COLORTERM", "truecolor");

    let mut child = pair
        .slave
        .spawn_command(cmd)
        .map_err(|e| format!("spawn failed: {e}"))?;
    drop(pair.slave);

    let id = NEXT_ID.fetch_add(1, Ordering::Relaxed);
    let data_event = format!("pty-data-{id}");
    let exit_event = format!("pty-exit-{id}");

    let mut reader = pair
        .master
        .try_clone_reader()
        .map_err(|e| format!("clone reader failed: {e}"))?;
    let writer = pair
        .master
        .take_writer()
        .map_err(|e| format!("take writer failed: {e}"))?;

    state
        .sessions
        .lock()
        .unwrap()
        .insert(id, PtySession { master: pair.master, writer });

    let app_for_reader = app.clone();
    let data_event_clone = data_event.clone();
    std::thread::spawn(move || {
        let mut buf = [0u8; 8192];
        loop {
            match reader.read(&mut buf) {
                Ok(0) => break,
                Ok(n) => {
                    let chunk = &buf[..n];
                    let b64 = base64_encode(chunk);
                    let _ = app_for_reader.emit(&data_event_clone, DataEvent { id, b64 });
                }
                Err(_) => break,
            }
        }
    });

    let app_for_waiter = app.clone();
    let exit_event_clone = exit_event.clone();
    std::thread::spawn(move || {
        let exit_code = child.wait().map(|s| s.exit_code()).unwrap_or(255);
        let _ = app_for_waiter.emit(
            &exit_event_clone,
            serde_json::json!({ "id": id, "exitCode": exit_code }),
        );
        if let Some(state) = app_for_waiter.try_state::<PtyState>() {
            let _ = state.sessions.lock().unwrap().remove(&id);
        }
    });

    Ok(SpawnResult {
        id,
        data_event,
        exit_event,
    })
}

#[tauri::command]
pub fn pty_write(state: State<'_, PtyState>, id: u64, b64: String) -> Result<(), String> {
    let bytes = base64_decode(&b64).map_err(|e| format!("bad base64: {e}"))?;
    let mut sessions = state.sessions.lock().unwrap();
    let s = sessions
        .get_mut(&id)
        .ok_or_else(|| format!("no pty session {id}"))?;
    s.writer
        .write_all(&bytes)
        .map_err(|e| format!("write failed: {e}"))?;
    s.writer
        .flush()
        .map_err(|e| format!("flush failed: {e}"))?;
    Ok(())
}

#[tauri::command]
pub fn pty_resize(
    state: State<'_, PtyState>,
    id: u64,
    cols: u16,
    rows: u16,
) -> Result<(), String> {
    let sessions = state.sessions.lock().unwrap();
    let s = sessions
        .get(&id)
        .ok_or_else(|| format!("no pty session {id}"))?;
    s.master
        .resize(PtySize {
            rows,
            cols,
            pixel_width: 0,
            pixel_height: 0,
        })
        .map_err(|e| format!("resize failed: {e}"))?;
    Ok(())
}

#[tauri::command]
pub fn pty_kill(state: State<'_, PtyState>, id: u64) -> Result<(), String> {
    state.sessions.lock().unwrap().remove(&id);
    Ok(())
}

// --- minimal base64 helpers (no extra dep) ----------------------------------

const B64: &[u8; 64] = b"ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/";

fn base64_encode(input: &[u8]) -> String {
    let mut out = String::with_capacity((input.len() + 2) / 3 * 4);
    for chunk in input.chunks(3) {
        let b0 = chunk[0];
        let b1 = chunk.get(1).copied().unwrap_or(0);
        let b2 = chunk.get(2).copied().unwrap_or(0);
        out.push(B64[(b0 >> 2) as usize] as char);
        out.push(B64[(((b0 & 0b11) << 4) | (b1 >> 4)) as usize] as char);
        if chunk.len() > 1 {
            out.push(B64[(((b1 & 0b1111) << 2) | (b2 >> 6)) as usize] as char);
        } else {
            out.push('=');
        }
        if chunk.len() > 2 {
            out.push(B64[(b2 & 0b111111) as usize] as char);
        } else {
            out.push('=');
        }
    }
    out
}

fn base64_decode(input: &str) -> Result<Vec<u8>, String> {
    let mut lookup = [255u8; 256];
    for (i, c) in B64.iter().enumerate() {
        lookup[*c as usize] = i as u8;
    }
    let bytes: Vec<u8> = input.bytes().filter(|b| *b != b'\n' && *b != b'\r').collect();
    let mut out = Vec::with_capacity(bytes.len() / 4 * 3);
    for chunk in bytes.chunks(4) {
        if chunk.len() < 4 {
            return Err("truncated base64".into());
        }
        let v0 = if chunk[0] == b'=' { 0 } else { lookup[chunk[0] as usize] };
        let v1 = if chunk[1] == b'=' { 0 } else { lookup[chunk[1] as usize] };
        let v2 = if chunk[2] == b'=' { 0 } else { lookup[chunk[2] as usize] };
        let v3 = if chunk[3] == b'=' { 0 } else { lookup[chunk[3] as usize] };
        if v0 == 255 || v1 == 255 || (chunk[2] != b'=' && v2 == 255) || (chunk[3] != b'=' && v3 == 255) {
            return Err("invalid base64 character".into());
        }
        out.push((v0 << 2) | (v1 >> 4));
        if chunk[2] != b'=' {
            out.push((v1 << 4) | (v2 >> 2));
        }
        if chunk[3] != b'=' {
            out.push((v2 << 6) | v3);
        }
    }
    Ok(out)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn base64_roundtrip_ascii() {
        let raw = b"hello world";
        let encoded = base64_encode(raw);
        assert_eq!(encoded, "aGVsbG8gd29ybGQ=");
        assert_eq!(base64_decode(&encoded).unwrap(), raw);
    }

    #[test]
    fn base64_roundtrip_binary() {
        let raw: Vec<u8> = (0u8..=255).collect();
        let encoded = base64_encode(&raw);
        let decoded = base64_decode(&encoded).unwrap();
        assert_eq!(decoded, raw);
    }

    #[test]
    fn base64_roundtrip_ansi_escape() {
        // tmux output contains lots of these — make sure they survive.
        let raw = b"\x1b[1;31mred\x1b[0m\r\nnext line\n";
        let decoded = base64_decode(&base64_encode(raw)).unwrap();
        assert_eq!(decoded, raw);
    }
}
