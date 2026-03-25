use std::process::Command;

use anyhow::{Context, Result};

use crate::logger::LOG;
use crate::tmux::{apply_session_style, format_status_left};
use crate::types::{PrInfo, SwitchToSessionOptions, TmuxSession};

/// Command-based implementation of `TmuxService`.
pub struct CommandTmux;

impl super::TmuxService for CommandTmux {
    fn list_sessions(&self) -> Vec<TmuxSession> {
        let out = Command::new("tmux")
            .args([
                "list-sessions",
                "-F",
                "#{session_name}\t#{session_path}\t#{session_attached}",
            ])
            .output();

        let output = match out {
            Ok(o) if o.status.success() => o.stdout,
            _ => return Vec::new(),
        };

        let text = String::from_utf8_lossy(&output);
        let mut sessions = Vec::new();

        for line in text.trim().lines() {
            if line.is_empty() {
                continue;
            }
            let parts: Vec<&str> = line.splitn(3, '\t').collect();
            if parts.len() != 3 {
                continue;
            }
            sessions.push(TmuxSession {
                name: parts[0].to_string(),
                path: parts[1].to_string(),
                attached: parts[2] == "1",
                pane_title: None,
            });
        }

        // Fetch pane titles for all sessions in one call (pane index 0 only).
        let pane_out = Command::new("tmux")
            .args([
                "list-panes",
                "-a",
                "-F",
                "#{session_name}\t#{pane_index}\t#{pane_title}",
            ])
            .output();

        if let Ok(o) = pane_out
            && o.status.success()
        {
            let pane_text = String::from_utf8_lossy(&o.stdout);
            for line in pane_text.trim().lines() {
                let parts: Vec<&str> = line.splitn(3, '\t').collect();
                if parts.len() == 3
                    && parts[1] == "0"
                    && let Some(session) =
                        sessions.iter_mut().find(|s| s.name == parts[0])
                {
                    session.pane_title = Some(parts[2].to_string());
                }
            }
        }

        LOG.info(&format!("listTmuxSessions: {} sessions", sessions.len()));
        sessions
    }

    fn new_detached_session(&self, name: &str, start_dir: &str) -> Result<()> {
        let output = Command::new("tmux")
            .args(["new-session", "-d", "-s", name, "-c", start_dir])
            .output()
            .context("tmux new-session")?;

        if !output.status.success() {
            let stderr = String::from_utf8_lossy(&output.stderr);
            return Err(anyhow::anyhow!(
                "tmux new-session failed: {}",
                stderr.trim()
            ));
        }

        LOG.info(&format!("newDetachedSession: {} at {}", name, start_dir));
        Ok(())
    }

    fn kill_session(&self, name: &str) -> Result<()> {
        Command::new("tmux")
            .args(["kill-session", "-t", name])
            .status()
            .context("tmux kill-session")?;
        LOG.info(&format!("killTmuxSession: {}", name));
        Ok(())
    }

    fn capture_pane_content(&self, session: &str, lines: u32) -> Result<String> {
        let lines_arg = format!("-{lines}");
        let out = Command::new("tmux")
            .args([
                "capture-pane",
                "-t",
                session,
                "-p",
                "-J",
                "-S",
                &lines_arg,
            ])
            .output()
            .context("tmux capture-pane")?;

        let text = String::from_utf8_lossy(&out.stdout);
        Ok(text.trim_end_matches('\n').to_string())
    }

    fn create_session(&self, opts: &SwitchToSessionOptions) -> Result<()> {
        let exists = Command::new("tmux")
            .args(["has-session", "-t", &opts.session_name])
            .status()
            .map(|s| s.success())
            .unwrap_or(false);

        if !exists {
            Command::new("tmux")
                .args([
                    "new-session",
                    "-d",
                    "-s",
                    &opts.session_name,
                    "-c",
                    &opts.worktree_path,
                ])
                .status()
                .with_context(|| format!("creating session {}", opts.session_name))?;
        }

        LOG.info(&format!(
            "createSession: {} ({})",
            opts.session_name,
            if exists { "existing" } else { "new" }
        ));

        apply_session_style(
            &opts.session_name,
            opts.branch.as_deref(),
            opts.pr.as_ref(),
        )?;

        Ok(())
    }

    fn apply_session_style(
        &self,
        name: &str,
        branch: Option<&str>,
        pr: Option<&PrInfo>,
    ) -> Result<()> {
        let status_left = format_status_left(branch, pr);
        let t = ["-t", name];

        let opts: &[(&str, &str)] = &[
            ("status", "on"),
            ("status-style", "bg=colour235,fg=colour248"),
            ("status-left-length", "60"),
            ("status-right-length", "150"),
            ("status-left", &status_left),
            ("status-right", crate::tmux::CHEATSHEET),
        ];

        for (key, value) in opts {
            Command::new("tmux")
                .arg("set-option")
                .args(t)
                .args([*key, value])
                .status()
                .with_context(|| format!("tmux set-option {key}"))?;
        }

        Ok(())
    }

    fn has_session(&self, name: &str) -> bool {
        Command::new("tmux")
            .args(["has-session", "-t", name])
            .status()
            .map(|s| s.success())
            .unwrap_or(false)
    }

    fn session_pane_dead(&self, name: &str) -> bool {
        Command::new("tmux")
            .args(["list-panes", "-t", name, "-F", "#{pane_dead}"])
            .output()
            .ok()
            .map(|o| String::from_utf8_lossy(&o.stdout).trim() == "1")
            .unwrap_or(false)
    }

    fn capture_pane_last_line(&self, name: &str) -> String {
        Command::new("tmux")
            .args(["capture-pane", "-t", name, "-p", "-S", "-1"])
            .output()
            .ok()
            .map(|o| String::from_utf8_lossy(&o.stdout).to_string())
            .unwrap_or_default()
    }

    fn create_proxy_session(
        &self,
        local_name: &str,
        connect_cmd: &str,
    ) -> anyhow::Result<()> {
        let create_out = Command::new("tmux")
            .args([
                "new-session",
                "-d",
                "-s",
                local_name,
                "--",
                "sh",
                "-c",
                connect_cmd,
            ])
            .output()?;

        if !create_out.status.success() {
            let stderr = String::from_utf8_lossy(&create_out.stderr);
            if !stderr.contains("duplicate session") {
                return Err(anyhow::anyhow!(
                    "creating local proxy session {:?}: {}",
                    local_name,
                    stderr
                ));
            }
        }
        Ok(())
    }

    fn set_remain_on_exit(&self, name: &str) -> anyhow::Result<()> {
        let _ = Command::new("tmux")
            .args(["set-option", "-t", name, "remain-on-exit", "on"])
            .status();
        Ok(())
    }

    fn list_panes_for_session(&self, name: &str) -> String {
        Command::new("tmux")
            .args(["list-panes", "-t", name, "-F", "#{pane_dead}"])
            .output()
            .ok()
            .map(|o| String::from_utf8_lossy(&o.stdout).to_string())
            .unwrap_or_default()
    }
}
