//! `orchard chat join <room>`.

use std::process::ExitCode;

use clap::Args as ClapArgs;

use crate::cmd::sender;

#[derive(ClapArgs, Debug)]
pub struct Args {
    /// Room name (with or without leading `#`).
    pub room: String,

    /// Override the joining handle (otherwise auto-derived from `$TMUX`).
    #[arg(long = "as", value_name = "HANDLE")]
    pub as_handle: Option<String>,

    /// Override the tmux session name (defaults to `$TMUX` session).
    #[arg(long = "session")]
    pub session: Option<String>,

    /// JSON output.
    #[arg(short = 'j', long = "json")]
    pub json: bool,
}

pub fn run(args: Args) -> ExitCode {
    let room = args.room.trim_start_matches('#').to_string();
    if room.is_empty() {
        eprintln!("orchard chat join: room name required");
        return ExitCode::from(3);
    }
    let handle = match sender::resolve(args.as_handle.as_deref()) {
        Ok(h) => h,
        Err(e) => {
            eprintln!("orchard chat join: {e}");
            return ExitCode::from(3);
        }
    };
    let session = args
        .session
        .or_else(current_tmux_session)
        .unwrap_or_else(|| handle.trim_start_matches('@').to_string());
    let machine = chat_core::current_machine();
    if let Err(e) = chat_core::join(&room, &handle, &machine, &session) {
        eprintln!("orchard chat join: {e:#}");
        return ExitCode::from(2);
    }
    if args.json {
        println!(
            "{}",
            serde_json::json!({
                "kind": "joined",
                "room": room,
                "handle": handle,
                "machine": machine,
                "tmux_session": session,
            })
        );
    } else {
        println!("joined #{room} as {handle} (tmux: {session})");
    }
    ExitCode::SUCCESS
}

fn current_tmux_session() -> Option<String> {
    std::env::var_os("TMUX")?;
    let out = std::process::Command::new("tmux")
        .args(["display-message", "-p", "#S"])
        .output()
        .ok()?;
    if !out.status.success() {
        return None;
    }
    let s = String::from_utf8_lossy(&out.stdout).trim().to_string();
    if s.is_empty() { None } else { Some(s) }
}
