//! Tauri command bridges to `chat-core` + the local-file watcher that
//! emits `chat-message-appended` Tauri events on every room append.
//!
//! This is the v1 chat plane wiring path: the GUI talks directly to
//! `chat-core` via Tauri commands (Layer 1 of research/037 — stateless
//! library calls, no daemon required). A background `notify` watcher
//! tails `~/.orchard/chat/*.jsonl` and pushes new events to the
//! webview. The daemon-mediated path (per research/038's Go provider)
//! is a separate slice for cross-machine federation; not needed for
//! v1 single-machine.
//!
//! Shape:
//!
//!  - `chat_list_rooms`   → list rooms + per-room counts.
//!  - `chat_load_room`    → backfill messages + members for one room.
//!  - `chat_send`         → append via `chat-core`; fan out via
//!                          `tmux_fanout` with Level 2 receipts.
//!  - Tauri event `chat-message-appended` → fired on every newly-seen
//!    JSONL line. Payload tagged by `kind`:
//!      `{ kind: "message", room, line: { id, ts, sender, … } }`
//!      `{ kind: "member_joined", room, line: { handle, machine, … } }`
//!      `{ kind: "member_left", room, handle }`

use std::collections::HashMap;
use std::fs;
use std::path::{Path, PathBuf};
use std::sync::Mutex;
use std::time::Duration;

use chat_core::types::{Event, Member, Message, Target};
use chat_core::{
    append_message, derive_handle, list_members, list_rooms, paths, read_history, tmux_fanout,
};
use notify::{Event as NotifyEvent, EventKind, RecommendedWatcher, RecursiveMode, Watcher};
use serde::{Deserialize, Serialize};
use tauri::{AppHandle, Emitter, Manager, State};

