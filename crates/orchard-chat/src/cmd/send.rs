//! `orchard chat send <target> <text>` (also reachable as `orchard send …`).

use std::process::ExitCode;

use clap::Args as ClapArgs;

use chat_core::{FanoutOutcome, SendOutcome, Target};

use crate::cmd::sender;

#[derive(ClapArgs, Debug)]
pub struct Args {
    /// `#room` (broadcast) or `@handle` (direct).
    pub target: String,

    /// The message text.
    pub text: String,

    /// Override the sender handle (otherwise auto-derived from `$TMUX`).
    #[arg(long = "as", value_name = "HANDLE")]
    pub as_handle: Option<String>,

    /// Emit machine-readable JSON (full `SendOutcome` shape) on stdout.
    #[arg(short = 'j', long = "json")]
    pub json: bool,
}

pub fn run(args: Args) -> ExitCode {
    let target = match Target::parse(&args.target) {
        Some(t) => t,
        None => {
            eprintln!(
                "orchard chat send: target must start with '#' (room) or '@' (handle); got {:?}",
                args.target
            );
            return ExitCode::from(3);
        }
    };
    let sender = match sender::resolve(args.as_handle.as_deref()) {
        Ok(s) => s,
        Err(e) => {
            eprintln!("orchard chat send: {e}");
            return ExitCode::from(3);
        }
    };
    let outcome = match chat_core::send(&target, &sender, &args.text) {
        Ok(o) => o,
        Err(e) => {
            eprintln!("orchard chat send: failed to append message: {e:#}");
            return ExitCode::from(2);
        }
    };

    let exit = derive_exit_code(&outcome);
    if args.json {
        match serde_json::to_string(&outcome) {
            Ok(s) => println!("{s}"),
            Err(e) => {
                eprintln!("orchard chat send: failed to serialize outcome: {e}");
                return ExitCode::from(2);
            }
        }
    } else {
        report_human(&outcome);
    }
    if exit != 0 {
        report_partial_to_stderr(&outcome);
    }
    ExitCode::from(exit)
}

fn derive_exit_code(outcome: &SendOutcome) -> u8 {
    // Empty fanout (no recipients) → full success. The message is in JSONL
    // and there was nobody to deliver to (e.g. broadcasting to a room where
    // you're the only member, or sending `@self`). The skip-sender path is
    // also "no real recipient" — exclude it from problem-counting too, so
    // sending to yourself doesn't surface as exit 1.
    let problems = outcome.fanout.iter().filter(|o| !is_ok_outcome(o)).count();
    if problems == 0 { 0 } else { 1 }
}

fn is_ok_outcome(o: &FanoutOutcome) -> bool {
    match o {
        FanoutOutcome::Delivered { .. } => true,
        // Sender-self isn't a real recipient — skipping it is expected, not
        // a partial failure.
        FanoutOutcome::Skipped { reason, .. } if reason == "sender" => true,
        _ => false,
    }
}

fn report_human(outcome: &SendOutcome) {
    let delivered = outcome
        .fanout
        .iter()
        .filter(|o| o.is_delivered())
        .count();
    let total = outcome.fanout.len();
    println!(
        "sent message {} to {} ({}/{} delivered)",
        outcome.message_id, outcome.room, delivered, total
    );
}

fn report_partial_to_stderr(outcome: &SendOutcome) {
    for o in &outcome.fanout {
        match o {
            FanoutOutcome::Delivered { .. } => {}
            FanoutOutcome::ByteOnly { recipient, reason } => {
                eprintln!("  byte-only {}: {reason}", with_at(recipient));
            }
            FanoutOutcome::Failed { recipient, error } => {
                eprintln!("  failed {}: {error}", with_at(recipient));
            }
            FanoutOutcome::Skipped { recipient, reason } => {
                eprintln!("  skipped {}: {reason}", with_at(recipient));
            }
        }
    }
}

/// Recipients can come in either with or without the leading `@` sigil
/// (depending on whether the target was `Target::Direct(handle)` whose
/// `handle` may not include the sigil or via membership where `Member.handle`
/// always does). This helper makes the formatted output uniform without
/// double-printing the sigil.
fn with_at(recipient: &str) -> String {
    if recipient.starts_with('@') {
        recipient.to_string()
    } else {
        format!("@{recipient}")
    }
}
