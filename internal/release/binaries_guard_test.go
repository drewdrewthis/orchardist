package release

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// repoRoot resolves the module root from this test's own location
// (internal/release/binaries_guard_test.go -> ../..), so the script-scanning
// tests do not depend on the working directory `go test` was invoked from.
func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Join(filepath.Dir(file), "..", "..")
}

// inlineCommentRe matches a trailing ` #...`/`\t#...` inline comment: the `#`
// must be preceded by whitespace, so a parameter-expansion default like
// `${x#y}` (no preceding whitespace) is never mistaken for a comment.
var inlineCommentRe = regexp.MustCompile(`[ \t]#.*$`)

// stripComments blanks whole-comment lines and trims trailing inline
// comments, so a binary name mentioned only in prose/comments never counts
// as a mention by any regex run against the result.
func stripComments(text string) string {
	lines := strings.Split(text, "\n")
	for i, l := range lines {
		if strings.HasPrefix(strings.TrimSpace(l), "#") {
			lines[i] = ""
			continue
		}
		if loc := inlineCommentRe.FindStringIndex(l); loc != nil {
			lines[i] = l[:loc[0]]
		}
	}
	return strings.Join(lines, "\n")
}

// bashArrayRe captures the elements of `SUITE_BINARIES=(...)` in install.sh.
var bashArrayRe = regexp.MustCompile(`(?m)^SUITE_BINARIES=\(([^)]*)\)`)

// gobinLoopRe captures the binary list of `for gobin in ... ; do` in
// install.sh. Anchored like bashArrayRe (line-start, allowing indentation)
// and run against comment-stripped text. Both regexes assume their target
// construct is written on a single line -- a multi-line rewrite of either
// would silently escape this drift guard.
var gobinLoopRe = regexp.MustCompile(`(?m)^\s*for gobin in ([^;]*);`)

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
	text := stripComments(string(src))

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
// them (install.sh is excluded — it is literal by design, see AC4b). Each
// must actually read the lister, not just avoid a literal list.
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
	slices.SortStableFunc(names, func(a, b string) int { return len(b) - len(a) })
	for i, n := range names {
		names[i] = regexp.QuoteMeta(n)
	}
	return regexp.MustCompile(strings.Join(names, "|"))
}()

// distinctBinaryNames returns how many DISTINCT suite binaries appear (as
// bare-word regex matches) in text. A line/block repeating one name (e.g.
// `cp .../orchard-tui "$dir/orchard-tui"`) counts as one; a literal list
// (`for bin in a b c`) counts as several.
func distinctBinaryNames(text string) int {
	seen := map[string]bool{}
	for _, m := range binaryNameRe.FindAllString(text, -1) {
		seen[m] = true
	}
	return len(seen)
}

// listItemRe matches a line that consists SOLELY of one suite-binary name:
// optional YAML `- ` marker, optional quotes, optional trailing `,` or `\`.
// This is what distinguishes a hand-maintained list from an executable line
// that merely mentions a binary (a path, a `cp` argument, a `-p orchard`
// cargo flag), which is never a hit.
var listItemRe = regexp.MustCompile(`^\s*-?\s*["']?(` + binaryNameRe.String() + `)["']?\s*[,\\]?\s*$`)

// literalListHits scans text for hand-maintained multi-binary literals under
// two rules, after stripComments:
//
//   - single-line: a line naming >=2 distinct suite binaries (`for bin in a b`)
//   - multi-line: >=2 CONSECUTIVE list-item lines naming >=2 distinct binaries
//     (a bash array or YAML list spread over several lines)
//
// Each hit is returned as "line N: <snippet>" for exact reporting.
func literalListHits(text string) []string {
	lines := strings.Split(stripComments(text), "\n")

	var hits []string
	runStart := -1
	var runLines, runNames []string
	flush := func() {
		if len(runLines) > 1 && distinctBinaryNames(strings.Join(runNames, " ")) >= 2 {
			hits = append(hits, fmt.Sprintf("line %d: %s", runStart, strings.TrimSpace(strings.Join(runLines, "\n"))))
		}
		runLines, runNames = nil, nil
		runStart = -1
	}
	for i, l := range lines {
		if distinctBinaryNames(l) >= 2 {
			hits = append(hits, fmt.Sprintf("line %d: %s", i+1, strings.TrimSpace(l)))
		}
		m := listItemRe.FindStringSubmatch(l)
		if m == nil {
			flush()
			continue
		}
		if runStart == -1 {
			runStart = i + 1
		}
		runLines = append(runLines, l)
		runNames = append(runNames, m[1])
	}
	flush()
	return hits
}

// TestLiteralListHits pins literalListHits' two rules directly, ahead
// of TestNoLiteralBinaryLists running it against the real files.
func TestLiteralListHits(t *testing.T) {
	tests := []struct {
		name    string
		text    string
		wantHit bool
	}{
		{
			name:    "single-line list",
			text:    "for bin in orchard-daemon orchard-sidebar; do",
			wantHit: true,
		},
		{
			name:    "multi-line bash array",
			text:    "GO_BINS=(\n  orchard-daemon\n  orchard-sidebar\n)",
			wantHit: true,
		},
		{
			name:    "multi-line YAML list",
			text:    "- orchard-daemon\n- orchard-sidebar",
			wantHit: true,
		},
		{
			name:    "same name repeated in one command is not a list",
			text:    `cp a/orchard-tui b/orchard-tui`,
			wantHit: false,
		},
		{
			name:    "two blocks separated by a blank line, one name each",
			text:    "cp a/orchard-daemon b/orchard-daemon\n\ncp a/orchard-sidebar b/orchard-sidebar",
			wantHit: false,
		},
		{
			name:    "trailing comment naming a second binary does not count",
			text:    "cp a/orchard-daemon b/orchard-daemon # orchard-daemon orchard-sidebar",
			wantHit: false,
		},
		{
			name:    "consecutive cp lines naming two binaries are not list items",
			text:    "cp a/orchard-daemon b/orchard-daemon\ncp a/orchard-sidebar b/orchard-sidebar",
			wantHit: false,
		},
		{
			name:    "consecutive list items repeating one name is not a list",
			text:    "- orchard-daemon\n- orchard-daemon",
			wantHit: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := literalListHits(tt.text)
			if hit := len(got) > 0; hit != tt.wantHit {
				t.Errorf("literalListHits(%q) = %v; want a hit: %v", tt.text, got, tt.wantHit)
			}
		})
	}
}

// TestNoLiteralBinaryLists is the drift guard: it fails, naming the file, when
// any rewired file carries a hand-maintained multi-binary literal (AC5), or
// no longer actually reads the suite-bins lister.
func TestNoLiteralBinaryLists(t *testing.T) {
	root := repoRoot(t)
	for _, rel := range rewiredFiles {
		src, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		base := filepath.Base(rel)
		text := string(src)

		if !strings.Contains(text, "suite-bins") {
			t.Errorf("%s: no reference to the suite-bins lister found; must read the binary roster via suite-bins", base)
		}

		for _, hit := range literalListHits(text) {
			t.Errorf("%s: carries a literal binary list (must read via suite-bins): %s", base, hit)
		}
	}
}
