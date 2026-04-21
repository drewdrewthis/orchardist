//! AC7 regression: `orchard` subcommands that are meant for background /
//! batch use MUST work when stderr is not a TTY. Pre-fix, color_eyre's
//! `HookBuilder::default()` opened `/dev/tty` at every startup and failed
//! with ENXIO under cron, systemd, CI runners, and `ssh` without `-t`.
//!
//! This file exercises the release binary with redirected stdio and
//! asserts the installable startup path does not crash on ENXIO.

use std::process::{Command, Stdio};

use tempfile::TempDir;

fn orchard_bin() -> Command {
    Command::new(env!("CARGO_BIN_EXE_orchard"))
}

/// Non-TTY invocation of `orchard --version` must succeed (it's
/// dispatched before any work and is a common health probe from
/// shell scripts).
#[test]
fn orchard_version_succeeds_without_controlling_tty() {
    // assert_cmd already pipes stdio, so neither stdout nor stderr is a
    // terminal by the time `main()` runs. That's the exact environment
    // that broke pre-fix.
    let status = orchard_bin()
        .arg("--version")
        .stdin(Stdio::null())
        .stdout(Stdio::piped())
        .stderr(Stdio::piped())
        .status()
        .expect("orchard --version must exec");
    assert!(
        status.success(),
        "orchard --version exited non-zero: {status:?}"
    );
}

/// Non-TTY invocation of `orchard refresh` with an empty config must
/// succeed. `refresh` is the explicit cron/systemd target; if it can't
/// run without a TTY, AC7's "background services" claim is false.
#[test]
fn orchard_refresh_succeeds_without_controlling_tty() {
    let home = TempDir::new().expect("create temp home");
    std::fs::create_dir_all(home.path().join(".config/orchard")).unwrap();
    // Empty config — no repos, no remotes. refresh becomes a no-op
    // that still must exit 0.
    std::fs::write(
        home.path().join(".config/orchard/config.json"),
        r#"{"repos": []}"#,
    )
    .unwrap();

    let status = orchard_bin()
        .arg("refresh")
        .env("HOME", home.path())
        .env("XDG_CONFIG_HOME", home.path().join(".config"))
        .stdin(Stdio::null())
        .stdout(Stdio::piped())
        .stderr(Stdio::piped())
        .status()
        .expect("orchard refresh must exec");
    assert!(
        status.success(),
        "orchard refresh exited non-zero: {status:?}"
    );
}

/// Non-TTY invocation of `orchard --json` must succeed and emit valid
/// JSON on stdout (cache-only read path, AC7).
#[test]
fn orchard_json_succeeds_without_controlling_tty() {
    let home = TempDir::new().expect("create temp home");
    std::fs::create_dir_all(home.path().join(".config/orchard")).unwrap();
    std::fs::write(
        home.path().join(".config/orchard/config.json"),
        r#"{"repos": []}"#,
    )
    .unwrap();

    let output = orchard_bin()
        .arg("--json")
        .env("HOME", home.path())
        .env("XDG_CONFIG_HOME", home.path().join(".config"))
        .stdin(Stdio::null())
        .stdout(Stdio::piped())
        .stderr(Stdio::piped())
        .output()
        .expect("orchard --json must exec");

    assert!(
        output.status.success(),
        "orchard --json exited non-zero: stdout={}, stderr={}",
        String::from_utf8_lossy(&output.stdout),
        String::from_utf8_lossy(&output.stderr),
    );
    serde_json::from_slice::<serde_json::Value>(&output.stdout).expect("stdout must be valid JSON");
}
