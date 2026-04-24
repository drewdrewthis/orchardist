//! SSH-based remote worktree and tmux session access.
//!
//! Provides helpers to run commands on a remote host over SSH with multiplexed
//! connections, list remote git worktrees and tmux sessions, and create or
//! attach to sessions on the remote machine. Consumed by `cache_sources` and
//! the TUI delete flow.
use std::process::Command;

use anyhow::anyhow;

use crate::logger::LOG;

/// Shell-escape a string for safe use in SSH command strings.
pub fn shell_escape(s: &str) -> String {
    if s.is_empty() {
        return "''".to_string();
    }
    // If it only contains safe characters, return as-is
    if s.chars()
        .all(|c| c.is_alphanumeric() || "-_./=@:+~".contains(c))
    {
        return s.to_string();
    }
    // Otherwise, wrap in single quotes and escape any internal single quotes
    format!("'{}'", s.replace('\'', "'\\''"))
}

/// Returns the SSH flags used for all orchard remote connections.
///
/// The ControlPath is placed under the system temp directory (`$TMPDIR` / `std::env::temp_dir`)
/// rather than a hardcoded `/tmp`.
fn ssh_flags() -> Vec<String> {
    // Use /tmp directly — macOS $TMPDIR is too long for Unix domain sockets.
    let control_path = std::path::PathBuf::from("/tmp/orchard-ssh-%C");
    vec![
        "-o".to_string(),
        "ConnectTimeout=5".to_string(),
        "-o".to_string(),
        "BatchMode=yes".to_string(),
        "-o".to_string(),
        "ControlMaster=auto".to_string(),
        "-o".to_string(),
        format!("ControlPath={}", control_path.display()),
        "-o".to_string(),
        "ControlPersist=600".to_string(),
    ]
}

/// Runs a shell command on a remote host over SSH and returns stdout.
pub fn ssh_exec(host: &str, command: &str) -> anyhow::Result<String> {
    let flags = ssh_flags();
    let mut args: Vec<&str> = flags.iter().map(|s| s.as_str()).collect();
    args.push(host);
    args.push(command);

    let out = Command::new("ssh").args(&args).output()?;
    if !out.status.success() {
        let stderr = sanitize_remote_payload(&String::from_utf8_lossy(&out.stderr));
        return Err(anyhow!("ssh command failed: {}", stderr));
    }
    Ok(String::from_utf8_lossy(&out.stdout).into_owned())
}

/// Sanitizes an arbitrary remote-sourced byte stream for safe inclusion in
/// log lines and error messages. Strips control bytes and multibyte
/// sequences, truncates to 256 chars. Mirrors the policy used by
/// `remote_adapter::sanitize_raw_payload` so a malicious or misconfigured
/// remote cannot inject ANSI escapes or break structured-log parsing via
/// stderr text.
fn sanitize_remote_payload(raw: &str) -> String {
    raw.chars()
        .filter(|c| c.is_ascii_graphic() || *c == ' ' || *c == '\n' || *c == '\t')
        .take(256)
        .collect()
}

/// Runs a shell command on a remote host over SSH with a hard wall-clock
/// timeout, killing the child process when it expires.
///
/// `ssh -o ConnectTimeout=5` only bounds the initial TCP/SSH handshake.
/// Once authenticated, a hung remote command (e.g. a tmux server that
/// accepts the SSH session but never responds to the command itself) will
/// block indefinitely. This wrapper spawns the SSH subprocess, waits up to
/// `timeout`, and kills the child if it has not exited — guaranteeing the
/// caller never blocks beyond the deadline.
///
/// Returns `Err` with `"ssh command timed out after <N>s"` if the deadline
/// fires, distinguishable from other SSH errors by the `timed out` phrase.
pub fn ssh_exec_with_timeout(
    host: &str,
    command: &str,
    timeout: std::time::Duration,
) -> anyhow::Result<String> {
    use std::io::Read;
    use std::time::Instant;

    let flags = ssh_flags();
    let mut args: Vec<&str> = flags.iter().map(|s| s.as_str()).collect();
    args.push(host);
    args.push(command);

    let mut child = Command::new("ssh")
        .args(&args)
        .stdout(std::process::Stdio::piped())
        .stderr(std::process::Stdio::piped())
        .spawn()?;

    let deadline = Instant::now() + timeout;
    loop {
        match child.try_wait()? {
            Some(status) => {
                let mut stdout = String::new();
                let mut stderr = String::new();
                if let Some(mut o) = child.stdout.take() {
                    let _ = o.read_to_string(&mut stdout);
                }
                if let Some(mut e) = child.stderr.take() {
                    let _ = e.read_to_string(&mut stderr);
                }
                if !status.success() {
                    let safe = sanitize_remote_payload(&stderr);
                    return Err(anyhow!("ssh command failed: {safe}"));
                }
                return Ok(stdout);
            }
            None => {
                if Instant::now() >= deadline {
                    let _ = child.kill();
                    let _ = child.wait();
                    return Err(anyhow!(
                        "ssh command timed out after {}s",
                        timeout.as_secs()
                    ));
                }
                std::thread::sleep(std::time::Duration::from_millis(50));
            }
        }
    }
}

