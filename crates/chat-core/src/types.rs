//! Public type surface: events, messages, members, send/fanout outcomes.

use serde::{Deserialize, Serialize};

/// Routing target for a send: either a room broadcast (`#room`) or a direct
/// recipient (`@handle`).
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum Target {
    Room(String),
    Direct(String),
}

impl Target {
    /// Parse a target string. The leading sigil (`#` or `@`) disambiguates
    /// routing. Returns `None` if the string has no recognized sigil.
    pub fn parse(s: &str) -> Option<Self> {
        if let Some(rest) = s.strip_prefix('#') {
            if rest.is_empty() {
                None
            } else {
                Some(Target::Room(rest.to_string()))
            }
        } else if let Some(rest) = s.strip_prefix('@') {
            if rest.is_empty() {
                None
            } else {
                Some(Target::Direct(rest.to_string()))
            }
        } else {
            None
        }
    }

    /// The implicit "room name" used to durably store messages even for
    /// direct sends. For `@bob` we use the literal `@bob` as the room name
    /// so its history is queryable; for `#general` it's `general`.
    pub fn room_name(&self) -> String {
        match self {
            Target::Room(name) => name.clone(),
            Target::Direct(handle) => format!("@{handle}"),
        }
    }
}

/// A persisted message id (ULID).
pub type MessageId = String;

/// A user-visible message line in a room.
#[derive(Debug, Clone, Serialize, Deserialize, PartialEq, Eq)]
pub struct Message {
    pub id: MessageId,
    pub ts: String,
    pub sender: String,
    pub sender_machine: String,
    pub text: String,
    #[serde(default = "default_source")]
    pub source: String,
}

fn default_source() -> String {
    "internal".to_string()
}

/// A member of a room (derived from joined/left events).
#[derive(Debug, Clone, Serialize, Deserialize, PartialEq, Eq)]
pub struct Member {
    pub handle: String,
    pub machine: String,
    pub tmux_session: String,
    pub joined_at: String,
}

/// One JSONL row. Discriminated on `type`.
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(tag = "type", rename_all = "snake_case")]
pub enum Event {
    Message {
        id: MessageId,
        ts: String,
        sender: String,
        sender_machine: String,
        text: String,
        #[serde(default = "default_source")]
        source: String,
    },
    #[serde(rename = "member.joined")]
    MemberJoined {
        ts: String,
        handle: String,
        machine: String,
        tmux_session: String,
    },
    #[serde(rename = "member.left")]
    MemberLeft { ts: String, handle: String },
}

/// Outcome of a `send` call: the durable message id (proof it landed in
/// JSONL) plus per-recipient fanout outcomes.
#[derive(Debug, Clone, Serialize)]
pub struct SendOutcome {
    pub message_id: MessageId,
    pub room: String,
    pub fanout: Vec<crate::FanoutOutcome>,
}