/// Per-file byte offset state — the watcher resumes tail reads from
/// these positions on every fsnotify nudge.
#[derive(Default)]
pub struct ChatState {
    offsets: Mutex<HashMap<PathBuf, u64>>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ChatRoomSummary {
    pub id: String,
    pub message_count: usize,
    pub member_count: usize,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ChatRoomFull {
    pub id: String,
    pub messages: Vec<Message>,
    pub members: Vec<Member>,
}

#[derive(Debug, Clone, Serialize)]
#[serde(tag = "kind", rename_all = "snake_case")]
pub enum AppendedPayload {
    Message {
        room: String,
        line: Message,
    },
    #[serde(rename = "member_joined")]
    MemberJoined {
        room: String,
        line: Member,
    },
    #[serde(rename = "member_left")]
    MemberLeft {
        room: String,
        handle: String,
    },
}

#[tauri::command]
pub fn chat_list_rooms() -> Result<Vec<ChatRoomSummary>, String> {
    let rooms = list_rooms().map_err(|e| e.to_string())?;
    let mut out = Vec::with_capacity(rooms.len());
    for room in rooms {
        let history = read_history(&room, 0).unwrap_or_default();
        let members = list_members(&room).unwrap_or_default();
        out.push(ChatRoomSummary {
            id: room,
            message_count: history.len(),
            member_count: members.len(),
        });
    }
    Ok(out)
}

#[tauri::command]
pub fn chat_load_room(room: String) -> Result<ChatRoomFull, String> {
    let messages = read_history(&room, 0).map_err(|e| e.to_string())?;
    let members = list_members(&room).map_err(|e| e.to_string())?;
    Ok(ChatRoomFull {
        id: room,
        messages,
        members,
    })
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct SendOutcomeView {
    pub message_id: String,
    pub room: String,
    pub fanout: Vec<FanoutOutcomeView>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(tag = "kind", rename_all = "snake_case")]
pub enum FanoutOutcomeView {
    Delivered {
        recipient: String,
        scrollback_verified_at: String,
    },
    ByteOnly {
        recipient: String,
        reason: String,
    },
    Failed {
        recipient: String,
        error: String,
    },
    Skipped {
        recipient: String,
        reason: String,
    },
}

#[tauri::command]
pub fn chat_send(
    target: String,
    text: String,
    sender: Option<String>,
) -> Result<SendOutcomeView, String> {
    let target_parsed = Target::parse(&target)
        .ok_or_else(|| format!("invalid target {target:?} — expected `#room` or `@handle`"))?;
    let sender_resolved = match sender {
        Some(s) if !s.is_empty() => s,
        _ => default_sender_handle().ok_or_else(|| {
            "cannot derive sender (not in a tmux session) — supply explicit sender"
                .to_string()
        })?,
    };

    let room = target_parsed.room_name();

    // Append to JSONL store.
    let message_id = append_message(&room, &sender_resolved, &text).map_err(|e| e.to_string())?;

    // Resolve recipients per target.
    let recipients: Vec<String> = match &target_parsed {
        Target::Room(_) => list_members(&room)
            .map_err(|e| e.to_string())?
            .into_iter()
            .map(|m| m.handle)
            .collect(),
        Target::Direct(handle) => vec![format!("@{handle}")],
    };

    let fanout = tmux_fanout(&recipients, &sender_resolved, &text);
    let fanout_view: Vec<FanoutOutcomeView> = fanout
        .into_iter()
        .map(|fo| match fo {
            chat_core::FanoutOutcome::Delivered {
                recipient,
                scrollback_verified_at,
            } => FanoutOutcomeView::Delivered {
                recipient,
                scrollback_verified_at,
            },
            chat_core::FanoutOutcome::ByteOnly { recipient, reason } => {
                FanoutOutcomeView::ByteOnly { recipient, reason }
            }
            chat_core::FanoutOutcome::Failed { recipient, error } => {
                FanoutOutcomeView::Failed { recipient, error }
            }
            chat_core::FanoutOutcome::Skipped { recipient, reason } => {
                FanoutOutcomeView::Skipped { recipient, reason }
            }
        })
        .collect();

    Ok(SendOutcomeView {
        message_id,
        room,
        fanout: fanout_view,
    })
}

fn default_sender_handle() -> Option<String> {
    let tmux = std::env::var("TMUX").ok()?;
    if tmux.is_empty() {
        return None;
    }
    let session = std::process::Command::new("tmux")
        .args(["display-message", "-p", "#{session_name}"])
        .output()
        .ok()?;
    if !session.status.success() {
        return None;
    }
    let name = String::from_utf8_lossy(&session.stdout).trim().to_string();
    if name.is_empty() {
        None
    } else {
        Some(format!("@{}", derive_handle(&name, None)))
    }
}

/// Spawn a background `notify` watcher on the chat directory. Emits a
/// `chat-message-appended` Tauri event for every newly-appended JSONL
/// line. Tracks per-file byte offsets so each line is emitted exactly
/// once across the lifetime of the GUI.
pub fn spawn_watcher(app: AppHandle) {
    std::thread::spawn(move || {
        let state: State<'_, ChatState> = app.state();
        let dir = match paths::chat_dir() {
            Ok(d) => d,
            Err(err) => {
                eprintln!("[chat-watcher] could not resolve chat dir: {err}");
                return;
            }
        };
        // Initial offset hydration: skip past existing content so we
        // only emit *new* lines after the GUI loads.
        for entry in fs::read_dir(&dir).into_iter().flatten().flatten() {
            let path = entry.path();
            if path.extension().and_then(|s| s.to_str()) != Some("jsonl") {
                continue;
            }
            if let Ok(meta) = fs::metadata(&path) {
                state
                    .offsets
                    .lock()
                    .unwrap()
                    .insert(path.clone(), meta.len());
            }
        }

        let (tx, rx) = std::sync::mpsc::channel();
        let mut watcher = match RecommendedWatcher::new(
            move |res: Result<NotifyEvent, notify::Error>| {
                if let Ok(ev) = res {
                    let _ = tx.send(ev);
                }
            },
            notify::Config::default(),
        ) {
            Ok(w) => w,
            Err(err) => {
                eprintln!("[chat-watcher] could not create watcher: {err}");
                return;
            }
        };
        if let Err(err) = watcher.watch(&dir, RecursiveMode::NonRecursive) {
            eprintln!("[chat-watcher] watch {dir:?}: {err}");
            return;
        }

        loop {
            match rx.recv_timeout(Duration::from_secs(30)) {
                Ok(ev) => handle_event(&app, ev),
                Err(std::sync::mpsc::RecvTimeoutError::Timeout) => {
                    // Backstop scan in case fsnotify dropped events.
                    rescan_dir(&app, &dir);
                }
                Err(std::sync::mpsc::RecvTimeoutError::Disconnected) => break,
            }
        }
    });
}

fn handle_event(app: &AppHandle, ev: NotifyEvent) {
    match ev.kind {
        EventKind::Modify(_) | EventKind::Create(_) => {
            for path in ev.paths {
                if path.extension().and_then(|s| s.to_str()) != Some("jsonl") {
                    continue;
                }
                emit_new_lines(app, &path);
            }
        }
        _ => {}
    }
}

fn rescan_dir(app: &AppHandle, dir: &Path) {
    if let Ok(entries) = fs::read_dir(dir) {
        for entry in entries.flatten() {
            let path = entry.path();
            if path.extension().and_then(|s| s.to_str()) == Some("jsonl") {
                emit_new_lines(app, &path);
            }
        }
    }
}

fn emit_new_lines(app: &AppHandle, path: &Path) {
    let state: State<'_, ChatState> = app.state();
    let mut offsets = state.offsets.lock().unwrap();
    let last = *offsets.get(path).unwrap_or(&0);
    let meta = match fs::metadata(path) {
        Ok(m) => m,
        Err(_) => return,
    };
    let size = meta.len();
    if size <= last {
        offsets.insert(path.to_path_buf(), 0);
        return;
    }
    let bytes = match fs::read(path) {
        Ok(b) => b,
        Err(_) => return,
    };
    if (bytes.len() as u64) < size {
        return;
    }
    let new_chunk = &bytes[last as usize..];
    let mut consumed_to: u64 = last;
    for line in new_chunk.split(|b| *b == b'\n') {
        if line.is_empty() {
            consumed_to += 1; // newline byte
            continue;
        }
        let line_len = line.len() as u64;
        let line_end = consumed_to + line_len;
        if line_end + 1 > size {
            break;
        }
        consumed_to = line_end + 1;
        let room = path
            .file_stem()
            .and_then(|s| s.to_str())
            .unwrap_or("")
            .to_string();
        if let Ok(ev) = serde_json::from_slice::<Event>(line) {
            if let Some(payload) = event_to_payload(&room, ev) {
                let _ = app.emit("chat-message-appended", payload);
            }
        }
    }
    offsets.insert(path.to_path_buf(), consumed_to);
}

fn event_to_payload(room: &str, ev: Event) -> Option<AppendedPayload> {
    match ev {
        Event::Message {
            id,
            ts,
            sender,
            sender_machine,
            text,
            source,
        } => Some(AppendedPayload::Message {
            room: room.to_string(),
            line: Message {
                id,
                ts,
                sender,
                sender_machine,
                text,
                source,
            },
        }),
        Event::MemberJoined {
            ts,
            handle,
            machine,
            tmux_session,
        } => Some(AppendedPayload::MemberJoined {
            room: room.to_string(),
            line: Member {
                handle,
                machine,
                tmux_session,
                joined_at: ts,
            },
        }),
        Event::MemberLeft { handle, .. } => Some(AppendedPayload::MemberLeft {
            room: room.to_string(),
            handle,
        }),
    }
}