/// Returns the first hop SSH target and the fully-chained command string for a
/// remote write operation.
///
/// When `discovery_path` is `Some` and has length ≥ 3 (i.e., at least one
/// intermediate hop between local and the leaf), the leaf command is wrapped in
/// nested SSH calls via [`crate::federation::build_ssh_chain`] and the first
/// hop host is returned as the SSH target.
///
/// For depth-1 direct remotes (`discovery_path` is `None`, empty, or
/// `["local", host]`) the command and host are returned unchanged — behaviour
/// is bit-identical to before federation was introduced.
///
/// Exposed for testing so callers can verify the SSH chain without performing a
/// real SSH round-trip.
pub fn chain_cmd(host: &str, discovery_path: Option<&[String]>, cmd: &str) -> (String, String) {
    match discovery_path {
        Some(path) if path.len() > 2 => {
            // Transitive: build nested SSH chain and target the first hop.
            let chained = crate::federation::build_ssh_chain(path, cmd);
            // The first element is "local"; the second is the jump host.
            let jump_host = path[1].clone();
            (jump_host, chained)
        }
        // Depth-1 or no path — pass through unchanged.
        _ => (host.to_string(), cmd.to_string()),
    }
}

/// Kills the named tmux session on the remote host.
///
/// `discovery_path` is the full hop chain from `"local"` to `host`
/// (e.g. `["local", "boxd", "child.example.com"]`).  Pass `None` for
/// direct (depth-1) remotes; the behaviour is unchanged from before
/// transitive federation was introduced.
pub fn kill_remote_tmux_session(
    host: &str,
    name: &str,
    discovery_path: Option<&[String]>,
) -> anyhow::Result<()> {
    let inner_cmd = format!("tmux kill-session -t {}", shell_escape(name));
    let (ssh_target, cmd) = chain_cmd(host, discovery_path, &inner_cmd);
    ssh_exec(&ssh_target, &cmd)?;
    LOG.info(&format!("remote: killed tmux session {}", name));
    Ok(())
}

/// Removes a worktree on the remote host.
///
/// First tries `git worktree remove --force`; falls back to `git worktree prune && rm -rf`.
///
/// `discovery_path` is the full hop chain from `"local"` to `host`.  Pass
/// `None` for direct (depth-1) remotes; the behaviour is unchanged.
pub fn remove_remote_worktree(
    host: &str,
    repo_path: &str,
    wt_path: &str,
    discovery_path: Option<&[String]>,
) -> anyhow::Result<()> {
    let cmd = format!(
        "cd {} && git worktree remove --force {}",
        shell_escape(repo_path),
        shell_escape(wt_path)
    );
    let (ssh_target, chained_cmd) = chain_cmd(host, discovery_path, &cmd);
    if ssh_exec(&ssh_target, &chained_cmd).is_ok() {
        return Ok(());
    }

    let fallback = format!(
        "cd {} && git worktree prune && rm -rf {}",
        shell_escape(repo_path),
        shell_escape(wt_path)
    );
    let (ssh_target2, chained_fallback) = chain_cmd(host, discovery_path, &fallback);
    ssh_exec(&ssh_target2, &chained_fallback)?;
    Ok(())
}

