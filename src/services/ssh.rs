use std::process::Command;

use anyhow::anyhow;

use crate::remote::SSH_FLAGS;

/// Command-based implementation of `SshService`.
pub struct CommandSsh;

impl super::SshService for CommandSsh {
    fn exec(&self, host: &str, command: &str) -> anyhow::Result<String> {
        let mut args: Vec<&str> = SSH_FLAGS.to_vec();
        args.push(host);
        args.push(command);

        let out = Command::new("ssh").args(&args).output()?;
        if !out.status.success() {
            let stderr = String::from_utf8_lossy(&out.stderr).into_owned();
            return Err(anyhow!("ssh command failed: {}", stderr));
        }
        Ok(String::from_utf8_lossy(&out.stdout).into_owned())
    }

    fn is_reachable(&self, host: &str) -> bool {
        self.exec(host, "true").is_ok()
    }
}
