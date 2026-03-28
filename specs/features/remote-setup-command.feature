Feature: Remote setup command
  As an orchard user with a remote host configured
  I want to run `orchard setup-remote <host>` to provision the remote
  So that remote worktree management works without manual setup

  Background:
    Given a remote host is configured in orchard's config (global or per-repo)
    And the host is reachable via SSH

  @unit
  Scenario: Host resolution from config
    Given a global config with remote "gpu" pointing to "ubuntu@10.0.0.1"
    When I run `orchard setup-remote gpu`
    Then orchard resolves "gpu" to the full remote config
    And uses "ubuntu@10.0.0.1" as the SSH target

  @unit
  Scenario: Host resolution by direct SSH target
    When I run `orchard setup-remote user@host`
    Then orchard treats "user@host" as a direct SSH target
    And looks up the matching remote config by host field

  @integration
  Scenario: Verify SSH connectivity
    When I run `orchard setup-remote <host>`
    Then orchard attempts an SSH connection to the host
    And reports PASS if the connection succeeds
    And reports FAIL with error details if the connection fails

  @integration
  Scenario: Check remote dependencies
    When I run `orchard setup-remote <host>`
    Then orchard checks for tmux, git, gh, and claude using `command -v` (POSIX-portable)
    And reports PASS/FAIL for each dependency individually

  @integration
  Scenario: Install orchard-state hook on remote
    When I run `orchard setup-remote <host>`
    Then orchard transfers orchard-state.sh via base64 encoding to avoid shell quoting issues
    And writes it to ~/.claude/hooks/orchard-state.sh on the remote
    And makes it executable (chmod +x)
    And registers it in ~/.claude/settings.json on the remote
    And the registration reads existing settings via SSH, merges in Rust, writes back atomically
    And the atomic write uses a temp file + mv pattern

  @integration
  Scenario: Idempotent hook installation
    Given orchard-state.sh is already installed on the remote
    And hooks are already registered in settings.json
    When I run `orchard setup-remote <host>` again
    Then the hook script is updated (overwritten with latest)
    And no duplicate entries appear in settings.json

  @integration
  Scenario: Verify remote repo access
    Given the remote config specifies repoPath "/home/ubuntu/myrepo"
    When I run `orchard setup-remote <host>`
    Then orchard verifies the path exists on the remote
    And verifies it is a valid git repository
    And reports PASS/FAIL for repo access

  @unit
  Scenario: Pass/fail summary report
    When setup-remote completes all checks
    Then it prints a summary with pass/fail status for each step:
      | Step                  | Status |
      | SSH connectivity      | PASS   |
      | tmux                  | PASS   |
      | git                   | PASS   |
      | gh                    | PASS   |
      | claude                | PASS   |
      | Hook installed        | PASS   |
      | Hooks registered      | PASS   |
      | Repo access           | PASS   |

  @unit
  Scenario: Partial failure reporting
    Given gh is not installed on the remote
    When I run `orchard setup-remote <host>`
    Then the summary shows FAIL for gh
    And shows PASS for all other successful steps
    And the command exits with a non-zero exit code

  @unit
  Scenario: Unknown host argument
    When I run `orchard setup-remote nonexistent`
    And no remote named "nonexistent" exists in config
    And "nonexistent" doesn't look like a user@host pattern
    Then orchard prints an error explaining the host was not found
    And suggests running `orchard init` to configure remotes