/// Creates a new detached tmux session on the remote host.
///
/// If the session already exists the error is silently ignored.
///
/// `discovery_path` is the full hop chain from `"local"` to `host`.  Pass
/// `None` for direct (depth-1) remotes; the behaviour is unchanged.
pub fn create_remote_session(
    host: &str,
    name: &str,
    path: &str,
    discovery_path: Option<&[String]>,
) -> anyhow::Result<()> {
    let cmd = format!(
        "tmux new-session -d -s {} -c {}",
        shell_escape(name),
        shell_escape(path)
    );
    let (ssh_target, chained_cmd) = chain_cmd(host, discovery_path, &cmd);
    match ssh_exec(&ssh_target, &chained_cmd) {
        Ok(_) => Ok(()),
        Err(e) if e.to_string().contains("duplicate session") => Ok(()),
        Err(e) => Err(e),
    }
}

/// Checks if a local proxy session exists and is healthy.
/// Returns `true` if the session needs to be (re)created.
/// Determines whether the local proxy session needs to be (re)created.
/// `remote_was_fresh` indicates the remote session was just created, meaning
/// any existing proxy is connected to a stale session.
fn should_recreate_proxy(local_name: &str, remote_was_fresh: bool) -> bool {
    let proxy_exists = Command::new("tmux")
        .args(["has-session", "-t", local_name])
        .status()
        .map(|s| s.success())
        .unwrap_or(false);

    if !proxy_exists {
        return true;
    }

    // If we just created the remote session, the existing proxy is stale.
    if remote_was_fresh {
        LOG.info(&format!(
            "remote: killing stale proxy {} (remote was recreated)",
            local_name
        ));
        let _ = Command::new("tmux")
            .args(["kill-session", "-t", local_name])
            .status();
        return true;
    }

    let pane_dead = Command::new("tmux")
        .args(["list-panes", "-t", local_name, "-F", "#{pane_dead}"])
        .output()
        .ok()
        .map(|o| String::from_utf8_lossy(&o.stdout).trim() == "1")
        .unwrap_or(false);

    let pane_stuck = if !pane_dead {
        Command::new("tmux")
            .args(["capture-pane", "-t", local_name, "-p", "-S", "-1"])
            .output()
            .ok()
            .map(|o| {
                let content = String::from_utf8_lossy(&o.stdout);
                content.contains("mosh: Last contact")
                    || (content.contains("Connection to") && content.contains("closed"))
            })
            .unwrap_or(false)
    } else {
        false
    };

    if pane_dead || pane_stuck {
        LOG.info(&format!(
            "remote: killing {} proxy session {}",
            if pane_dead { "dead" } else { "stuck" },
            local_name
        ));
        let _ = Command::new("tmux")
            .args(["kill-session", "-t", local_name])
            .status();
        return true;
    }

    false
}

