package chat

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeJSONL appends `lines` to <dir>/<room>.jsonl and returns the
// path. Each line gets a trailing newline.
func writeJSONL(t *testing.T, dir, room string, lines ...string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %q: %v", dir, err)
	}
	path := filepath.Join(dir, room+".jsonl")
	body := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %q: %v", path, err)
	}
	return path
}

func TestAdapter_SnapshotEmptyDir(t *testing.T) {
	dir := t.TempDir()
	a := NewAdapter(dir)
	rooms, offsets, err := a.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if len(rooms) != 0 {
		t.Errorf("rooms: got %d want 0", len(rooms))
	}
	if len(offsets) != 0 {
		t.Errorf("offsets: got %d want 0", len(offsets))
	}
}

func TestAdapter_SnapshotMissingDir(t *testing.T) {
	a := NewAdapter(filepath.Join(t.TempDir(), "missing"))
	rooms, _, err := a.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("missing dir should not error: %v", err)
	}
	if len(rooms) != 0 {
		t.Errorf("rooms: got %d want 0", len(rooms))
	}
}

func TestAdapter_SnapshotReadsRoomsAndOffsets(t *testing.T) {
	dir := t.TempDir()
	writeJSONL(t, dir, "general",
		`{"type":"member.joined","ts":"2026-05-09T17:00:00Z","handle":"@alice","machine":"m","tmux_session":"s"}`,
		`{"type":"message","ts":"2026-05-09T17:01:00Z","id":"01J1","sender":"@alice","text":"hi"}`,
	)
	writeJSONL(t, dir, "@bob",
		`{"type":"message","ts":"2026-05-09T17:02:00Z","id":"01J2","sender":"@alice","text":"yo"}`,
	)

	a := NewAdapter(dir)
	rooms, offsets, err := a.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if got, want := len(rooms), 2; got != want {
		t.Fatalf("rooms: got %d want %d", got, want)
	}
	if got, want := len(rooms["general"]), 2; got != want {
		t.Errorf("general events: got %d want %d", got, want)
	}
	if got, want := len(rooms["@bob"]), 1; got != want {
		t.Errorf("@bob events: got %d want %d", got, want)
	}
	if offsets["general.jsonl"] == 0 {
		t.Errorf("general offset should be > 0")
	}
	if offsets["@bob.jsonl"] == 0 {
		t.Errorf("@bob offset should be > 0")
	}
}

func TestAdapter_FollowFromOffsetsTailsAppends(t *testing.T) {
	dir := t.TempDir()
	writeJSONL(t, dir, "r",
		`{"type":"message","ts":"2026-05-09T17:00:00Z","id":"01J1","sender":"@a","text":"first"}`,
	)
	a := NewAdapter(dir)
	_, offsets, err := a.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}

	// Append a second message.
	path := filepath.Join(dir, "r.jsonl")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := f.WriteString(`{"type":"message","ts":"2026-05-09T17:01:00Z","id":"01J2","sender":"@a","text":"second"}` + "\n"); err != nil {
		t.Fatalf("write: %v", err)
	}
	f.Close()

	tail, _, err := a.FollowFromOffsets(context.Background(), offsets)
	if err != nil {
		t.Fatalf("follow: %v", err)
	}
	if got, want := len(tail["r"]), 1; got != want {
		t.Fatalf("tail events: got %d want %d", got, want)
	}
	if tail["r"][0].ID != "01J2" {
		t.Errorf("tail event id: got %q want 01J2", tail["r"][0].ID)
	}
}

func TestAdapter_PartialFinalLineNotConsumed(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "r.jsonl")
	if err := os.WriteFile(path,
		[]byte(`{"type":"message","ts":"2026-05-09T17:00:00Z","id":"01J1","sender":"@a","text":"complete"}`+"\n"+
			`{"type":"message","ts":"2026-05-09T17:01:00Z","id":"01J2","sender":"@a","text":"incomp`),
		0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	a := NewAdapter(dir)
	rooms, offsets, err := a.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if got, want := len(rooms["r"]), 1; got != want {
		t.Fatalf("only the complete line should be folded: got %d want %d", got, want)
	}
	// Offset should be the position past the complete line, NOT past
	// the partial trailing line.
	st, _ := os.Stat(path)
	if offsets["r.jsonl"] >= st.Size() {
		t.Errorf("offset %d should be less than file size %d (partial line uncommitted)",
			offsets["r.jsonl"], st.Size())
	}
}
