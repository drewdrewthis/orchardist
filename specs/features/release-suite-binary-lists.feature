Feature: Single source for suite binary lists + cross-arch orchard-tui revision check (#820)
  As a release engineer for the orchard suite
  I want every toolchain-present script and CI job to read the suite/Go/Rust/revision binary sets from one source, install.sh's literal lists drift-checked against that source, and the orchard-tui revision stamp verified on every arch
  So that the hand-mirrored copies cannot silently drift, and a mis-stamped foreign-arch orchard-tui can no longer ship unverified

  Background:
    Given internal/release/assets.go declares `SuiteBinaries` = [orchard-daemon, orchard-sidebar, orchard-shell, orchard-upgrade, orchard-tui, orchard] (helpers first, dispatcher last)
    And internal/release/revision.go derives `RevisionBinaries` = `SuiteBinaries` minus `UnstampedBinaries` ([orchard])
    And orchard-tui and orchard are Rust binaries (crates/orchard); the other four are Go binaries (cmd/*)
    And cmd/orchard-status exists on disk but is deliberately absent from `SuiteBinaries`
    And scripts/install.sh runs via `curl | bash` on machines with no Go toolchain, so it keeps literal lists

  # =======================================================================
  # AC1 — Lister emits correct, ordered sets
  # =======================================================================

  @integration @issue-820
  Scenario: The lister prints each named set one binary per line in SuiteBinaries order
    Given internal/release/cmd/suite-bins is present
    When `go run ./internal/release/cmd/suite-bins <set>` is run for each of suite, go, rust, revision
    Then "suite" prints orchard-daemon, orchard-sidebar, orchard-shell, orchard-upgrade, orchard-tui, orchard in that order
    And "go" prints orchard-daemon, orchard-sidebar, orchard-shell, orchard-upgrade in that order
    And "rust" prints orchard-tui, orchard in that order
    And "revision" prints orchard-daemon, orchard-sidebar, orchard-shell, orchard-upgrade, orchard-tui in that order
    And each invocation exits 0

  # =======================================================================
  # AC2 — Unknown / missing set arg fails loudly
  # =======================================================================

  @integration @issue-820
  Scenario: An unknown set name is rejected with a non-zero exit
    Given internal/release/cmd/suite-bins is present
    When `go run ./internal/release/cmd/suite-bins bogus` is run
    Then it writes an error naming the valid sets to stderr
    And it prints nothing to stdout
    And it exits non-zero

  @integration @issue-820
  Scenario: A missing set arg is rejected with a non-zero exit
    Given internal/release/cmd/suite-bins is present
    When `go run ./internal/release/cmd/suite-bins` is run with no set argument
    Then it writes an error naming the valid sets to stderr
    And it exits non-zero

  # =======================================================================
  # AC3 — Go set derived from SuiteBinaries; excludes orchard-status; partitions cleanly
  # =======================================================================

  @unit @issue-820
  Scenario: GoBinaries and RustBinaries partition SuiteBinaries exactly
    Given the internal/release classification vars
    When the union of GoBinaries and RustBinaries is compared to SuiteBinaries
    Then the union equals SuiteBinaries with no overlap, no missing member, and no extra member
    And GoBinaries preserves SuiteBinaries order

  @integration @issue-820
  Scenario: The CLI go and rust sets partition the suite set with no overlap
    Given internal/release/cmd/suite-bins is present
    When the sorted concatenation of the go set and the rust set is diffed against the sorted suite set
    Then the diff is empty
    And the go set has four members and the rust set has two, summing to the suite set's six

  @unit @issue-820
  Scenario: A new Go binary added to SuiteBinaries joins GoBinaries with no other edit
    Given a copy of SuiteBinaries with a fake Go binary appended
    When GoBinaries is derived from it
    Then the fake binary appears in GoBinaries
    And it does not appear in RustBinaries

  @unit @issue-820
  Scenario: orchard-status is never classified as a suite Go binary
    Given cmd/orchard-status exists on disk but is not in SuiteBinaries
    When the go set is produced from SuiteBinaries
    Then orchard-status is absent from the go set

  # =======================================================================
  # AC4 — The three rewired files read the lister; no literal list on an executable line
  # =======================================================================

  @integration @issue-820
  Scenario: No hand-maintained binary-list literal remains on an executable line of the rewired files
    Given scripts/check-suite-revisions.sh, scripts/dist.sh, and .github/workflows/release-please.yml
    When each file is grepped for two adjacent suite-binary names, excluding comment lines
    Then each file returns zero matching non-comment lines
    And each of the three files invokes suite-bins at least once

  # =======================================================================
  # AC4b — install.sh stays literal but is drift-checked against the Go source
  # =======================================================================

  @unit @issue-820
  Scenario: A Go mirror-test asserts install.sh's literal lists equal the Go source
    Given scripts/install.sh keeps a literal SUITE_BINARIES array and a literal gobin loop, each with a comment stating why it is not rewired
    When TestInstallShMirrorsSuiteBinaries parses both constructs from install.sh
    Then SUITE_BINARIES equals SuiteBinaries in value and order
    And the gobin loop equals GoBinaries in value and order

  @unit @issue-820
  Scenario: A drifted install.sh literal fails the mirror-test, naming install.sh
    Given install.sh:69 SUITE_BINARIES is seeded with a divergence, such as a dropped orchard-tui
    When `go test ./internal/release -run TestInstallShMirrorsSuiteBinaries` runs
    Then the test fails and its output names install.sh
    And reverting the divergence makes the test pass

  # =======================================================================
  # AC5 — The drift guard fails on any reintroduced or divergent literal
  # =======================================================================

  @unit @issue-820
  Scenario: A literal list re-added to a rewired file in a different order fails the guard, naming the file
    Given a hardcoded binary-list literal is re-added to scripts/dist.sh in a different order than SuiteBinaries
    When the Go drift-guard test runs
    Then it fails and names dist.sh

  @unit @issue-820
  Scenario: A divergence seeded in install.sh fails the same guard, naming install.sh
    Given a divergence is seeded in scripts/install.sh:69
    When the Go drift-guard test runs
    Then it fails and names install.sh

  @unit @issue-820
  Scenario: The guard passes on a clean tree
    Given no rewired file carries a literal binary list and install.sh mirrors the Go source
    When the Go drift-guard test runs
    Then it exits 0

  # =======================================================================
  # AC6 — Lister calls are cross-compile-safe (failure mode)
  # =======================================================================

  @integration @issue-820
  Scenario: The lister runs on the host even when GOOS/GOARCH target a foreign arch
    Given GOOS=darwin and GOARCH=arm64 are exported in the shell
    When the exact captured lister command from the build-go step is run to produce the go set
    Then it prints the go set without an "exec format error"
    And every lister invocation in a GOOS/GOARCH-exported scope strips those vars

  # =======================================================================
  # AC7 — The assembled suite tarball still holds exactly the six binaries
  # =======================================================================

  @integration @issue-820
  Scenario: The assembled suite tarball holds exactly the six binaries, proven from tar tzf
    Given a suite tarball assembled after the rewire
    When `tar tzf orchard-suite-x86_64-unknown-linux-gnu.tar.gz` is listed and any leading ./ is stripped
    Then the sorted listing equals the six SuiteBinaries names sorted, with no adds and no drops
    And the crash-safe helpers-first, dispatcher-last replacement order is enforced by the consumer iterating SuiteBinaries, not by tar member order

  # =======================================================================
  # AC8 — check-suite-revisions.sh still passes clean / fails on skew; module-source dependency documented
  # =======================================================================

  @integration @issue-820
  Scenario: A clean single-commit suite passes the revision check
    Given suite tarballs whose binaries were all built from one clean commit
    When scripts/check-suite-revisions.sh runs against them
    Then it prints "revision check PASSED" and exits 0

  @integration @issue-820
  Scenario: A revision-skewed or dirty binary fails the revision check
    Given a suite tarball where one binary carries a different revision or a +dirty stamp
    When scripts/check-suite-revisions.sh runs against it
    Then it exits non-zero and names the offending binary

  @integration @issue-820
  Scenario: The script header documents its module-source dependency
    Given scripts/check-suite-revisions.sh now reads its lists via `go run`
    When the script header is read
    Then it documents that the script requires the module source checked out and always runs from the repo checkout in CI

  # =======================================================================
  # AC9 — orchard-tui embeds a statically-greppable revision marker (Phase 2)
  # =======================================================================

  @integration @issue-820
  Scenario: A cross-built orchard-tui carries a greppable ORCHARD_REVISION marker
    Given orchard-tui cross-built for aarch64-apple-darwin from an ubuntu host, release-stripped
    When the binary is grepped with `grep -a -oE 'ORCHARD_REVISION=[0-9a-f]{7,40}(\+dirty)?'`
    Then the marker line is returned without executing the binary

  # =======================================================================
  # AC10 — Marker value equals runtime --revision, incl. +dirty (correctness)
  # =======================================================================

  @integration @issue-820
  Scenario: The embedded marker matches what orchard-tui --revision prints on the host
    Given the host-triple suite tarball
    When check-suite-revisions.sh compares the statically-grepped marker to `orchard-tui --revision` output
    Then the two strings are equal, including an identical +dirty suffix when the tree was dirty

  @integration @issue-820
  Scenario: A marker that diverges from the runtime revision fails the check
    Given an orchard-tui whose embedded marker differs from what --revision prints
    When check-suite-revisions.sh runs against the host tarball
    Then it exits non-zero

  # =======================================================================
  # AC11 — Cross-arch orchard-tui revision is verified, not skipped
  # =======================================================================

  @integration @issue-820
  Scenario: A foreign-arch orchard-tui revision is checked statically instead of skipped
    Given a foreign-triple suite tarball on an ubuntu host
    When scripts/check-suite-revisions.sh runs against it
    Then orchard-tui's revision is extracted statically and folded into the cross-tarball comparison
    And the script does not print "SKIP --revision orchard-tui"

  @integration @issue-820
  Scenario: A seeded foreign-arch orchard-tui skew now fails the check
    Given a foreign-triple suite tarball whose orchard-tui revision differs from the other tarballs
    When scripts/check-suite-revisions.sh runs against the set
    Then it exits non-zero and names orchard-tui

  # --- AC Coverage Map ---
  # AC1  Lister emits correct, ordered sets
  #   -> The lister prints each named set one binary per line in SuiteBinaries order
  # AC2  Unknown / missing set arg fails loudly
  #   -> An unknown set name is rejected with a non-zero exit
  #   -> A missing set arg is rejected with a non-zero exit
  # AC3  Go set derived from SuiteBinaries; excludes orchard-status; partitions cleanly
  #   -> GoBinaries and RustBinaries partition SuiteBinaries exactly
  #   -> The CLI go and rust sets partition the suite set with no overlap
  #   -> A new Go binary added to SuiteBinaries joins GoBinaries with no other edit
  #   -> orchard-status is never classified as a suite Go binary
  # AC4  Three rewired files read the lister; no literal list on an executable line
  #   -> No hand-maintained binary-list literal remains on an executable line of the rewired files
  # AC4b install.sh stays literal but is drift-checked against the Go source
  #   -> A Go mirror-test asserts install.sh's literal lists equal the Go source
  #   -> A drifted install.sh literal fails the mirror-test, naming install.sh
  # AC5  Drift guard fails on any reintroduced or divergent literal
  #   -> A literal list re-added to a rewired file in a different order fails the guard, naming the file
  #   -> A divergence seeded in install.sh fails the same guard, naming install.sh
  #   -> The guard passes on a clean tree
  # AC6  Lister calls are cross-compile-safe (failure mode)
  #   -> The lister runs on the host even when GOOS/GOARCH target a foreign arch
  # AC7  Assembled suite tarball still holds exactly the six binaries
  #   -> The assembled suite tarball holds exactly the six binaries, proven from tar tzf
  # AC8  check-suite-revisions.sh passes clean / fails on skew; module-source dependency documented
  #   -> A clean single-commit suite passes the revision check
  #   -> A revision-skewed or dirty binary fails the revision check
  #   -> The script header documents its module-source dependency
  # AC9  orchard-tui embeds a statically-greppable revision marker (Phase 2)
  #   -> A cross-built orchard-tui carries a greppable ORCHARD_REVISION marker
  # AC10 Marker value equals runtime --revision, incl. +dirty
  #   -> The embedded marker matches what orchard-tui --revision prints on the host
  #   -> A marker that diverges from the runtime revision fails the check
  # AC11 Cross-arch orchard-tui revision is verified, not skipped
  #   -> A foreign-arch orchard-tui revision is checked statically instead of skipped
  #   -> A seeded foreign-arch orchard-tui skew now fails the check
  #
  # AC count: 12 (AC1-AC11 + AC4b). Mapped scenarios: 25 (>= 1 per AC). No drops, no gaps.