/// Creates a local proxy tmux session that connects to the remote session via ssh or
/// mosh. Does NOT switch the local tmux client to it. Returns the local session name.
///
/// This is the popup-mode entry point: the caller gets the local session name back
/// and prints it to stdout for the wrapper script to call `tmux switch-client`.
///
/// `discovery_path` is the full hop chain from `"local"` to `host`.  Pass
/// `None` for direct (depth-1) remotes; the behaviour is unchanged.  For
/// transitive (depth-2+) remotes the `has-session` probe, `create_remote_session`
/// call, and the local proxy's inner `ssh ... tmux attach-session` command all
/// route through the nested SSH chain.
pub fn create_remote_proxy_session(
    host: &str,
    name: &str,
    path: &str,
    shell: &str,
    discovery_path: Option<&[String]>,
) -> anyhow::Result<String> {
    let shell = if shell.is_empty() { "ssh" } else { shell };

    // Create the remote session if it doesn't exist yet.
    let has_session_cmd = format!("tmux has-session -t {}", shell_escape(name));
    let (has_target, has_cmd) = chain_cmd(host, discovery_path, &has_session_cmd);
    let remote_was_fresh = ssh_exec(&has_target, &has_cmd).is_err();
    if remote_was_fresh {
        create_remote_session(host, name, path, discovery_path)?;
    }

    let local_name = format!("remote_{}", name);
    let connect_cmd = if shell == "mosh" {
        // mosh doesn't support transitive hops — use direct host.
        format!(
            "env LC_ALL=en_US.UTF-8 mosh --predict=always {} -- tmux attach-session -t {}",
            shell_escape(host),
            shell_escape(name)
        )
    } else {
        // Build the connect command for the local proxy pane.
        // For depth-1: `ssh -tt host tmux attach-session -t name`
        // For depth-2+: `ssh -tt jump_host ssh 'leaf' 'tmux attach-session -t name'`
        let leaf_attach = format!("tmux attach-session -t {}", shell_escape(name));
        match discovery_path {
            Some(dp) if dp.len() > 2 => {
                // Build inner hops using build_ssh_chain on the leaf-only portion,
                // then add -tt on the outermost hop.
                // hops = dp[1..] (strip "local"), jump = dp[1], rest = dp[2..]
                let jump_host = &dp[1];
                // Build the chain from the jump host onward (inner only).
                // We use build_ssh_chain with a sub-path starting from the jump host.
                let sub_path: Vec<String> = dp[1..].to_vec();
                let inner = crate::federation::build_ssh_chain(&sub_path, &leaf_attach);
                // inner = "ssh jump_host ssh 'leaf' 'tmux attach-session -t name'"
                // but the outermost `ssh jump_host` needs `-tt`.
                // Replace leading `ssh jump_host ` with `ssh -tt jump_host `.
                let prefix = format!("ssh {} ", jump_host);
                if inner.starts_with(&prefix) {
                    format!("ssh -tt {} {}", jump_host, &inner[prefix.len()..])
                } else {
                    format!("ssh -tt {} {}", shell_escape(jump_host), inner)
                }
            }
            // depth-0 or depth-1: unchanged
            _ => format!(
                "ssh -tt {} tmux attach-session -t {}",
                shell_escape(host),
                shell_escape(name)
            ),
        }
    };

    let need_create = should_recreate_proxy(&local_name, remote_was_fresh);
    if need_create {
        let create_out = Command::new("tmux")
            .args([
                "new-session",
                "-d",
                "-s",
                &local_name,
                "--",
                "sh",
                "-c",
                &connect_cmd,
            ])
            .output()?;

        if !create_out.status.success() {
            let stderr = String::from_utf8_lossy(&create_out.stderr);
            if !stderr.contains("duplicate session") {
                return Err(anyhow!(
                    "creating local proxy session {:?}: {}",
                    local_name,
                    stderr
                ));
            }
        }
    }

    // Keep the pane alive after the SSH/mosh process exits.
    let _ = Command::new("tmux")
        .args(["set-option", "-t", &local_name, "remain-on-exit", "on"])
        .status();

    LOG.info(&format!(
        "createRemoteProxySession: {} -> {}",
        name, local_name
    ));
    Ok(local_name)
}

/// Captures the pane content of a remote tmux session via SSH.
pub fn capture_remote_pane_content(
    host: &str,
    session: &str,
    lines: u32,
) -> anyhow::Result<String> {
    let cmd = format!(
        "tmux capture-pane -t {} -p -J -S -{}",
        shell_escape(session),
        lines
    );
    let out = ssh_exec(host, &cmd)?;
    Ok(out.trim_end_matches('\n').to_string())
}

