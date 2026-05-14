package claudeinstance

import (
	"context"
	"testing"
	"time"

	"github.com/drewdrewthis/git-orchard-rs/internal/server/graphql"
)

// fakeJsonlReader is the test stub for JsonlReader. Configure either
// `at` (returned for any matching cwd+sessionID) or `byKey` (per-key
// override). Returns (zero, false) when neither matches, mirroring the
// production reader's contract on miss.
type fakeJsonlReader struct {
	at    time.Time
	ok    bool
	byKey map[string]time.Time
}

func (f *fakeJsonlReader) LastActivityAt(_ context.Context, cwd, sessionID string) (time.Time, bool) {
	if f.byKey != nil {
		if t, ok := f.byKey[cwd+"|"+sessionID]; ok {
			return t, true
		}
		return time.Time{}, false
	}
	return f.at, f.ok
}

// TestComposer_LastActivityAt_PrefersJsonlOverHeartbeat pins the
// priority order set in composeOne: when the jsonl reader has a value,
// it wins. The jsonl tail is appended to on every assistant/user/system
// step, so it is the most precise source for lastActivityAt.
func TestComposer_LastActivityAt_PrefersJsonlOverHeartbeat(t *testing.T) {
	now := time.Date(2026, 5, 9, 22, 0, 0, 0, time.UTC)
	jsonlActivity := now.Add(-15 * time.Second) // jsonl tail timestamp

	hb := Heartbeat{
		TmuxSession:     "alpha",
		SessionID:       "uuid-alpha",
		ClaudePid:       42100,
		Cwd:             "/home/user/workspace/foo",
		Timestamp:       now.Add(-2 * time.Second),
		LastHeartbeatAt: now.Add(-2 * time.Second),
	}

	c := NewComposerWith(
		"local",
		nil,
		nil,
		nil,
		fakeLiveness{alive: map[int]bool{42100: true}},
		&fakeJsonlReader{at: jsonlActivity, ok: true},
		func() time.Time { return now },
		HeartbeatStaleAfter,
	)

	out := c.Compose(context.Background(), []Heartbeat{hb})
	if len(out) != 1 {
		t.Fatalf("got %d, want 1", len(out))
	}
	if out[0].LastActivityAt == nil {
		t.Fatal("LastActivityAt is nil; want jsonl timestamp")
	}
	got, err := time.Parse(time.RFC3339Nano, *out[0].LastActivityAt)
	if err != nil {
		t.Fatalf("parse %q: %v", *out[0].LastActivityAt, err)
	}
	if !got.Equal(jsonlActivity) {
		t.Errorf("LastActivityAt = %v, want jsonlActivity %v", got, jsonlActivity)
	}
}

// TestComposer_LastActivityAt_SkipsJsonlWhenCwdMissing pins the
// composer's "if hb.Cwd != ''" guard. When the heartbeat predates cwd
// recording (legacy hook), the jsonl reader is bypassed entirely — its
// LastActivityAt method is never called. Phase 3 removed the heartbeat
// last_activity fallback, so with no cwd AND no pane session timestamp,
// LastActivityAt remains nil.
func TestComposer_LastActivityAt_SkipsJsonlWhenCwdMissing(t *testing.T) {
	now := time.Date(2026, 5, 9, 22, 0, 0, 0, time.UTC)

	hb := Heartbeat{
		TmuxSession:     "alpha",
		SessionID:       "uuid-alpha",
		ClaudePid:       42100,
		// Cwd intentionally absent
		Timestamp:       now.Add(-2 * time.Second),
		LastHeartbeatAt: now.Add(-2 * time.Second),
	}

	wrongTime := now.Add(1 * time.Hour) // would be obviously wrong if surfaced
	c := NewComposerWith(
		"local",
		nil,
		nil,
		nil,
		fakeLiveness{alive: map[int]bool{42100: true}},
		&fakeJsonlReader{at: wrongTime, ok: true},
		func() time.Time { return now },
		HeartbeatStaleAfter,
	)

	out := c.Compose(context.Background(), []Heartbeat{hb})
	if out[0].LastActivityAt != nil {
		t.Errorf("LastActivityAt = %v, want nil (no cwd, no pane fallback)", *out[0].LastActivityAt)
	}
}

// TestComposer_LastActivityAt_FallsBackToPaneSession verifies the
// second tier: with no jsonl hit, the pane's session-level
// lastActivityAt is the fallback. Coarse but better than null for the
// GUI. (Phase 3 removed the heartbeat last_activity tier entirely.)
func TestComposer_LastActivityAt_FallsBackToPaneSession(t *testing.T) {
	now := time.Date(2026, 5, 9, 22, 0, 0, 0, time.UTC)
	hb := Heartbeat{
		TmuxSession:     "alpha",
		SessionID:       "uuid-alpha",
		ClaudePid:       42100,
		Cwd:             "/home/user/workspace/foo",
		Timestamp:       now.Add(-2 * time.Second),
		LastHeartbeatAt: now.Add(-2 * time.Second),
	}

	const sessionTS = "2026-05-09T21:30:00Z"
	v := sessionTS
	pane := &graphql.TmuxPane{
		ID: "TmuxPane:local:%26",
		Window: &graphql.TmuxWindow{
			Session: &graphql.TmuxSession{LastActivityAt: &v},
		},
	}

	c := NewComposerWith(
		"local",
		&fakePaneFinder{byPid: map[int]*graphql.TmuxPane{42100: pane}},
		nil,
		nil,
		fakeLiveness{alive: map[int]bool{42100: true}},
		&fakeJsonlReader{ok: false},
		func() time.Time { return now },
		HeartbeatStaleAfter,
	)

	out := c.Compose(context.Background(), []Heartbeat{hb})
	if out[0].LastActivityAt == nil || *out[0].LastActivityAt != sessionTS {
		t.Errorf("LastActivityAt = %v, want pane session %q", out[0].LastActivityAt, sessionTS)
	}
}
