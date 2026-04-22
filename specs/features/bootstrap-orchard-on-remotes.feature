Feature: Bootstrap orchard on remote hosts for federated discovery
  As an orchard user with a fleet of remote hosts (boxd VMs, remmy)
  I want a documented, scripted way to install orchard on any remote over SSH
  So that I can flip remotes to "type": "orchard-proxy" and use federated discovery
     without hand-rolling install recipes per host

  Background:
    Given local orchard is version 0.10.0 or newer (ships `install-remote`)
    And the remote host is reachable via SSH
    And the remote is Linux x86_64 with sudo available
    And the GitHub release for the local orchard version has a tarball + sha256 for the remote's arch

  # ---------------------------------------------------------------------------
  # AC1 — Install recipe (subcommand + script)
  # ---------------------------------------------------------------------------

  @e2e
  Scenario: Install current orchard release on a Linux x86_64 remote via subcommand
    Given the remote host has no orchard binary at /usr/local/bin/orchard
    When I run `orchard install-remote <host>`
    Then orchard downloads the release asset for the local version (pinned via CARGO_PKG_VERSION)
    And the asset matches the remote's `uname -m` (x86_64-unknown-linux-gnu)
    And the asset's sha256 is verified against the release's .sha256 file
    And orchard installs the binary to /usr/local/bin/orchard with mode 755 via `sudo install`
    And `ssh <host> orchard --version` prints the local version

  @e2e
  Scenario: Zero-state bootstrap via shell script when no local orchard exists
    Given the user does not yet have a local orchard binary
    When I run `scripts/install-orchard-remote.sh <host> <version>`
    Then the script downloads the specified release tarball + sha256
    And verifies the checksum
    And installs the binary to /usr/local/bin/orchard on the remote
    And `ssh <host> orchard --version` prints the requested version

  @integration
  Scenario: install-remote is idempotent when version already matches
    Given /usr/local/bin/orchard on the remote already reports the local version
    When I run `orchard install-remote <host>`
    Then orchard reports "already at <version>" and no-ops
    And exits 0 without invoking sudo

  @integration
  Scenario: install-remote performs a visible upgrade when versions differ
    Given /usr/local/bin/orchard on the remote reports an older version than local
    When I run `orchard install-remote <host>`
    Then orchard logs that it is upgrading from <old> to <new>
    And replaces the binary at /usr/local/bin/orchard
    And verifies the new version over a fresh SSH connection

  @integration
  Scenario: install-remote fails cleanly when sha256 does not match
    Given the downloaded tarball has been tampered with
    When orchard runs the sha256 verification step
    Then the install aborts before any write to /usr/local/bin/orchard
    And the error message names the expected and actual checksums
    And exits non-zero

  @integration
  Scenario: install-remote fails cleanly when the remote arch has no release asset
    Given the remote is aarch64-linux
    And the local release only publishes x86_64-unknown-linux-gnu
    When I run `orchard install-remote <host>`
    Then orchard aborts before any download
    And the error message names the detected arch and the missing asset
    And exits non-zero

  @integration
  Scenario: install-remote fails fast when sudo requires a password
    Given the remote user requires a password for sudo
    When orchard probes with `sudo -n true` during the precheck phase
    Then orchard aborts before attempting the install
    And the error message explains that passwordless sudo is required
    And exits non-zero

  # ---------------------------------------------------------------------------
  # AC2 — Non-interactive PATH
  # ---------------------------------------------------------------------------

  @e2e
  Scenario: Installed binary is on the non-interactive SSH PATH
    Given orchard has been installed via `install-remote` on a fresh boxd VM
    When I run `ssh <host> orchard --version`
    Then orchard responds with its version without relying on shell rc files
    And the binary path resolves from /usr/local/bin which is on the default non-interactive PATH

  @unit
  Scenario: ~/.local/bin is rejected as an install target
    When the install logic selects an install prefix
    Then it chooses /usr/local/bin (not ~/.local/bin)
    Because ~/.local/bin is not on the default non-interactive SSH PATH on Ubuntu 24.04

  # ---------------------------------------------------------------------------
  # AC3 — Bootstrap a boxd VM (golden image + existing forks)
  # ---------------------------------------------------------------------------

  @e2e
  Scenario: Bootstrap the langwatch-main-golden-image so future forks inherit orchard
    Given the host `langwatch-main-golden-image.boxd.sh` is reachable
    When I follow the documented golden-image bootstrap recipe
    Then orchard is installed at /usr/local/bin/orchard on the golden image
    And future forks from this golden automatically inherit the binary

  @e2e
  Scenario Outline: Bootstrap each existing boxd fork that predates orchard on the golden
    Given the host `<host>` was forked before orchard was added to the golden
    When I run `orchard install-remote <host>`
    Then orchard is installed at /usr/local/bin/orchard on `<host>`
    And `ssh <host> orchard --version` matches the local version

    Examples:
      | host                         |
      | issue3201.boxd.sh            |
      | ai-gateway-3327.boxd.sh      |
      | orchard-rs.boxd.sh           |

  @integration
  Scenario: `--all-orchard-proxy` re-installs across every orchard-proxy remote
    Given multiple remotes in ~/.config/orchard/config.json have "type": "orchard-proxy"
    When I run `orchard install-remote --all-orchard-proxy`
    Then orchard iterates over each orchard-proxy remote
    And installs (or no-ops if already at the target version) on each one
    And reports per-host pass/fail in a summary at the end

  # ---------------------------------------------------------------------------
  # AC4 — `"type": "orchard-proxy"` config example + behavior delta
  # ---------------------------------------------------------------------------

  @e2e
  Scenario: Documentation shows how to flip a remote to orchard-proxy
    Given `docs/remote-install.md` exists
    When I read the "Flipping a remote to orchard-proxy" section
    Then it shows the exact JSON edit in ~/.config/orchard/config.json
    And explains the behavior delta: snapshot-backed, SWR reads, error on missing remote binary
    And warns that malformed JSON breaks cache-load on startup

  @integration
  Scenario: Flipping a remote to orchard-proxy routes reads through OrchardProxyAdapter
    Given a remote `<host>` has orchard installed
    And ~/.config/orchard/config.json sets that remote's "type" to "orchard-proxy"
    When local orchard performs a refresh
    Then the remote's worktrees are fetched via `ssh <host> orchard --json`
    And are merged into OrchardState without re-running the local join pipeline
    And no `remote_adapter.proxy_failure` event is written for that host

  @integration
  Scenario: Missing remote binary surfaces a proxy_failure event (no silent fallback)
    Given a remote is configured as "type": "orchard-proxy"
    And the remote does not have orchard installed
    When local orchard refreshes that host
    Then OrchardProxyAdapter returns AdapterError::FetchFailure with "remote orchard missing (exit 127)"
    And a `remote_adapter.proxy_failure` event is appended to events.jsonl with the host and reason
    And the last-known cached snapshot for that host remains on disk

  # ---------------------------------------------------------------------------
  # AC5 — Smoke test: OrchardProxy source + valid v6 snapshot
  # ---------------------------------------------------------------------------

  @e2e
  Scenario: After bootstrap + flip, local --json shows worktrees sourced via OrchardProxy
    Given `<host>` has orchard installed
    And `<host>` is configured as "type": "orchard-proxy" in local config
    When I run `orchard --json` locally
    Then the remote's worktrees appear under the correct repo slug
    And each remote worktree's `source` field is `"orchard-proxy"` (not synthesized by a legacy adapter)

  @e2e
  Scenario: Remote orchard --json returns a valid v6 snapshot
    Given `<host>` has orchard installed at the expected version
    When I run `ssh <host> orchard --json`
    Then the output is valid JSON
    And the top-level `version` field equals 6 (matches SUPPORTED_JSON_OUTPUT_VERSIONS)
    And the snapshot parses successfully through `check_json_output_version`

  @integration
  Scenario: Unknown JSON output version surfaces ParseFailure (version-skew protection)
    Given a remote returns a JsonOutput with `version` outside SUPPORTED_JSON_OUTPUT_VERSIONS
    When OrchardProxyAdapter ingests the response
    Then `check_json_output_version` returns AdapterError::ParseFailure
    And a `remote_adapter.proxy_failure` event is written
    And the cached snapshot on disk is not overwritten

  @unit
  Scenario: JsonWorktree and JsonSession carry a `source` discriminator
    Given a JsonWorktree is serialized
    Then it includes a `source` field of type string
    And the value is one of: "local", "orchard-proxy", "remmy", "boxd-fork", "boxd-shared"
    And deserializing an older snapshot without the field defaults `source` to "local"

  @unit
  Scenario: merge_remote_snapshot stamps source="orchard-proxy" for OrchardProxy remotes
    Given a JsonOutput snapshot was fetched via OrchardProxyAdapter for `<host>`
    When merge_remote_snapshot folds it into local OrchardState
    Then every worktree and session originating from `<host>` has `source` set to "orchard-proxy"