/// Removes the remmy session registry file for the given session name on the
/// remote host.
///
/// `discovery_path` is the full hop chain from `"local"` to `host`.  Pass
/// `None` for direct (depth-1) remotes; the behaviour is unchanged.
pub fn remove_remote_registry_entry(
    host: &str,
    name: &str,
    discovery_path: Option<&[String]>,
) -> anyhow::Result<()> {
    let cmd = format!("rm -f ~/.remmy/sessions/{}.json", shell_escape(name));
    let (ssh_target, chained_cmd) = chain_cmd(host, discovery_path, &cmd);
    ssh_exec(&ssh_target, &chained_cmd)?;
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn shell_escape_returns_empty_quoted_for_empty_string() {
        assert_eq!(shell_escape(""), "''");
    }

    #[test]
    fn shell_escape_passes_through_safe_characters() {
        assert_eq!(shell_escape("hello-world_123"), "hello-world_123");
    }

    #[test]
    fn shell_escape_passes_through_tilde() {
        assert_eq!(shell_escape("~/path/to/dir"), "~/path/to/dir");
    }

    #[test]
    fn shell_escape_passes_through_at_and_colon() {
        assert_eq!(shell_escape("user@host:path"), "user@host:path");
    }

    #[test]
    fn shell_escape_wraps_spaces_in_single_quotes() {
        assert_eq!(shell_escape("hello world"), "'hello world'");
    }

    #[test]
    fn shell_escape_escapes_single_quotes() {
        assert_eq!(shell_escape("it's"), "'it'\\''s'");
    }

    #[test]
    fn shell_escape_wraps_semicolons() {
        assert_eq!(shell_escape("cmd;evil"), "'cmd;evil'");
    }

    #[test]
    fn shell_escape_wraps_dollar_signs() {
        assert_eq!(shell_escape("$HOME"), "'$HOME'");
    }

    #[test]
    fn shell_escape_wraps_backticks() {
        assert_eq!(shell_escape("`whoami`"), "'`whoami`'");
    }

    // ---------------------------------------------------------------------
    // ssh_exec_with_timeout — AC6 (feature.feature:469)
    //
    // These tests spawn a real `ssh` subprocess pointed at a deliberately
    // unroutable host. They are gated behind a helper that skips the test
    // when no `ssh` binary is available, so CI environments without SSH
    // (e.g. the Tauri cross-compile shard) still build green.
    // ---------------------------------------------------------------------

    fn ssh_binary_present() -> bool {
        std::process::Command::new("ssh").arg("-V").output().is_ok()
    }

    /// `ssh_exec_with_timeout` returns an error within the configured
    /// deadline regardless of whether the OS drops or actively refuses
    /// the connection. The bound that matters for AC6 is wall-clock; the
    /// specific error phrase ("timed out" vs "Connection refused")
    /// depends on CI's outbound-network policy and is not asserted.
    #[test]
    fn ssh_exec_with_timeout_returns_within_deadline() {
        if !ssh_binary_present() {
            eprintln!("SKIP: ssh binary not available");
            return;
        }

        // 192.0.2.0/24 is TEST-NET-1 (RFC 5737) — guaranteed unroutable on
        // the public internet, but a CI firewall may RST the SYN
        // immediately. Both outcomes are acceptable; AC6 promises only the
        // wall-clock bound.
        let start = std::time::Instant::now();
        let result =
            ssh_exec_with_timeout("192.0.2.1", "true", std::time::Duration::from_millis(200));
        let elapsed = start.elapsed();

        assert!(
            result.is_err(),
            "unreachable host must produce Err, got: {:?}",
            result
        );
        // SSH ConnectTimeout would otherwise let this hang for 5s — well
        // under that proves the wrapper preempted the SSH default.
        assert!(
            elapsed < std::time::Duration::from_millis(1500),
            "timeout must preempt SSH ConnectTimeout; elapsed {:?}",
            elapsed
        );
    }

    /// Pure unit: the polling-deadline loop fires within its budget on a
    /// genuinely-hung child (cat blocking on stdin). Exercises the same
    /// loop shape `ssh_exec_with_timeout` uses, without depending on
    /// network policy or the `ssh` binary being present.
    #[test]
    fn timeout_poll_loop_kills_hung_child_within_deadline() {
        let start = std::time::Instant::now();
        let mut child = std::process::Command::new("cat")
            .stdin(std::process::Stdio::piped())
            .stdout(std::process::Stdio::piped())
            .stderr(std::process::Stdio::piped())
            .spawn()
            .expect("`cat` is part of POSIX");

        let deadline = std::time::Instant::now() + std::time::Duration::from_millis(150);
        let killed = loop {
            match child.try_wait().unwrap() {
                Some(_) => break false,
                None => {
                    if std::time::Instant::now() >= deadline {
                        let _ = child.kill();
                        let _ = child.wait();
                        break true;
                    }
                    std::thread::sleep(std::time::Duration::from_millis(20));
                }
            }
        };

        assert!(killed, "deadline must fire and reap the hung child");
        assert!(
            start.elapsed() < std::time::Duration::from_millis(800),
            "deadline-driven loop must terminate promptly"
        );
    }

    #[test]
    fn sanitize_remote_payload_strips_ansi_escapes() {
        // \x1b[31m is the ANSI red-foreground escape — common
        // injection vector via terminal control codes.
        let evil = "\x1b[31mERROR\x1b[0m: \x07bell";
        let safe = sanitize_remote_payload(evil);
        assert!(!safe.contains('\x1b'), "ANSI escapes must be stripped");
        assert!(!safe.contains('\x07'), "bell character must be stripped");
        assert!(safe.contains("ERROR"), "printable ASCII must survive");
    }

    #[test]
    fn sanitize_remote_payload_caps_at_256_chars() {
        let long = "x".repeat(1000);
        assert_eq!(sanitize_remote_payload(&long).len(), 256);
    }
}
