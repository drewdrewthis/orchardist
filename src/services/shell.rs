use std::process::Command;

/// Command-based implementation of `ShellCommandService`.
pub struct CommandShell;

impl super::ShellCommandService for CommandShell {
    fn run(&self, program: &str, args: &[&str]) -> anyhow::Result<String> {
        let out = Command::new(program).args(args).output()?;
        if !out.status.success() {
            let stderr = String::from_utf8_lossy(&out.stderr).into_owned();
            return Err(anyhow::anyhow!("{program} failed: {stderr}"));
        }
        Ok(String::from_utf8_lossy(&out.stdout).into_owned())
    }

    fn run_in(&self, program: &str, args: &[&str], cwd: &str) -> anyhow::Result<String> {
        let out = Command::new(program)
            .args(args)
            .current_dir(cwd)
            .output()?;
        if !out.status.success() {
            let stderr = String::from_utf8_lossy(&out.stderr).into_owned();
            return Err(anyhow::anyhow!("{program} failed: {stderr}"));
        }
        Ok(String::from_utf8_lossy(&out.stdout).into_owned())
    }

    fn run_status(&self, program: &str, args: &[&str]) -> anyhow::Result<bool> {
        let status = Command::new(program).args(args).status()?;
        Ok(status.success())
    }
}
