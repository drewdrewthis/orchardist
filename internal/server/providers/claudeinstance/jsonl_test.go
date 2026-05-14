package claudeinstance

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestEncodeCwd_SwapsSlashesAndDots locks in claude's project-directory
// naming transform: every '/' AND every '.' becomes '-'. Verified
// empirically against ~/.claude/projects directory names — e.g. a path
// containing `/.claude/` lands at `--claude-` (two dashes from the `/.`).
// Pure string replacement, no symlink resolution.
func TestEncodeCwd_SwapsSlashesAndDots(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"/home/user/workspace/git-orchard-rs", "-home-user-workspace-git-orchard-rs"},
		{"/", "-"},
		{"", ""},
		{"/foo", "-foo"},
		{"/home/user/.claude/projects", "-home-user--claude-projects"},
		{"/repo/.worktrees/issue603", "-repo--worktrees-issue603"},
	}
	for _, c := range cases {
		if got := encodeCwd(c.in); got != c.want {
			t.Errorf("encodeCwd(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestFsJsonlReader_LastActivityAt_ReturnsTimestampFromLastLine verifies
// the happy path: a transcript jsonl with several lines, each carrying a
// `timestamp` field, returns the last line's timestamp.
func TestFsJsonlReader_LastActivityAt_ReturnsTimestampFromLastLine(t *testing.T) {
	const cwd = "/home/user/workspace/foo"
	const sessionID = "11111111-2222-3333-4444-555555555555"
	const lastTS = "2026-05-09T22:33:44.567Z"

	dir := makeJsonlFixture(t, cwd, sessionID,
		`{"type":"user","timestamp":"2026-05-09T22:33:40Z"}`,
		`{"type":"assistant","timestamp":"2026-05-09T22:33:42Z"}`,
		`{"type":"assistant","timestamp":"`+lastTS+`"}`,
	)

	r := NewFsJsonlReader(dir)
	got, ok := r.LastActivityAt(context.Background(), cwd, sessionID)
	if !ok {
		t.Fatal("LastActivityAt returned ok=false; want true")
	}
	want, _ := time.Parse(time.RFC3339Nano, lastTS)
	if !got.Equal(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// TestFsJsonlReader_LastActivityAt_HandlesTrailingNewline verifies the
// reader does not mistake a trailing '\n' for an empty last line.
func TestFsJsonlReader_LastActivityAt_HandlesTrailingNewline(t *testing.T) {
	const cwd = "/home/user/workspace/foo"
	const sessionID = "trailing-newline"
	const lastTS = "2026-05-09T22:33:44Z"

	dir := t.TempDir()
	path := filepath.Join(dir, encodeCwd(cwd))
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	body := `{"type":"user","timestamp":"2026-05-09T22:33:40Z"}` + "\n" +
		`{"type":"assistant","timestamp":"` + lastTS + `"}` + "\n"
	if err := os.WriteFile(filepath.Join(path, sessionID+".jsonl"), []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	r := NewFsJsonlReader(dir)
	got, ok := r.LastActivityAt(context.Background(), cwd, sessionID)
	if !ok {
		t.Fatal("LastActivityAt returned ok=false; want true")
	}
	want, _ := time.Parse(time.RFC3339, lastTS)
	if !got.Equal(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// TestFsJsonlReader_LastActivityAt_TailWindowSurvivesGiantTranscript
// verifies the bounded-tail read works when the transcript is larger
// than tailWindow. We synthesise a >tailWindow file whose last line
// carries the answer; the reader must seek-to-end and find it.
func TestFsJsonlReader_LastActivityAt_TailWindowSurvivesGiantTranscript(t *testing.T) {
	const cwd = "/home/user/workspace/foo"
	const sessionID = "giant"
	const lastTS = "2026-05-09T22:33:44Z"

	dir := t.TempDir()
	path := filepath.Join(dir, encodeCwd(cwd))
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	// Build a transcript whose first line is a 100 KiB blob (well
	// past tailWindow), followed by the line we actually care about.
	// readLastTimestamp must not be deceived by the giant prefix — it
	// only reads the last tailWindow bytes.
	bigPrefix := strings.Repeat("x", 100*1024)
	body := `{"type":"junk","blob":"` + bigPrefix + `"}` + "\n" +
		`{"type":"assistant","timestamp":"` + lastTS + `"}` + "\n"
	if err := os.WriteFile(filepath.Join(path, sessionID+".jsonl"), []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	r := NewFsJsonlReader(dir)
	got, ok := r.LastActivityAt(context.Background(), cwd, sessionID)
	if !ok {
		t.Fatal("LastActivityAt returned ok=false; want true")
	}
	want, _ := time.Parse(time.RFC3339, lastTS)
	if !got.Equal(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// TestFsJsonlReader_LastActivityAt_MissingFileReturnsFalse covers the
// silent-failure contract: a missing transcript is not an error, the
// reader simply returns (zero, false) so the composer falls through.
func TestFsJsonlReader_LastActivityAt_MissingFileReturnsFalse(t *testing.T) {
	dir := t.TempDir()
	r := NewFsJsonlReader(dir)
	if _, ok := r.LastActivityAt(context.Background(), "/no/such/cwd", "no-such-uuid"); ok {
		t.Error("LastActivityAt on missing file returned ok=true; want false")
	}
}

// TestFsJsonlReader_LastActivityAt_EmptyFileReturnsFalse covers the
// edge where the transcript exists but is empty — same contract.
func TestFsJsonlReader_LastActivityAt_EmptyFileReturnsFalse(t *testing.T) {
	const cwd = "/home/user/workspace/foo"
	const sessionID = "empty"
	dir := makeJsonlFixture(t, cwd, sessionID)

	r := NewFsJsonlReader(dir)
	if _, ok := r.LastActivityAt(context.Background(), cwd, sessionID); ok {
		t.Error("LastActivityAt on empty file returned ok=true; want false")
	}
}

// TestFsJsonlReader_LastActivityAt_LastLineWithoutTimestampReturnsFalse
// covers the case where the last line is well-formed JSON but carries
// no `timestamp` field (early summary lines do this). The reader must
// not invent a value.
func TestFsJsonlReader_LastActivityAt_LastLineWithoutTimestampReturnsFalse(t *testing.T) {
	const cwd = "/home/user/workspace/foo"
	const sessionID = "no-ts"
	dir := makeJsonlFixture(t, cwd, sessionID,
		`{"type":"user","timestamp":"2026-05-09T22:33:40Z"}`,
		`{"type":"summary","summary":"some recap text"}`,
	)

	r := NewFsJsonlReader(dir)
	if _, ok := r.LastActivityAt(context.Background(), cwd, sessionID); ok {
		t.Error("LastActivityAt with no timestamp on last line returned ok=true; want false")
	}
}

// TestFsJsonlReader_LastActivityAt_NilOrEmptyArgsReturnsFalse pins the
// nil-safe contract used by composer's "if c.jsonl != nil && hb.Cwd !=
// '' && hb.SessionID != ''" guard. The reader still defends against
// empty inputs in case a caller forgets.
func TestFsJsonlReader_LastActivityAt_NilOrEmptyArgsReturnsFalse(t *testing.T) {
	r := NewFsJsonlReader(t.TempDir())
	if _, ok := r.LastActivityAt(context.Background(), "", "uuid"); ok {
		t.Error("empty cwd returned ok=true; want false")
	}
	if _, ok := r.LastActivityAt(context.Background(), "/cwd", ""); ok {
		t.Error("empty sessionID returned ok=true; want false")
	}
	var nilReader *FsJsonlReader
	if _, ok := nilReader.LastActivityAt(context.Background(), "/cwd", "uuid"); ok {
		t.Error("nil receiver returned ok=true; want false")
	}
}

// TestNewFsJsonlReader_DefaultsToHomeProjects verifies the constructor
// defaults to ~/.claude/projects when projectsDir is empty. We do not
// assert the file system state — only that the resolved path matches
// the home-directory shape so consumers see the expected behaviour.
func TestNewFsJsonlReader_DefaultsToHomeProjects(t *testing.T) {
	r := NewFsJsonlReader("")
	if r == nil {
		// On a build agent without HOME this can legitimately be nil —
		// composer guards against that. Skip rather than fail.
		t.Skip("UserHomeDir unresolvable in this environment")
	}
	if !strings.HasSuffix(r.projectsDir, filepath.Join(".claude", "projects")) {
		t.Errorf("projectsDir = %q, want suffix .claude/projects", r.projectsDir)
	}
}

// makeJsonlFixture builds a test directory laid out the way claude's
// project layout looks: <dir>/<encoded-cwd>/<sessionID>.jsonl. Lines
// are joined with '\n' and a trailing '\n' is appended.
func makeJsonlFixture(t *testing.T, cwd, sessionID string, lines ...string) string {
	t.Helper()
	dir := t.TempDir()
	projectDir := filepath.Join(dir, encodeCwd(cwd))
	if err := os.MkdirAll(projectDir, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	body := strings.Join(lines, "\n")
	if body != "" {
		body += "\n"
	}
	if err := os.WriteFile(filepath.Join(projectDir, sessionID+".jsonl"), []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return dir
}
