//! `orchard chat members <room>`.

use std::process::ExitCode;

use clap::Args as ClapArgs;

#[derive(ClapArgs, Debug)]
pub struct Args {
    pub room: String,

    #[arg(short = 'j', long = "json")]
    pub json: bool,
}

pub fn run(args: Args) -> ExitCode {
    let room = args.room.trim_start_matches('#').to_string();
    let members = match chat_core::list_members(&room) {
        Ok(m) => m,
        Err(e) => {
            eprintln!("orchard chat members: {e:#}");
            return ExitCode::from(2);
        }
    };
    if args.json {
        match serde_json::to_string(&members) {
            Ok(s) => println!("{s}"),
            Err(e) => {
                eprintln!("orchard chat members: serializing: {e}");
                return ExitCode::from(2);
            }
        }
    } else if members.is_empty() {
        println!("(no members in #{room})");
    } else {
        for m in &members {
            println!(
                "{}\t{}\t{}\t{}",
                m.handle, m.machine, m.tmux_session, m.joined_at
            );
        }
    }
    ExitCode::SUCCESS
}
