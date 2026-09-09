package release

import (
	"slices"
	"testing"
)

// TestBinarySets locks the Go/Rust classification: the two sets partition
// SuiteBinaries exactly, GoBinaries keeps SuiteBinaries order, orchard-status
// never leaks in, and the derivation picks up a newly added Go binary with no
// other edit (the anti-drift guarantee).
func TestBinarySets(t *testing.T) {
	t.Run("partition SuiteBinaries exactly", func(t *testing.T) {
		union := append(slices.Clone(GoBinaries), RustBinaries...)
		got := slices.Clone(union)
		slices.Sort(got)
		want := slices.Clone(SuiteBinaries)
		slices.Sort(want)
		if !slices.Equal(got, want) {
			t.Errorf("GoBinaries ∪ RustBinaries = %v; want SuiteBinaries %v", union, SuiteBinaries)
		}
		for _, name := range GoBinaries {
			if slices.Contains(RustBinaries, name) {
				t.Errorf("%q is in both GoBinaries and RustBinaries", name)
			}
		}
		if len(GoBinaries) != 4 || len(RustBinaries) != 2 {
			t.Errorf("got %d Go + %d Rust binaries; want 4 + 2 = 6", len(GoBinaries), len(RustBinaries))
		}
	})

	t.Run("GoBinaries preserves SuiteBinaries order", func(t *testing.T) {
		want := goBinaries(SuiteBinaries, RustBinaries) // recomputed, same order
		if !slices.Equal(GoBinaries, want) {
			t.Errorf("GoBinaries = %v; want %v (SuiteBinaries order)", GoBinaries, want)
		}
		// Members must appear in the same relative order as in SuiteBinaries.
		last := -1
		for _, name := range GoBinaries {
			idx := slices.Index(SuiteBinaries, name)
			if idx <= last {
				t.Errorf("GoBinaries member %q at SuiteBinaries index %d breaks order", name, idx)
			}
			last = idx
		}
	})

	t.Run("orchard-status is never a suite Go binary", func(t *testing.T) {
		if slices.Contains(GoBinaries, "orchard-status") {
			t.Error("orchard-status leaked into GoBinaries; it exists on disk but is not in SuiteBinaries")
		}
	})

	t.Run("a new Go binary appended to SuiteBinaries joins the derived Go set", func(t *testing.T) {
		fake := "orchard-fake"
		suite := append(slices.Clone(SuiteBinaries), fake)
		got := goBinaries(suite, RustBinaries)
		if !slices.Contains(got, fake) {
			t.Errorf("%q not in derived Go set %v", fake, got)
		}
		if slices.Contains(RustBinaries, fake) {
			t.Errorf("%q wrongly in RustBinaries", fake)
		}
	})
}
