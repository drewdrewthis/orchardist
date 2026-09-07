package release

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strings"
	"testing"
)

// repoRoot resolves the module root from this test's own location
// (internal/release/binaries_test.go -> ../..), so the script-scanning tests
// do not depend on the working directory `go test` was invoked from.
func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Join(filepath.Dir(file), "..", "..")
}

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

// bashArrayRe captures the elements of `SUITE_BINARIES=(...)` in install.sh.
var bashArrayRe = regexp.MustCompile(`(?m)^SUITE_BINARIES=\(([^)]*)\)`)

// gobinLoopRe captures the binary list of `for gobin in ... ; do` in install.sh.
var gobinLoopRe = regexp.MustCompile(`for gobin in ([^;]*);`)

// TestInstallShMirrorsSuiteBinaries pins install.sh's two literal lists to the
// Go source. install.sh runs via `curl | bash` with no Go toolchain, so it
// cannot read the lister; this test is its drift guard instead. Any divergence
// fails naming install.sh (AC4b).
func TestInstallShMirrorsSuiteBinaries(t *testing.T) {
	path := filepath.Join(repoRoot(t), "scripts", "install.sh")
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read install.sh: %v", err)
	}
	text := string(src)

	arr := bashArrayRe.FindStringSubmatch(text)
	if arr == nil {
		t.Fatal("install.sh: could not find a SUITE_BINARIES=(...) array")
	}
	if got := strings.Fields(arr[1]); !slices.Equal(got, SuiteBinaries) {
		t.Errorf("install.sh SUITE_BINARIES = %v; want SuiteBinaries %v (value and order)", got, SuiteBinaries)
	}

	loop := gobinLoopRe.FindStringSubmatch(text)
	if loop == nil {
		t.Fatal("install.sh: could not find a `for gobin in ...;` loop")
	}
	if got := strings.Fields(loop[1]); !slices.Equal(got, GoBinaries) {
		t.Errorf("install.sh gobin loop = %v; want GoBinaries %v (value and order)", got, GoBinaries)
	}
}

// rewiredFiles are the copies that were switched to the suite-bins lister. No
// hand-maintained binary list may reappear on an executable line of any of
// them (install.sh is excluded — it is literal by design, see AC4b).
var rewiredFiles = []string{
	"scripts/check-suite-revisions.sh",
	"scripts/dist.sh",
	".github/workflows/release-please.yml",
}

// binaryNameRe matches a single suite-binary token at its longest spelling, so
// "orchard-tui" is one token and never also counts as a bare "orchard". Built
// longest-first because Go's regexp alternation is leftmost-first.
var binaryNameRe = func() *regexp.Regexp {
	names := slices.Clone(SuiteBinaries)
	sort.SliceStable(names, func(i, j int) bool { return len(names[i]) > len(names[j]) })
	for i, n := range names {
		names[i] = regexp.QuoteMeta(n)
	}
	return regexp.MustCompile(strings.Join(names, "|"))
}()

// distinctBinaryNames returns how many DISTINCT suite binaries a line names. A
// line repeating one name (e.g. `cp .../orchard-tui "$dir/orchard-tui"`) counts
// as one; a literal list (`for bin in a b c`) counts as several.
func distinctBinaryNames(line string) int {
	seen := map[string]bool{}
	for _, m := range binaryNameRe.FindAllString(line, -1) {
		seen[m] = true
	}
	return len(seen)
}

// TestNoLiteralBinaryLists is the drift guard: it fails, naming the file, when
// any rewired file carries a hand-maintained multi-binary literal on an
// executable (non-comment) line (AC5). It also runs the install.sh mirror
// check so one `go test ./internal/release` target covers both AC4b and AC5.
func TestNoLiteralBinaryLists(t *testing.T) {
	root := repoRoot(t)
	for _, rel := range rewiredFiles {
		src, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		base := filepath.Base(rel)
		for i, line := range strings.Split(string(src), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "#") {
				continue // comment line
			}
			if distinctBinaryNames(line) >= 2 {
				t.Errorf("%s:%d carries a literal binary list (must read via suite-bins): %s",
					base, i+1, strings.TrimSpace(line))
			}
		}
	}
}
