//! `orchard chat tail <room>` — print full history then follow new appends.

use std::io::{BufRead, BufReader, Seek, SeekFrom};
use std::process::ExitCode;
use std::time::Duration;

use clap::Args as ClapArgs;

use chat_core::Event;

#[derive(ClapArgs, Debug)]
pub struct Args {
    pub room: String,

    /// Print raw JSONL lines instead of formatted. Useful for piping.
    #[arg(long = "raw")]
    pub raw: bool,
}

pub fn run(args: Args) -> ExitCode {
    let room = args.room.trim_start_matches('#').to_string();
    let path = match chat_core::room_path(&room) {
        Ok(p) => p,
        Err(e) => {
            eprintln!("orchard chat tail: {e:#}");
            return ExitCode::from(3);
        }
    };

    // Ensure the file exists so we can tail it even if no one's joined.
    if !path.exists() {
        if let Some(parent) = path.parent() {
            std::fs::create_dir_all(parent).ok();
        }
        std::fs::OpenOptions::new()
            .create(true)
            .append(true)
            .open(&path)
            .ok();
    }

    let mut file = match std::fs::File::open(&path) {
        Ok(f) => f,
        Err(e) => {
            eprintln!("orchard chat tail: opening {}: {e}", path.display());
            return ExitCode::from(2);
        }
    };

    // Phase 1: backfill — read everything from the start.
    let mut buf_reader = BufReader::new(&file);
    for line in (&mut buf_reader).lines().map_while(Result::ok) {
        emit_line(&line, args.raw);
    }
    // Capture position for follow phase.
    let pos = buf_reader.stream_position().unwrap_or(0);
    drop(buf_reader);
    file.seek(SeekFrom::Start(pos)).ok();

    // Phase 2: follow.
    let mut reader = BufReader::new(file);
    loop {
        let mut buf = String::new();
        match reader.read_line(&mut buf) {
            Ok(0) => {
                std::thread::sleep(Duration::from_millis(200));
            }
            Ok(_) => {
                let line = buf.trim_end();
                if !line.is_empty() {
                    emit_line(line, args.raw);
                }
            }
            Err(e) => {
                eprintln!("orchard chat tail: read error: {e}");
                return ExitCode::from(2);
            }
        }
    }
}

fn emit_line(line: &str, raw: bool) {
    if raw {
        println!("{line}");
        return;
    }
    match serde_json::from_str::<Event>(line) {
        Ok(Event::Message {
            ts, sender, text, ..
        }) => {
            println!("[{ts}] {sender}: {text}");
        }
        Ok(Event::MemberJoined {
            ts,
            handle,
            tmux_session,
            ..
        }) => {
            println!("[{ts}] {handle} joined (tmux: {tmux_session})");
        }
        Ok(Event::MemberLeft { ts, handle }) => {
            println!("[{ts}] {handle} left");
        }
        Err(_) => {
            // Malformed line — print raw as a hint.
            println!("(unparsed) {line}");
        }
    }
}
