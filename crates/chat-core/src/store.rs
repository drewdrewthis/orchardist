//! JSONL store: append messages, append membership events, fold to current
//! state.
//!
//! All writes use POSIX `O_APPEND` atomicity. Reads are streaming line scans
//! — no in-memory index, fold-on-read. At our scale (rooms with hundreds to
//! low thousands of lines), this is fast enough; if it ever isn't, that's a
//! tomorrow problem.

use std::fs::OpenOptions;
use std::io::{BufRead, BufReader, Write};
use std::path::Path;

use anyhow::{Context, Result};
use ulid::Ulid;

use crate::fanout::{Recipient, tmux_fanout};
use crate::identity::current_machine;
use crate::paths::{chat_dir, room_path};
use crate::types::{Event, Member, Message, MessageId, SendOutcome, Target};

/// Compose: append the message to the room's JSONL, then fan out via tmux.
///
/// The JSONL append is the durable receipt — if it succeeds and fanout
/// fails for some recipients, the message is still in history and replays
/// will redeliver. Callers MUST NOT retry on partial failure; the message
/// id is the same and recipients who already received would double up.
///
/// `sender` is required — caller has already resolved the sender's handle
/// (e.g. from `$TMUX` or `--as`). For room broadcasts, the recipient list
/// is derived from current membership minus the sender. For direct sends,
/// the recipient is the single tmux session matching the handle.
pub fn send(
    target: &Target,
    sender: &str,
    text: &str,
) -> Result<SendOutcome> {
    let room = target.room_name();
    let id = append_message(&room, sender, text)?;

    let recipients: Vec<Recipient> = match target {
        Target::Room(r) => list_members(r)?
            .into_iter()
            .filter(|m| m.handle != sender)
            .map(|m| Recipient::new(m.handle, m.tmux_session))
            .collect(),
        // Direct send: handle == tmux session name (no slugify); the sigil
        // is decoration. `@bob` → tmux session `bob`.
        Target::Direct(handle) => {
            let session = handle.trim_start_matches('@').to_string();
            vec![Recipient::new(format!("@{session}"), session)]
        }
    };

    let outcomes = tmux_fanout(&recipients, sender, text);
    Ok(SendOutcome {
        message_id: id,
        room,
        fanout: outcomes,
    })
}

/// Append a `type: "message"` row and return its ULID.
///
/// `ts` is RFC3339 with milliseconds; `sender_machine` is auto-stamped from
/// `gethostname`.
pub fn append_message(room: &str, sender: &str, text: &str) -> Result<MessageId> {
    let id = Ulid::new().to_string();
    let ts = now_rfc3339();
    let event = Event::Message {
        id: id.clone(),
        ts,
        sender: sender.to_string(),
        sender_machine: current_machine(),
        text: text.to_string(),
        source: "internal".to_string(),
    };
    append_event(room, &event)?;
    Ok(id)
}

/// Append a `member.joined` event.
pub fn join(
    room: &str,
    handle: &str,
    machine: &str,
    tmux_session: &str,
) -> Result<()> {
    let event = Event::MemberJoined {
        ts: now_rfc3339(),
        handle: handle.to_string(),
        machine: machine.to_string(),
        tmux_session: tmux_session.to_string(),
    };
    append_event(room, &event)
}

/// Append a `member.left` event.
pub fn leave(room: &str, handle: &str) -> Result<()> {
    let event = Event::MemberLeft {
        ts: now_rfc3339(),
        handle: handle.to_string(),
    };
    append_event(room, &event)
}

