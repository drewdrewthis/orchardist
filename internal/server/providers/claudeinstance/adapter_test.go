package claudeinstance

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestFileReader_ReadAll_Mixed asserts the reader globs only well-formed
// non-inflight heartbeat files and parses both legacy snake_case and
// ADR-shape camelCase fields.
func TestFileReader_ReadAll_Mixed(t *testing.T) {
	dir := t.TempDir()

	// Legacy snake_case shape (today's hook script writes this).
	legacy := map[string]any{
		"tmux_session": "alpha",
		"session_id":   "uuid-alpha",
		"state":        "working",
		"timestamp":    "2026-04-12T10:00:00Z",
		"cwd":          "/workspace",
		"event":        "PreToolUse",
	}
	writeJSON(t, filepath.Join(dir, "orchard-claude-alpha.json"), legacy)

	// ADR-011 shape with camelCase fields the future hook will write.
	adr := map[string]any{
		"tmux_session":    "bravo",
		"session_id":      "uuid-bravo",
		"state":           "idle",
		"timestamp":       "2026-04-12T10:00:30Z",
		"claudePid":       42100,
		"rcUrl":           "https://claude.ai/code/session_xyz",
		"rcEnabled":       true,
		"lastHeartbeatAt": "2026-04-12T10:00:35Z",
	}
	writeJSON(t, filepath.Join(dir, "orchard-claude-bravo.json"), adr)

	// Inflight staging file — must be skipped (mid-write atomic rename).
	writeJSON(t, filepath.Join(dir, "orchard-claude-charlie.inflight.json"), map[string]any{
		"tmux_session": "charlie",
		"state":        "working",
	})

	// Malformed JSON — must be skipped silently.
	writeRaw(t, filepath.Join(dir, "orchard-claude-malformed.json"), "not json")

	// Unrelated file — must not match.
	writeRaw(t, filepath.Join(dir, "totally-unrelated.json"), `{"foo":1}`)

	r := NewFileReader(dir)
	got, err := r.ReadAll(context.Background())
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d heartbeats, want 2 (alpha + bravo, skipping inflight + malformed): %+v", len(got), got)
	}
	// Sorted by tmux session — alpha before bravo.
	if got[0].TmuxSession != "alpha" || got[1].TmuxSession != "bravo" {
		t.Errorf("sort order = %s,%s; want alpha,bravo", got[0].TmuxSession, got[1].TmuxSession)
	}

	// Bravo decoded the ADR fields.
	if got[1].ClaudePid != 42100 {
		t.Errorf("bravo claudePid = %d, want 42100", got[1].ClaudePid)
	}
	if !got[1].RcEnabled {
		t.Error("bravo rcEnabled = false, want true")
	}
	if got[1].RcURL == "" {
		t.Error("bravo rcURL empty, want non-empty")
	}
	if got[1].LastHeartbeatAt.IsZero() {
		t.Error("bravo LastHeartbeatAt is zero, want parsed timestamp")
	}

	// Alpha falls back from missing LastHeartbeatAt to Timestamp.
	if got[0].LastHeartbeatAt != got[0].Timestamp {
		t.Errorf("alpha LastHeartbeatAt %v != Timestamp %v (fallback should match)",
			got[0].LastHeartbeatAt, got[0].Timestamp)
	}
}

// TestFileReader_ReadAll_EmptyDir asserts a missing/empty heartbeat
// directory returns an empty slice without erroring — the daemon must
// boot cleanly when no Claude session has ever started.
func TestFileReader_ReadAll_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	r := NewFileReader(dir)
	got, err := r.ReadAll(context.Background())
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %d, want 0 in empty dir", len(got))
	}
}

// TestResolveDir_EnvPrecedence asserts ORCHARD_HEARTBEAT_DIR wins over
// TMPDIR which wins over /tmp. The ladder mirrors the orchard hook
// script so the daemon and the writer agree on the directory without
// a coordination protocol.
func TestResolveDir_EnvPrecedence(t *testing.T) {
	t.Setenv("ORCHARD_HEARTBEAT_DIR", "/explicit")
	t.Setenv("TMPDIR", "/tmp-fallback")
	if got := ResolveDir(); got != "/explicit" {
		t.Errorf("explicit override = %q, want /explicit", got)
	}

	t.Setenv("ORCHARD_HEARTBEAT_DIR", "")
	if got := ResolveDir(); got != "/tmp-fallback" {
		t.Errorf("TMPDIR fallback = %q, want /tmp-fallback", got)
	}

	t.Setenv("TMPDIR", "")
	if got := ResolveDir(); got != "/tmp" {
		t.Errorf("/tmp ultimate fallback = %q, want /tmp", got)
	}
}

// writeJSON is a test helper that marshals payload to path using
// os.WriteFile. Failures are fatal so a setup bug never masquerades as
// an assertion failure.
func writeJSON(t *testing.T, path string, payload map[string]any) {
	t.Helper()
	b, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal %v: %v", payload, err)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// writeRaw writes raw bytes; used for malformed-input fixtures.
func writeRaw(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

