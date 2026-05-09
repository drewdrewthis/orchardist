//! `orchard chat leave <room>`.

use std::process::ExitCode;

use clap::Args as ClapArgs;

use crate::cmd::sender;

#[derive(ClapArgs, Debug)]
pub struct Args {
    pub room: String,

    #[arg(long = "as", value_name = "HANDLE")]
    pub as_handle: Option<String>,

    #[arg(short = 'j', long = "json")]
    pub json: bool,
}

pub fn run(args: Args) -> ExitCode {
    let room = args.room.trim_start_matches('#').to_string();
    if room.is_empty() {
        eprintln!("orchard chat leave: room name required");
        return ExitCode::from(3);
    }
    let handle = match sender::resolve(args.as_handle.as_deref()) {
        Ok(h) => h,
        Err(e) => {
            eprintln!("orchard chat leave: {e}");
            return ExitCode::from(3);
        }
    };
    if let Err(e) = chat_core::leave(&room, &handle) {
        eprintln!("orchard chat leave: {e:#}");
        return ExitCode::from(2);
    }
    if args.json {
        println!(
            "{}",
            serde_json::json!({
                "kind": "left",
                "room": room,
                "handle": handle,
            })
        );
    } else {
        println!("left #{room} as {handle}");
    }
    ExitCode::SUCCESS
}
