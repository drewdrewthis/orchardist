//! Cross-machine chat substrate.
//!
//! `chat-core` is the single source of truth for chat reads and writes. It
//! backs the `orchard-chat` CLI binary and any future TUI/GUI surface. Higher
//! layers compose on top — agents post via the CLI, daemons watch the JSONL
//! files, GUIs render histories. This library is local-first and pure-ish:
//! every public function reads or writes one JSONL file and may shell out to
//! `tmux` for fanout.
//!
//! # Substrate
//!
//! One JSONL file per room, at `${ORCHARD_CHAT_DIR}/<room>.jsonl` (default
//! `~/.orchard/chat/`). The file stores three event types discriminated by
//! a top-level `type` field:
//!
//! ```jsonl
//! {"type":"message","id":"01J..","ts":"...","sender":"@alice","sender_machine":"drew-mac","text":"hi","source":"internal"}
//! {"type":"member.joined","ts":"...","handle":"@alice","machine":"drew-mac","tmux_session":"card-alice"}
//! {"type":"member.left","ts":"...","handle":"@alice"}
//! ```
//!
//! Rooms exist iff their JSONL exists. Membership at time T is derived by
//! folding `member.joined`/`member.left` events chronologically (last-event-
//! wins per handle).
//!
//! # Concurrency
//!
//! POSIX `O_APPEND` is atomic for writes ≤ `PIPE_BUF` (4096 bytes on Linux,
//! 512 on macOS — both larger than our typical line). No flock anywhere.
//! Multiple writers on the same machine append-without-tearing; cross-machine
//! is not in scope for v1.
//!
//! # Receipts
//!
//! `tmux_fanout` returns Level 2 receipts: bytes-delivered AND scrollback-
//! verified via `tmux capture-pane -p`. Level 3 (recipient-acknowledged via
//! transcript watching) is deferred to a follow-up.
//!
//! # Identity
//!
//! Handles map directly to tmux session names (`@bob` → tmux session `bob`).
//! No registry. `derive_handle` slugifies a tmux session name to lowercase
//! alphanum+underscores, with collision suffixes.

pub mod fanout;
pub mod handle;
pub mod identity;
pub mod paths;
pub mod store;
pub mod transcript;
pub mod types;

pub use fanout::{FanoutOutcome, Recipient, VerifiedVia, tmux_fanout};
pub use handle::{derive_handle, derive_handle_with_collisions};
pub use identity::current_machine;
pub use paths::{chat_dir, room_path};
pub use store::{
    append_message, join, leave, list_members, list_rooms, read_history, send,
};
pub use types::{Event, Member, Message, MessageId, SendOutcome, Target};