/// Read messages from a room, newest-last, capped at `limit`. Membership
/// events do not count toward the limit.
pub fn read_history(room: &str, limit: usize) -> Result<Vec<Message>> {
    let path = room_path(room)?;
    if !path.exists() {
        return Ok(Vec::new());
    }
    let mut messages = Vec::new();
    let f = std::fs::File::open(&path)
        .with_context(|| format!("opening {}", path.display()))?;
    for line in BufReader::new(f).lines() {
        let line = line?;
        if line.trim().is_empty() {
            continue;
        }
        if let Ok(Event::Message {
            id,
            ts,
            sender,
            sender_machine,
            text,
            source,
        }) = serde_json::from_str::<Event>(&line)
        {
            messages.push(Message {
                id,
                ts,
                sender,
                sender_machine,
                text,
                source,
            });
        }
        // Membership events and malformed lines silently skipped.
    }
    if limit > 0 && messages.len() > limit {
        let start = messages.len() - limit;
        messages = messages.split_off(start);
    }
    Ok(messages)
}

/// Derive current membership by folding joined/left events chronologically.
///
/// Last-event-wins per handle. A re-join after a leave puts the handle back.
/// Output is sorted by handle for stable display.
pub fn list_members(room: &str) -> Result<Vec<Member>> {
    let path = room_path(room)?;
    if !path.exists() {
        return Ok(Vec::new());
    }
    use std::collections::HashMap;
    let mut state: HashMap<String, Option<Member>> = HashMap::new();
    let f = std::fs::File::open(&path)
        .with_context(|| format!("opening {}", path.display()))?;
    for line in BufReader::new(f).lines() {
        let line = line?;
        if line.trim().is_empty() {
            continue;
        }
        match serde_json::from_str::<Event>(&line) {
            Ok(Event::MemberJoined {
                ts,
                handle,
                machine,
                tmux_session,
            }) => {
                state.insert(
                    handle.clone(),
                    Some(Member {
                        handle,
                        machine,
                        tmux_session,
                        joined_at: ts,
                    }),
                );
            }
            Ok(Event::MemberLeft { handle, .. }) => {
                state.insert(handle, None);
            }
            _ => {}
        }
    }
    let mut members: Vec<Member> = state.into_values().flatten().collect();
    members.sort_by(|a, b| a.handle.cmp(&b.handle));
    Ok(members)
}

/// List all rooms (each `<name>.jsonl` in [`chat_dir`]).
///
/// Returns just the room names, no extension.
pub fn list_rooms() -> Result<Vec<String>> {
    let dir = chat_dir()?;
    let mut rooms = Vec::new();
    if !dir.exists() {
        return Ok(rooms);
    }
    for entry in std::fs::read_dir(&dir)? {
        let entry = entry?;
        let path = entry.path();
        if path.extension().and_then(|s| s.to_str()) == Some("jsonl") {
            if let Some(stem) = path.file_stem().and_then(|s| s.to_str()) {
                rooms.push(stem.to_string());
            }
        }
    }
    rooms.sort();
    Ok(rooms)
}

fn append_event(room: &str, event: &Event) -> Result<()> {
    let path = room_path(room)?;
    if let Some(parent) = path.parent() {
        std::fs::create_dir_all(parent).ok();
    }
    let line = serde_json::to_string(event)
        .context("serializing event to JSON")?;
    append_line(&path, &line)
}

/// Append `line + \n` to `path` using `O_APPEND`. Caller is responsible for
/// keeping the line under PIPE_BUF (4096 on Linux, 512 on macOS) for atomicity.
fn append_line(path: &Path, line: &str) -> Result<()> {
    let mut file = OpenOptions::new()
        .append(true)
        .create(true)
        .open(path)
        .with_context(|| format!("opening {} for append", path.display()))?;
    let mut buf = String::with_capacity(line.len() + 1);
    buf.push_str(line);
    buf.push('\n');
    file.write_all(buf.as_bytes())
        .with_context(|| format!("appending to {}", path.display()))?;
    Ok(())
}

fn now_rfc3339() -> String {
    use time::format_description::well_known::Rfc3339;
    use time::OffsetDateTime;
    OffsetDateTime::now_utc()
        .format(&Rfc3339)
        .unwrap_or_else(|_| "1970-01-01T00:00:00Z".to_string())
}
