//! `orchard-chat` — agent-to-agent chat CLI.
//!
//! Thin clap wrapper on [`chat_core`]. Backs the `orchard chat <verb>` and
//! `orchard send <target> <text>` verbs in the orchard dispatcher (per
//! ADR-013 + #495).
//!
//! # Exit codes
//!
//! Stable per #495 AC-12:
//!
//! | Code | Meaning |
//! |------|---------|
//! | 0    | Full success — message landed AND fanout fully verified |
//! | 1    | Partial — message landed, some fanout `ByteOnly`/`Failed`/`Skipped` |
//! | 2    | Message did NOT land (append failure) |
//! | 3    | Usage error (no sender resolvable, bad target, missing arg) |
//!
//! Per-recipient detail prints to stderr on partial. JSON mode emits the
//! full `SendOutcome` shape on stdout.

use std::process::ExitCode;

use clap::Parser;

mod cmd;

#[derive(Parser, Debug)]
#[command(
    name = "orchard-chat",
    about = "Agent-to-agent chat: send to #room or @handle via tmux send-keys + JSONL receipts.",
    version
)]
struct Cli {
    #[command(subcommand)]
    command: Command,
}

#[derive(Parser, Debug)]
enum Command {
    /// Send a message to a `#room` (broadcast) or `@handle` (direct).
    Send(cmd::send::Args),

    /// Join a room (appends a `member.joined` event to the room's JSONL).
    Join(cmd::join::Args),

    /// Leave a room (appends a `member.left` event).
    Leave(cmd::leave::Args),

    /// List current members of a room.
    Members(cmd::members::Args),

    /// List all rooms (each `<name>.jsonl` in the chat dir).
    List(cmd::list::Args),

    /// Print the last N messages of a room.
    History(cmd::history::Args),

    /// Print the full history of a room, then follow new appends.
    Tail(cmd::tail::Args),
}

fn main() -> ExitCode {
    let cli = Cli::parse();
    match cli.command {
        Command::Send(args) => cmd::send::run(args),
        Command::Join(args) => cmd::join::run(args),
        Command::Leave(args) => cmd::leave::run(args),
        Command::Members(args) => cmd::members::run(args),
        Command::List(args) => cmd::list::run(args),
        Command::History(args) => cmd::history::run(args),
        Command::Tail(args) => cmd::tail::run(args),
    }
}
