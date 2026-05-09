//! `orchard chat history <room> [-n N]`.

use std::process::ExitCode;

use clap::Args as ClapArgs;

#[derive(ClapArgs, Debug)]
pub struct Args {
    pub room: String,

    /// Limit to the last N message-type rows (0 = all). Defaults to 50.
    #[arg(short = 'n', long = "limit", default_value_t = 50)]
    pub limit: usize,

    #[arg(short = 'j', long = "json")]
    pub json: bool,
}

pub fn run(args: Args) -> ExitCode {
    let room = args.room.trim_start_matches('#').to_string();
    let messages = match chat_core::read_history(&room, args.limit) {
        Ok(m) => m,
        Err(e) => {
            eprintln!("orchard chat history: {e:#}");
            return ExitCode::from(2);
        }
    };
    if args.json {
        match serde_json::to_string(&messages) {
            Ok(s) => println!("{s}"),
            Err(e) => {
                eprintln!("orchard chat history: serializing: {e}");
                return ExitCode::from(2);
            }
        }
    } else if messages.is_empty() {
        println!("(no messages in #{room})");
    } else {
        for m in &messages {
            println!("[{}] {}: {}", m.ts, m.sender, m.text);
        }
    }
    ExitCode::SUCCESS
}
