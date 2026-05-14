Feature: Collapse fleet-manifest into orchard config (peers[].purpose)
  As an orchardist running the local orchard daemon
  I want the standalone fleet-manifest subsystem replaced by a single optional `purpose`
  field on `peers[]` in `~/.orchard/config.json`
  So that fleet truth has one writer (peer config), the schema sheds the rotting
  lifecycle ceremony (`role`, `ownerOrchardist`, `decommissionSignal`, `lastVerified`,
  `inManifest`), and `/fleet-list` reads the daemon directly instead of regex-parsing
  hand-maintained YAML.

  # Refactor — sequenced inside-out per /plan:
  #   1) PeerRow.Purpose added to config (AC #1)
  #   2) Host.purpose enriched from peer config via alias-chain match (AC #2)
  #   3) Manifest schema + code stripped (AC #3, #4)
  #   4) Client schema snapshots + Houdini artifacts refreshed (AC #5, #6)
  #   5) Codex cleanup committed separately on drewdrewthis/orchard-codex (AC #7)
  #
  # Trade-off accepted: after ship, `{ hosts }` on the local daemon returns ~2 rows
  # (local + peer) where today it returns 8. The 6 manifest-only stubs (non-peer
  # federation hosts) are no longer represented in the local schema. Federated
  # fleet visibility (querying via boxd_orchardist) is a follow-on, not blocking.
  #
  # Code surface (audited by /investigate):
  #   - Config schema:    internal/server/providers/config/types.go (PeerRow)
  #   - Host enrichment:  internal/server/resolvers/schema.resolvers.go (hosts resolver)
  #   - Removed:          internal/server/providers/manifest/* (4 files),
  #                       internal/server/resolvers/manifest_merge{,_test}.go,
  #                       internal/server/manifest_e2e_test.go,
  #                       WithManifest() Option in server.go + resolvers/resolver.go,
  #                       manifest provider construction in internal/cli/daemon/daemon.go
  #   - Client snapshots: crates/orchard/schema.graphql (top-level),
  #                       crates/orchard-gui/schema.graphql + Houdini artifacts

  Background:
    Given the orchard daemon loads `peers[]` from `~/.orchard/config.json` at startup
    And the PeerRow struct lives in `internal/server/providers/config/types.go`
    And the Host GraphQL type is generated via gqlgen from `schema.graphql`
    And `Host.purpose` is sourced from `peers[].purpose` via the alias-chain match (no manifest subsystem)
    And the daemon already exposes a working `Host` type with `hostname`, `address`, `reachable`, `lastSeenAt`

  # =======================================================================
  # AC 1 — `peers[]` entries accept an optional `purpose: string`
  # =======================================================================

  @unit
  Scenario: PeerRow round-trips a config entry that includes `purpose`
    Given a `~/.orchard/config.json` whose `peers[0]` has `"purpose": "boxd_orchardist on graphql.orchard.boxd.sh"`
    When the config is loaded into a `PeerRow` and then re-serialized to JSON
    Then the resulting JSON preserves `"purpose": "boxd_orchardist on graphql.orchard.boxd.sh"` byte-for-byte
    And no other peer fields are mutated

  @unit
  Scenario: PeerRow round-trips a config entry without `purpose` (no migration)
    Given a `~/.orchard/config.json` whose `peers[0]` has no `purpose` field
    When the config is loaded into a `PeerRow` and then re-serialized to JSON
    Then the resulting JSON does NOT contain a `"purpose": ""` key
    # `omitempty` on `PeerRow.Purpose` is the load-bearing detail. Existing configs
    # must not pick up an empty-string field on round-trip.

  @unit
  Scenario: PeerRow accepts `purpose` alongside the existing `tls` and address fields
    Given a peer config row with `name`, `address`, `tls: true`, and `purpose: "p"`
    When the config is loaded
    Then all four fields are populated on the resulting `PeerRow`
    And no validation error is raised

  # =======================================================================
  # AC 2 — `Host.purpose` is populated from peer config (matched on alias chain)
  # =======================================================================

  @unit
  Scenario: Host.purpose returns the matched peer's purpose by hostname
    Given a peer config entry `{ name: "orchard", address: "graphql.orchard.boxd.sh", purpose: "boxd_orchardist" }`
    And a `Host` whose `hostname == "graphql.orchard.boxd.sh"`
    When the `Host.purpose` field is resolved
    Then the resolver returns `"boxd_orchardist"`

  @unit
  Scenario: Host.purpose returns the matched peer's purpose by machine ID
    Given a peer config entry whose address resolves to a host with `machineID == "abc123"`
    And a `Host` whose `machineID == "abc123"` (but whose hostname differs from the peer address)
    When the `Host.purpose` field is resolved
    Then the resolver returns the peer's `purpose`
    # Alias-chain match shape: MachineID -> Hostname -> stripSSHUser(Address).
    # Identical logic to the prior `lookupManifestForHost`, sourced from peers[].

  @unit
  Scenario: Host.purpose matches when peer address has a `boxd@` SSH-user prefix
    Given a peer config entry with `address: "boxd@graphql.orchard.boxd.sh"`
    And a `Host` whose `hostname == "graphql.orchard.boxd.sh"`
    When the `Host.purpose` field is resolved
    Then the resolver returns the peer's `purpose`
    And the match succeeds via address-host-portion stripping

  @unit
  Scenario: Host.purpose is null when no peer matches
    Given no peer config entry matches the host by any alias
    When the `Host.purpose` field is resolved on the wire
    Then the GraphQL response carries `purpose: null`
    # `purposeForLocalHost` returns `""`; the resolver only assigns
    # `host.Purpose = &p` when `p != ""`, so the wire value is null.

  @unit
  Scenario: Host.purpose is null when the matched peer has no `purpose`
    Given a peer config entry that matches the host but has no `purpose` field set
    When the `Host.purpose` field is resolved on the wire
    Then the GraphQL response carries `purpose: null`

  @integration
  Scenario: Manifest provider is no longer wired into daemon bootstrap
    Given the daemon source code after the collapse
    When the daemon is started
    Then no manifest provider is constructed in `internal/cli/daemon/daemon.go`
    And no `WithManifest()` option is invoked on the server
    And `Host.purpose` is populated solely from the loaded `peers[]` config

  # =======================================================================
  # AC 3 — Manifest-only schema surface is removed
  # =======================================================================

  @integration
  Scenario: Host type drops the 5 manifest-only fields
    Given the rebuilt daemon is serving GraphQL on 127.0.0.1:7777
    When the client introspects `{ __type(name:"Host"){ fields { name } } }`
    Then the field list does NOT contain `role`
    And the field list does NOT contain `ownerOrchardist`
    And the field list does NOT contain `decommissionSignal`
    And the field list does NOT contain `lastVerified`
    And the field list does NOT contain `inManifest`
    And the field list DOES contain `purpose`
    And the field list DOES contain `hostname`, `address`, `reachable`, `lastSeenAt`

  @integration
  Scenario: HostRole enum is removed from the schema
    Given the rebuilt daemon is serving GraphQL
    When the client introspects `{ __type(name:"HostRole"){ name } }`
    Then the response returns `null` for `__type`

  @integration
  Scenario: ManifestStatus type is removed from the schema
    Given the rebuilt daemon is serving GraphQL
    When the client introspects `{ __type(name:"ManifestStatus"){ name } }`
    Then the response returns `null` for `__type`

  @integration
  Scenario: Health.manifest field is removed from the schema
    Given the rebuilt daemon is serving GraphQL
    When the client introspects `{ __type(name:"Health"){ fields { name } } }`
    Then the field list does NOT contain `manifest`

  # =======================================================================
  # AC 4 — Manifest subsystem code is fully deleted from the daemon
  # =======================================================================

  @unit
  Scenario: `internal/server/providers/manifest/` directory no longer exists
    Given the daemon source tree after the collapse
    When the tree is walked for `internal/server/providers/manifest/`
    Then the directory does not exist
    And its prior contents (`doc.go`, `provider.go`, `parse.go`, `provider_test.go`) are gone

  @unit
  Scenario: Manifest resolver glue files are deleted
    Given the daemon source tree after the collapse
    Then `internal/server/resolvers/manifest_merge.go` does not exist
    And `internal/server/resolvers/manifest_merge_test.go` does not exist
    And `internal/server/manifest_e2e_test.go` does not exist

  @unit
  Scenario: `WithManifest()` Option is removed from server.go and resolvers/resolver.go
    Given the daemon source tree after the collapse
    When the source is grepped for `WithManifest`
    Then no matches are found in `internal/server/server.go`
    And no matches are found in `internal/server/resolvers/resolver.go`

  @unit
  Scenario: Manifest provider construction is removed from the daemon CLI entry point
    Given the daemon source tree after the collapse
    When `internal/cli/daemon/daemon.go` is inspected
    Then no manifest provider is constructed
    And no manifest is passed into the server builder

  @integration
  Scenario: `FLEET_MANIFEST` environment variable no longer affects daemon behaviour
    Given the rebuilt daemon
    When the daemon is started with `FLEET_MANIFEST=/some/path/manifest.yaml`
    Then the daemon does not attempt to read `/some/path/manifest.yaml`
    And `Host.purpose` is still populated solely from `peers[]` config

  @integration
  Scenario: Daemon Go suite passes after manifest removal
    Given the daemon source tree after the collapse
    When `go test ./internal/server/...` is run
    Then the suite passes
    And no test references the removed manifest types

  @integration
  Scenario: `make generate && go build ./...` succeeds after schema changes
    Given the daemon source tree after the collapse
    When `make generate` is run, then `schema.resolvers.go` is restored from snapshot and the `sortKey` rename re-applied
    And `go build ./...` is run
    Then both commands exit zero
    # gqlgen trap noted in CLAUDE.md: snapshot `schema.resolvers.go` to /tmp/ before
    # regenerating and re-apply the `sortKey` rename after.

  # =======================================================================
  # AC 5 — Client schema snapshots regenerated; TUI + GUI render without regression
  # =======================================================================

  @integration
  Scenario: `crates/orchard/schema.graphql` is refreshed against the new daemon schema
    Given the rebuilt daemon is serving the new schema on 127.0.0.1:7777
    When the Rust client's schema snapshot at `crates/orchard/schema.graphql` is regenerated
    Then the snapshot file does NOT contain `Host.role`
    And the snapshot file does NOT contain `Host.ownerOrchardist`
    And the snapshot file does NOT contain `Host.decommissionSignal`
    And the snapshot file does NOT contain `Host.lastVerified`
    And the snapshot file does NOT contain `Host.inManifest`
    And the snapshot file does NOT contain `HostRole`
    And the snapshot file does NOT contain `ManifestStatus`
    And the snapshot file does NOT contain `Health.manifest`
    And the snapshot file DOES contain `Host.purpose`

  @integration
  Scenario: `crates/orchard-gui/schema.graphql` is refreshed via Houdini against the live daemon
    Given the rebuilt daemon is serving the new schema
    When `pnpm dev` is run in `crates/orchard-gui/` and Houdini polls `127.0.0.1:7777/graphql`
    Then the regenerated `crates/orchard-gui/schema.graphql` reflects the new shape
    And the regenerated Houdini artifacts under `crates/orchard-gui/$houdini/` contain no references to the 5 removed Host fields, `HostRole`, `Health.manifest`, or `ManifestStatus`

  @integration
  Scenario: Rust TUI compiles cleanly against the refreshed schema
    Given the refreshed `crates/orchard/schema.graphql`
    When `cargo build` is run against `crates/orchard/`
    Then the build exits zero
    And no compile error references any removed schema field or type

  @integration
  Scenario: GUI dev server compiles cleanly against the refreshed schema
    Given the refreshed Houdini artifacts
    When the GUI dev server is started via `pnpm dev` in `crates/orchard-gui/`
    Then the dev server starts without errors
    And no console error references any removed schema field or type

  @e2e
  Scenario: TUI renders the dashboard against the rebuilt daemon without errors
    Given the rebuilt daemon is running on 127.0.0.1:7777
    And the TUI binary is built from the refreshed `crates/orchard/`
    When the operator launches `orchard`
    Then the dashboard renders all worktree rows
    And no panic, no rendering error, no missing-field error is observed
    And `orchard --json` returns a valid `OrchardState` with no schema mismatch errors

  @e2e
  Scenario: GUI renders the dashboard against the rebuilt daemon without errors
    Given the rebuilt daemon is running on 127.0.0.1:7777
    And the GUI dev server is up against the refreshed Houdini artifacts
    When the user loads the dashboard in the browser
    Then every page that lists hosts renders without console errors
    And `HostsList.gql` selections (`hostname, os, kernel, reachable, lastSeenAt, resourceLoad`) still resolve

  # =======================================================================
  # AC 6 — No new client-side joins introduced
  # =======================================================================

  @unit
  Scenario: No new client-side enrichment of Host.purpose is introduced anywhere in the clients
    Given the daemon now exposes `Host.purpose` directly
    When the Rust and Svelte/Houdini client source trees are inspected for any code that derives or composes a `purpose` value outside a daemon GraphQL query
    Then no such derivation exists
    And every read of `purpose` flows through a GraphQL selection on `Host`

  @unit
  Scenario: ADR-016/017/018 compliance check — no client-side joins for fleet data
    Given the source trees of `crates/orchard/`, `crates/orchard-gui/`, and the orchard CLI
    When the trees are grepped for any new client-side join over hosts, peers, or manifest data
    Then no new join is introduced as part of this change
    # ADR-016/017/018: the daemon owns state. Clients call GraphQL.

  # =======================================================================
  # AC 7 — Codex cleanup (separate repo, out of this PR's scope)
  # =======================================================================
  #
  # AC 7 is explicitly OUT-OF-PR scope: it lands as a separate commit on
  # `drewdrewthis/orchard-codex` main after this PR merges and the rebuilt
  # daemon is running. Its scenarios live in that repo's feature spec, not
  # here — this daemon repo cannot satisfy or test changes that target
  # `~/.claude/` files.

  # =======================================================================
  # Proof scenarios (per issue "Proof" section)
  # =======================================================================

  @integration
  Scenario: Regression test asserts `Host.purpose` is populated from peer config
    Given a daemon test fixture with a single peer config entry having `purpose: "test-purpose"`
    And a matching host record
    When the test resolves `Host.purpose` for the matching host
    Then the resolver returns `"test-purpose"`
    # This is the explicit Proof item from the issue body.

  @integration
  Scenario: Introspection after rebuild confirms the schema surface is correct
    Given the rebuilt daemon
    When the client issues `curl -s -X POST http://127.0.0.1:7777/graphql -d '{"query":"{__type(name:\"Host\"){fields{name}}}"}'`
    Then the response field list omits `role`, `ownerOrchardist`, `decommissionSignal`, `lastVerified`, `inManifest`
    And the response field list includes `purpose`
    When the client issues `curl -s -X POST http://127.0.0.1:7777/graphql -d '{"query":"{__type(name:\"HostRole\"){name}}"}'`
    Then the response returns `null` for `__type`

  # =======================================================================
  # AC Coverage Map
  # =======================================================================

  # --- AC Coverage Map ---
  # AC 1 "Config schema: peers[] entries accept an optional purpose: string; existing configs remain valid"
  #   -> @unit "PeerRow round-trips a config entry that includes `purpose`"
  #   -> @unit "PeerRow round-trips a config entry without `purpose` (no migration)"
  #   -> @unit "PeerRow accepts `purpose` alongside the existing `tls` and address fields"
  #
  # AC 2 "Daemon GraphQL: Host.purpose is populated from peer config (matched on hostname/address); manifest provider no longer wired into bootstrap"
  #   -> @unit "Host.purpose returns the matched peer's purpose by hostname"
  #   -> @unit "Host.purpose returns the matched peer's purpose by machine ID"
  #   -> @unit "Host.purpose matches when peer address has a `boxd@` SSH-user prefix"
  #   -> @unit "Host.purpose is null when no peer matches"
  #   -> @unit "Host.purpose is null when the matched peer has no `purpose`"
  #   -> @integration "Manifest provider is no longer wired into daemon bootstrap"
  #   -> @integration "Regression test asserts `Host.purpose` is populated from peer config"
  #
  # AC 3 "GraphQL removals: Host.role, Host.ownerOrchardist, Host.decommissionSignal, Host.lastVerified, Host.inManifest, HostRole enum, Health.manifest, ManifestStatus type"
  #   -> @integration "Host type drops the 5 manifest-only fields"
  #   -> @integration "HostRole enum is removed from the schema"
  #   -> @integration "ManifestStatus type is removed from the schema"
  #   -> @integration "Health.manifest field is removed from the schema"
  #   -> @integration "Introspection after rebuild confirms the schema surface is correct"
  #
  # AC 4 "Daemon code removed: manifest provider dir, manifest_merge files, manifest_e2e_test, WithManifest() option, provider construction in daemon.go; FLEET_MANIFEST env var has no effect"
  #   -> @unit "`internal/server/providers/manifest/` directory no longer exists"
  #   -> @unit "Manifest resolver glue files are deleted"
  #   -> @unit "`WithManifest()` Option is removed from server.go and resolvers/resolver.go"
  #   -> @unit "Manifest provider construction is removed from the daemon CLI entry point"
  #   -> @integration "`FLEET_MANIFEST` environment variable no longer affects daemon behaviour"
  #   -> @integration "Daemon Go suite passes after manifest removal"
  #   -> @integration "`make generate && go build ./...` succeeds after schema changes"
  #
  # AC 5 "Client schema snapshots refreshed: crates/orchard/schema.graphql, crates/orchard-gui/schema.graphql, Houdini artifacts; TUI and GUI render without regression"
  #   -> @integration "`crates/orchard/schema.graphql` is refreshed against the new daemon schema"
  #   -> @integration "`crates/orchard-gui/schema.graphql` is refreshed via Houdini against the live daemon"
  #   -> @integration "Rust TUI compiles cleanly against the refreshed schema"
  #   -> @integration "GUI dev server compiles cleanly against the refreshed schema"
  #   -> @e2e "TUI renders the dashboard against the rebuilt daemon without errors"
  #   -> @e2e "GUI renders the dashboard against the rebuilt daemon without errors"
  #
  # AC 6 "No new client-side joins: any new consumer of Host.purpose reads it via daemon GraphQL only (ADR-016/017/018 compliance)"
  #   -> @unit "No new client-side enrichment of Host.purpose is introduced anywhere in the clients"
  #   -> @unit "ADR-016/017/018 compliance check — no client-side joins for fleet data"
  #
  # AC 7 "Codex cleanup (separate commit on orchard-codex main)"
  #   -> OUT-OF-PR-SCOPE — validated against the post-merge daemon in the
  #      orchard-codex repo's own feature spec, not in this daemon repo.
