//! `orchard chat list` — list all rooms (each `<name>.jsonl` in chat dir).

use std::process::ExitCode;

use clap::Args as ClapArgs;

#[derive(ClapArgs, Debug)]
pub struct Args {
    #[arg(short = 'j', long = "json")]
    pub json: bool,
}

pub fn run(args: Args) -> ExitCode {
    let rooms = match chat_core::list_rooms() {
        Ok(r) => r,
        Err(e) => {
            eprintln!("orchard chat list: {e:#}");
            return ExitCode::from(2);
        }
    };
    if args.json {
        match serde_json::to_string(&rooms) {
            Ok(s) => println!("{s}"),
            Err(e) => {
                eprintln!("orchard chat list: serializing: {e}");
                return ExitCode::from(2);
            }
        }
    } else if rooms.is_empty() {
        println!("(no rooms)");
    } else {
        for r in &rooms {
            // Distinguish `#room` from `@handle` direct-send rooms.
            if r.starts_with('@') {
                println!("{r}");
            } else {
                println!("#{r}");
            }
        }
    }
    ExitCode::SUCCESS
}
