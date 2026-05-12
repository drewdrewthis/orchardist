Feature: Version drift visibility + local `orchard upgrade` (#417)
  As an orchardist running a federated orchard fleet
  I want each daemon to advertise its version + schema-contract-version, derive a per-peer compatibility class, surface drift in the TUI/CLI, and let me self-update the local Rust CLI
  So that schema drift across federation peers is visible before it silently breaks GraphQL joins, and upgrading the local CLI does not require re-running `cargo install` by hand

  # Scope: visibility plumbing + LOCAL upgrade only. Peer-driven upgrade
  # (`Mutation.upgradeSelf`, `orchard upgrade --peer`, `--all-peers`) is
  # split into a follow-up issue with its own auth + supervisor-presence
  # ACs. See the Investigation + Plan sections of #417.

  Background:
    Given the Go daemon at `cmd/orchard-daemon/main.go` declares `var version = "dev"`
    And the daemon's GraphQL schema lives at `internal/server/graphql/schema.graphql`
    And the daemon's peerproxy provider at `internal/server/providers/peerproxy/` can already speak `graphql-transport-ws` + HTTPS POST to peers
    And the Rust CLI at `crates/orchard/src/main.rs` has a stub `handle_upgrade()` that only prints a release-page URL
    And `release-please-config.json` currently tracks only the Rust crate (`crates/orchard`)

  # =======================================================================
  # AC1 — Daemon version baked at release time via -ldflags
  # =======================================================================

  @integration @issue-417
  Scenario: Release build bakes a real semver into the daemon binary
    Given the Makefile `daemon` target is invoked with `VERSION=1.2.3`
    When the daemon is built
    Then the resulting `orchard-daemon` binary, when run with `--version`, prints `1.2.3`
    And the bake mechanism is `-ldflags -X main.version=1.2.3` (no manual constant edits)

  @integration @issue-417
  Scenario: Plain `go build` without -ldflags keeps `dev` as the version
    Given a developer runs `go build ./cmd/orchard-daemon` with no ldflags
    When the resulting binary is run with `--version`
    Then it prints `dev`
    And the same string is what `Query.version` will return at runtime

  @integration @issue-417
  Scenario: Release-please Go build matrix bakes the version via the same flag
    Given `.github/workflows/release-please.yml` runs the daemon build job
    When the workflow builds `cmd/orchard-daemon` on linux × {amd64, arm64} and darwin × {amd64, arm64}
    Then every artifact embeds the release-please-published semver via `-X main.version=<semver>`
    And running `--version` on the downloaded binary prints that same semver

  # =======================================================================
  # AC2 — Query.version resolver exposes the baked version
  # =======================================================================

  @e2e @issue-417
  Scenario: `Query.version` returns the baked binary version
    Given a daemon built with `-X main.version=1.2.3`
    When a client queries `{ version }` against the daemon's `/graphql` endpoint
    Then the response is `{ "data": { "version": "1.2.3" } }`
    And the resolver is non-nullable (`String!`)

  @e2e @issue-417
  Scenario: `Query.version` returns `dev` on a non-release build
    Given a daemon built without `-ldflags`
    When a client queries `{ version }` against the daemon's `/graphql` endpoint
    Then the response is `{ "data": { "version": "dev" } }`

  # =======================================================================
  # AC3 — Host.version populated for local + peers (ferried over peerproxy)
  # =======================================================================

  @e2e @issue-417
  Scenario: `Host.version` on the local host returns the local daemon's baked version
    Given a daemon built with `-X main.version=1.2.3`
    When a client queries `{ host { version } }` against that daemon
    Then `host.version` equals `1.2.3`

  @e2e @issue-417
  Scenario: `Host.peers[].version` is populated by ferrying `query { version }` over peerproxy
    Given a local daemon at version `1.2.3` configured with peer `boxd-vm` at version `1.2.4`
    And the peerproxy probe has successfully reconnected to `boxd-vm`
    When a client queries `{ host { peers { version } } }` against the local daemon
    Then `peers[0].version` equals `1.2.4`
    And the daemon fetched the value via `peerproxy.Provider.Query(ctx, "boxd-vm", "{ version }", nil)` on probe/reconnect, not on every GraphQL request

  @integration @issue-417
  Scenario: `Host.peers[].version` is null when the peer is unreachable
    Given a local daemon configured with peer `dead-vm` whose peerproxy probe has failed
    When a client queries `{ host { peers { version } } }` against the local daemon
    Then `peers[0].version` is null
    And no error is raised (null is the legitimate value for "unknown")

  # =======================================================================
  # AC4 — Host.schemaContractVersion (explicit integer counter)
  # =======================================================================

  @e2e @issue-417
  Scenario: `Host.schemaContractVersion` on the local host returns the package-level constant
    Given the daemon package declares `const SchemaContractVersion = 1`
    When a client queries `{ host { schemaContractVersion } }` against the daemon
    Then the response value equals `1`
    And the field is non-nullable (`Int!`)

  @e2e @issue-417
  Scenario: `Host.peers[].schemaContractVersion` is ferried over peerproxy alongside version
    Given a local daemon with `SchemaContractVersion = 1` configured with peer `boxd-vm` whose daemon has `SchemaContractVersion = 2`
    And the peerproxy probe has successfully reconnected to `boxd-vm`
    When a client queries `{ host { peers { schemaContractVersion } } }` against the local daemon
    Then `peers[0].schemaContractVersion` equals `2`

  @integration @issue-417
  Scenario: `Host.peers[].schemaContractVersion` is null on unreachable peers
    Given a local daemon configured with peer `dead-vm` whose peerproxy probe has failed
    When a client queries `{ host { peers { schemaContractVersion } } }` against the local daemon
    Then `peers[0].schemaContractVersion` is null

  @integration @issue-417
  Scenario: An older peer that lacks the field resolves to null, not error
    Given a pre-#417 peer daemon that does not yet expose `Query.schemaContractVersion`
    When the local daemon's peerproxy probe asks for `{ schemaContractVersion }` against that peer
    Then the local daemon records the peer's `schemaContractVersion` as null
    And a subsequent `Host.peers[].schemaContractVersion` query returns null without raising

  # =======================================================================
  # AC5 — Host.peers[].compatibilityClass derivation (golden cases)
  # =======================================================================

  @unit @issue-417
  Scenario Outline: `compatibilityClass` is derived from local vs peer `schemaContractVersion`
    Given local `schemaContractVersion` is <local>
    And peer `schemaContractVersion` is <peer>
    When `compatibilityClass` is computed for that peer
    Then the result equals <expectedClass>

    Examples:
      | local | peer | expectedClass  |
      | 1     | 1    | COMPATIBLE     |
      | 2     | 2    | COMPATIBLE     |
      | 2     | 1    | DEGRADED       |
      | 1     | 2    | DEGRADED       |
      | 3     | 1    | INCOMPATIBLE   |
      | 1     | 3    | INCOMPATIBLE   |
      | 1     | null | UNKNOWN        |
      | null  | 1    | UNKNOWN        |
      | null  | null | UNKNOWN        |

  @e2e @issue-417
  Scenario: `Host.peers[].compatibilityClass` is non-nullable in the schema
    Given the daemon's GraphQL schema
    When introspection runs against `Host.peers` element type
    Then the `compatibilityClass` field is declared as `CompatibilityClass!`
    And the enum `CompatibilityClass` has exactly the values { COMPATIBLE, DEGRADED, INCOMPATIBLE, UNKNOWN }

  # =======================================================================
  # AC6 — Drift banner in TUI + `orchard status`
  # =======================================================================

  @e2e @issue-417
  Scenario: Drift banner appears when any peer is DEGRADED on a released local daemon
    Given the local daemon was built with `-X main.version=1.2.3`
    And the local daemon has one peer `boxd-vm` whose `compatibilityClass` is `DEGRADED`
    When the operator opens the TUI
    Then a drift banner row is rendered above the worktree list
    And the banner names the peer `boxd-vm`
    And the banner instructs the operator to run `orchard upgrade` on that host

  @e2e @issue-417
  Scenario: Drift banner appears when any peer is INCOMPATIBLE on a released local daemon
    Given the local daemon was built with `-X main.version=1.2.3`
    And the local daemon has peers `[boxd-vm: COMPATIBLE, drew-mac: INCOMPATIBLE]`
    When the operator opens the TUI
    Then a drift banner row is rendered
    And the banner names `drew-mac` (the INCOMPATIBLE peer)

  @e2e @issue-417
  Scenario: Drift banner is hidden when local version is `dev` even if peers are drifted
    Given the local daemon reports `version = "dev"` (no ldflags)
    And the local daemon has one peer whose `compatibilityClass` is `INCOMPATIBLE`
    When the operator opens the TUI
    Then no drift banner is rendered
    And the dev loop is not polluted by drift warnings

  @e2e @issue-417
  Scenario: Drift banner is hidden when all peers are COMPATIBLE or UNKNOWN
    Given the local daemon was built with `-X main.version=1.2.3`
    And every peer's `compatibilityClass` is either `COMPATIBLE` or `UNKNOWN`
    When the operator opens the TUI
    Then no drift banner is rendered

  @e2e @issue-417
  Scenario: `orchard status` CLI surfaces the same drift banner
    Given the local daemon was built with `-X main.version=1.2.3`
    And the local daemon has one peer whose `compatibilityClass` is `DEGRADED`
    When the operator runs `orchard status`
    Then the CLI output includes a drift banner naming that peer
    And the banner instructs the operator to run `orchard upgrade` on that host

  # =======================================================================
  # AC7 — `orchard upgrade` (local) via the `self_update` crate
  # =======================================================================

  @e2e @issue-417
  Scenario: `orchard upgrade` self-updates the local Rust CLI from the latest GitHub release
    Given the user is running `orchard` version `1.2.3`
    And GitHub has a release at `1.2.4` with a matching asset for the current platform
    When the user runs `orchard upgrade`
    Then the `self_update` crate fetches the `1.2.4` asset for the current LLVM triple
    And the running binary is atomically replaced on disk
    And the command exits with status 0

  @e2e @issue-417
  Scenario: `orchard upgrade --check` reports the available release without writing
    Given the user is running `orchard` version `1.2.3`
    And GitHub has a release at `1.2.4` available
    When the user runs `orchard upgrade --check`
    Then the output reports `1.2.3 → 1.2.4`
    And the binary on disk is unchanged

  @e2e @issue-417
  Scenario: `orchard upgrade --check` reports up-to-date when no newer release exists
    Given the user is running `orchard` version `1.2.4`
    And GitHub's latest release is `1.2.4`
    When the user runs `orchard upgrade --check`
    Then the output indicates the local binary is already up to date

  @unit @issue-417
  Scenario: Asset-name template is explicit per binary (no inferred matcher)
    Given the `self_update` configuration for the Rust CLI
    Then the asset-name template uses the LLVM target triple (e.g. `x86_64-apple-darwin`)
    And the template is declared explicitly in code, not relying on `self_update`'s defaults

  @unit @issue-417
  Scenario: `orchard upgrade` rejects `--peer` and `--all-peers` flags in this issue's scope
    When the user runs `orchard upgrade --peer boxd-vm`
    Then the CLI exits with a non-zero status
    And the error message indicates peer-driven upgrade is out of scope for this release
    And points the user at the follow-up issue

  # =======================================================================
  # AC8 — Release-please tracks `cmd/orchard-daemon` as a second package
  # =======================================================================

  @integration @issue-417
  Scenario: `release-please-config.json` declares the daemon as a Go package
    Given the release-please configuration
    Then it has an entry for `cmd/orchard-daemon` with `"release-type": "go"`
    And the existing entry for `crates/orchard` is unchanged

  @integration @issue-417
  Scenario: The release workflow builds the daemon across linux/darwin × amd64/arm64
    Given `.github/workflows/release-please.yml` runs after a release-please PR merges
    When the daemon build matrix executes
    Then artifacts are produced for { linux/amd64, linux/arm64, darwin/amd64, darwin/arm64 }
    And each artifact is published to GitHub Releases as `orchard-daemon_<version>_<goos>_<goarch>.tar.gz`
    And every artifact embeds the release version via `-X main.version=<version>`

  @integration @issue-417
  Scenario: Both packages tag from the same release SHA on a single release-please PR
    Given a commit on `main` that touches both `crates/orchard` and `cmd/orchard-daemon`
    When release-please opens its next release PR
    Then the PR bumps both package versions
    And merging it tags both packages from the same SHA
    And the GitHub Releases page shows both Rust and Go artifacts side by side

  # =======================================================================
  # AC9 — Bootstrap doc for pre-#417 daemons
  # =======================================================================

  @e2e @issue-417
  Scenario: Bootstrap doc explains the one-time manual upgrade for pre-#417 daemons
    Given the repository at `main` after #417 merges
    Then `docs/operations/upgrade.md` (or the next-best home agreed by review) exists
    And it documents how to manually copy a new daemon binary onto a pre-#417 peer once
    And it explicitly notes that subsequent upgrades will surface in the drift banner
    And it does NOT prescribe new SSH plumbing

  @e2e @issue-417
  Scenario: The drift banner links to the bootstrap doc
    Given the drift banner is rendered (any peer DEGRADED or INCOMPATIBLE, local version != `dev`)
    When the operator reads the banner text
    Then the banner includes a stable path or URL pointing at the bootstrap doc

# --- AC Coverage Map ---
# AC1 "Daemon version baked at release time via -ldflags" →
#   - Release build bakes a real semver into the daemon binary
#   - Plain `go build` without -ldflags keeps `dev` as the version
#   - Release-please Go build matrix bakes the version via the same flag
# AC2 "Query.version: String! resolver returns the baked version" →
#   - `Query.version` returns the baked binary version
#   - `Query.version` returns `dev` on a non-release build
# AC3 "Host.version populated for local + ferried from peers, null on unreachable" →
#   - `Host.version` on the local host returns the local daemon's baked version
#   - `Host.peers[].version` is populated by ferrying `query { version }` over peerproxy
#   - `Host.peers[].version` is null when the peer is unreachable
# AC4 "Host.schemaContractVersion: Int! integer counter, bumped manually, surfaced for local + peers, null on unreachable" →
#   - `Host.schemaContractVersion` on the local host returns the package-level constant
#   - `Host.peers[].schemaContractVersion` is ferried over peerproxy alongside version
#   - `Host.peers[].schemaContractVersion` is null on unreachable peers
#   - An older peer that lacks the field resolves to null, not error
# AC5 "Host.peers[].compatibilityClass: CompatibilityClass! with all four golden cases" →
#   - `compatibilityClass` is derived from local vs peer `schemaContractVersion` (Scenario Outline covers all 4 classes)
#   - `Host.peers[].compatibilityClass` is non-nullable in the schema
# AC6 "Drift banner in TUI + `orchard status`, gated on peer DEGRADED/INCOMPATIBLE AND local != dev" →
#   - Drift banner appears when any peer is DEGRADED on a released local daemon
#   - Drift banner appears when any peer is INCOMPATIBLE on a released local daemon
#   - Drift banner is hidden when local version is `dev` even if peers are drifted
#   - Drift banner is hidden when all peers are COMPATIBLE or UNKNOWN
#   - `orchard status` CLI surfaces the same drift banner
# AC7 "`orchard upgrade` (local) via self_update crate, --check flag, no --peer/--all-peers" →
#   - `orchard upgrade` self-updates the local Rust CLI from the latest GitHub release
#   - `orchard upgrade --check` reports the available release without writing
#   - `orchard upgrade --check` reports up-to-date when no newer release exists
#   - Asset-name template is explicit per binary (no inferred matcher)
#   - `orchard upgrade` rejects `--peer` and `--all-peers` flags in this issue's scope
# AC8 "Release-please tracks cmd/orchard-daemon, builds linux × {amd64,arm64} and darwin × {amd64,arm64}" →
#   - `release-please-config.json` declares the daemon as a Go package
#   - The release workflow builds the daemon across linux/darwin × amd64/arm64
#   - Both packages tag from the same release SHA on a single release-please PR
# AC9 "Bootstrap doc for pre-#417 daemons; drift banner links to it" →
#   - Bootstrap doc explains the one-time manual upgrade for pre-#417 daemons
#   - The drift banner links to the bootstrap doc
#
# Coverage check: 9 ACs in the issue body → all 9 mapped. ✓
