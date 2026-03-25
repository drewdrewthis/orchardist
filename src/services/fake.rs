use crate::types::Worktree;

/// Fake git service for testing. Returns pre-configured data.
pub struct FakeGitService {
    pub repo_root: String,
    pub repo_name: String,
    pub worktrees: Vec<Worktree>,
}

impl super::GitService for FakeGitService {
    fn find_repo_root(&self) -> anyhow::Result<String> {
        Ok(self.repo_root.clone())
    }

    fn get_repo_name(&self) -> anyhow::Result<String> {
        Ok(self.repo_name.clone())
    }

    fn list_worktrees(&self) -> anyhow::Result<Vec<Worktree>> {
        Ok(self.worktrees.clone())
    }

    fn worktree_has_conflicts(&self, _path: &str) -> bool {
        false
    }

    fn remove_worktree(&self, _path: &str, _force: bool) -> anyhow::Result<()> {
        Ok(())
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::services::GitService;

    #[test]
    fn fake_git_service_returns_configured_worktrees() {
        let wt = Worktree {
            path: "/test/repo".to_string(),
            branch: Some("main".to_string()),
            ..Default::default()
        };
        let svc = FakeGitService {
            repo_root: "/test/repo".to_string(),
            repo_name: "repo".to_string(),
            worktrees: vec![wt.clone()],
        };
        let result = svc.list_worktrees().unwrap();
        assert_eq!(result.len(), 1);
        assert_eq!(result[0].path, "/test/repo");
    }

    #[test]
    fn fake_git_service_is_send_sync() {
        fn assert_send_sync<T: Send + Sync>() {}
        assert_send_sync::<FakeGitService>();
    }
}
