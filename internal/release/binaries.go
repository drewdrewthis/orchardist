package release

import (
	"maps"
	"slices"
)

// RustBinaries are the suite binaries built from crates/orchard by cargo, as
// opposed to the Go cmd/* binaries. The split carries no filesystem signal —
// a cmd/* scan would wrongly enroll cmd/orchard-status, which exists on disk
// but is deliberately absent from SuiteBinaries — so the Rust members are named
// explicitly and the Go set is derived as the complement. This mirrors
// revision.go's `RevisionBinaries = SuiteBinaries − UnstampedBinaries` pattern.
//
// UnstampedBinaries ⊂ RustBinaries, but the two are NOT the same set and must
// stay distinct: orchard-tui is Rust *and* stamped (answers --revision), while
// orchard is Rust *and* unstamped (the dispatcher carries no build stamp).
var RustBinaries = []string{"orchard-tui", "orchard"}

// GoBinaries are the suite binaries built with `go build` under cmd/*:
// SuiteBinaries minus RustBinaries, in SuiteBinaries order. The release scripts
// and the release workflow read this set through cmd/suite-bins so no
// hand-mirrored copy can drift from the curated SuiteBinaries slice.
var GoBinaries = goBinaries(SuiteBinaries, RustBinaries)

// goBinaries returns suite minus rust, preserving suite order. It is a thin
// wrapper over complement (revision.go) so TestBinarySets can keep calling it
// directly with an explicit rust slice (e.g. a fake-binary probe).
func goBinaries(suite, rust []string) []string {
	return complement(suite, rust)
}

// SetsByName maps each lister set name to its slice, so cmd/suite-bins and the
// tests read from one source. "revision" reuses RevisionBinaries (revision.go).
var SetsByName = map[string][]string{
	"suite":    SuiteBinaries,
	"go":       GoBinaries,
	"rust":     RustBinaries,
	"revision": RevisionBinaries,
}

// SetNames returns the valid lister set names in sorted order, for the CLI's
// usage/error text and its tests.
func SetNames() []string {
	return slices.Sorted(maps.Keys(SetsByName))
}