# --- AC Coverage Map ---
# AC1: "Install recipe — skill or docs show how to install the current orchard release
#      on a Linux x86_64 host over SSH; downloads the right asset, installs to a path
#      on non-interactive $PATH, confirms with `ssh host orchard --version`" →
#        - Scenario: Install current orchard release on a Linux x86_64 remote via subcommand (@e2e)
#        - Scenario: Zero-state bootstrap via shell script when no local orchard exists (@e2e)
#        - Scenario: install-remote is idempotent when version already matches (@integration)
#        - Scenario: install-remote performs a visible upgrade when versions differ (@integration)
#        - Scenario: install-remote fails cleanly when sha256 does not match (@integration)
#        - Scenario: install-remote fails cleanly when the remote arch has no release asset (@integration)
#        - Scenario: install-remote fails fast when sudo requires a password (@integration)
#
# AC2: "Non-interactive PATH — install script leaves orchard discoverable from
#      `ssh host orchard`, not just interactive shells; tested on a fresh boxd VM" →
#        - Scenario: Installed binary is on the non-interactive SSH PATH (@e2e)
#        - Scenario: ~/.local/bin is rejected as an install target (@unit)
#
# AC3: "Bootstrap a boxd VM — document (and optionally script) the one-shot
#      bootstrap for the boxd fleet: golden-image install so new forks inherit
#      orchard; includes issue3201, ai-gateway-3327, orchard-rs,
#      langwatch-main-golden-image" →
#        - Scenario: Bootstrap the langwatch-main-golden-image so future forks inherit orchard (@e2e)
#        - Scenario Outline: Bootstrap each existing boxd fork that predates orchard on the golden (@e2e)
#        - Scenario: `--all-orchard-proxy` re-installs across every orchard-proxy remote (@integration)
#
# AC4: "`\"type\": \"orchard-proxy\"` config example — document how to flip a
#      remote in ~/.config/orchard/config.json from boxd-fork to orchard-proxy
#      once orchard is installed there, and what changes in behavior
#      (snapshot-backed, SWR, error on missing binary)" →
#        - Scenario: Documentation shows how to flip a remote to orchard-proxy (@e2e)
#        - Scenario: Flipping a remote to orchard-proxy routes reads through OrchardProxyAdapter (@integration)
#        - Scenario: Missing remote binary surfaces a proxy_failure event (no silent fallback) (@integration)
#
# AC5: "Smoke test — after bootstrap + config flip on one boxd VM, `orchard --json`
#      locally shows the remote worktrees sourced via OrchardProxyAdapter (not
#      adapter-synthesized), and `orchard --json` run on the VM itself returns
#      a valid v6 snapshot" →
#        - Scenario: After bootstrap + flip, local --json shows worktrees sourced via OrchardProxy (@e2e)
#        - Scenario: Remote orchard --json returns a valid v6 snapshot (@e2e)
#        - Scenario: Unknown JSON output version surfaces ParseFailure (version-skew protection) (@integration)
#        - Scenario: JsonWorktree and JsonSession carry a `source` discriminator (@unit)
#        - Scenario: merge_remote_snapshot stamps source="orchard-proxy" for OrchardProxy remotes (@unit)
#
# Coverage summary: 5 ACs → 20 scenarios (all mapped, no gaps)
