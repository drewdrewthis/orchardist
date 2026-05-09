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
// priority order set in composeOne: when both the jsonl reader and the
// heartbeat have a value, the jsonl wins. The hook's last_activity
// becomes a fallback layer behind the jsonl.
//
// This is the load-bearing assertion for the lastActiveAt field — the
// jsonl tail is appended to on every assistant/user/system step, so it
// is more recent and more precise than the hook's lifecycle-only
// last_activity.
func TestComposer_LastActivityAt_PrefersJsonlOverHeartbeat(t *testing.T) {
	now := time.Date(2026, 5, 9, 22, 0, 0, 0, time.UTC)
	heartbeatActivity := now.Add(-5 * time.Minute) // hook last_activity
	jsonlActivity := now.Add(-15 * time.Second)    // jsonl tail timestamp

	hb := Heartbeat{
		TmuxSession:     "alpha",
		SessionID:       "uuid-alpha",
		State:           "working",
		ClaudePid:       42100,
		Cwd:             "/home/user/workspace/foo",
		Timestamp:       now.Add(-2 * time.Second),
		LastHeartbeatAt: now.Add(-2 * time.Second),
		LastActivity:    heartbeatActivity,
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
		t.Errorf("LastActivityAt = %v, want jsonlActivity %v (heartbeat was %v)",
			got, jsonlActivity, heartbeatActivity)
	}
}

// TestComposer_LastActivityAt_FallsBackToHeartbeatWhenJsonlMisses
// covers the second tier of the priority chain: jsonl reader returned
// (zero, false) (file missing, no timestamp on last line, etc.); the
// composer must fall back to the hook's last_activity rather than
// jumping all the way to the pane fallback.
func TestComposer_LastActivityAt_FallsBackToHeartbeatWhenJsonlMisses(t *testing.T) {
	now := time.Date(2026, 5, 9, 22, 0, 0, 0, time.UTC)
	heartbeatActivity := now.Add(-30 * time.Second)

	hb := Heartbeat{
		TmuxSession:     "alpha",
		SessionID:       "uuid-alpha",
		State:           "working",
		ClaudePid:       42100,
		Cwd:             "/home/user/workspace/foo",
		Timestamp:       now.Add(-2 * time.Second),
		LastHeartbeatAt: now.Add(-2 * time.Second),
		LastActivity:    heartbeatActivity,
	}

	c := NewComposerWith(
		"local",
		nil,
		nil,
		nil,
		fakeLiveness{alive: map[int]bool{42100: true}},
		&fakeJsonlReader{ok: false}, // jsonl miss
		func() time.Time { return now },
		HeartbeatStaleAfter,
	)

	out := c.Compose(context.Background(), []Heartbeat{hb})
	if out[0].LastActivityAt == nil {
		t.Fatal("LastActivityAt is nil; want heartbeat fallback")
	}
	got, _ := time.Parse(time.RFC3339Nano, *out[0].LastActivityAt)
	if !got.Equal(heartbeatActivity) {
		t.Errorf("LastActivityAt = %v, want heartbeatActivity %v", got, heartbeatActivity)
	}
}

// TestComposer_LastActivityAt_SkipsJsonlWhenCwdMissing pins the
// composer's "if hb.Cwd != ''" guard. When the heartbeat predates cwd
// recording (legacy hook), the jsonl reader is bypassed entirely — its
// LastActivityAt method is never called. We verify this by configuring
// the fake to return a value that would be wrong if used.
func TestComposer_LastActivityAt_SkipsJsonlWhenCwdMissing(t *testing.T) {
	now := time.Date(2026, 5, 9, 22, 0, 0, 0, time.UTC)
	heartbeatActivity := now.Add(-30 * time.Second)

	hb := Heartbeat{
		TmuxSession:     "alpha",
		SessionID:       "uuid-alpha",
		State:           "working",
		ClaudePid:       42100,
		// Cwd intentionally absent
		Timestamp:       now.Add(-2 * time.Second),
		LastHeartbeatAt: now.Add(-2 * time.Second),
		LastActivity:    heartbeatActivity,
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
	got, _ := time.Parse(time.RFC3339Nano, *out[0].LastActivityAt)
	if got.Equal(wrongTime) {
		t.Error("composer called jsonl reader despite missing Cwd; want skip")
	}
	if !got.Equal(heartbeatActivity) {
		t.Errorf("LastActivityAt = %v, want heartbeat %v", got, heartbeatActivity)
	}
}

// TestComposer_LastActivityAt_FallsBackToPaneSession verifies the third
// tier: with no heartbeat last_activity AND no jsonl hit, the pane's
// session-level lastActivityAt is the last resort. Coarse but better
// than null for the GUI.
func TestComposer_LastActivityAt_FallsBackToPaneSession(t *testing.T) {
	now := time.Date(2026, 5, 9, 22, 0, 0, 0, time.UTC)
	hb := Heartbeat{
		TmuxSession:     "alpha",
		SessionID:       "uuid-alpha",
		State:           "working",
		ClaudePid:       42100,
		Cwd:             "/home/user/workspace/foo",
		Timestamp:       now.Add(-2 * time.Second),
		LastHeartbeatAt: now.Add(-2 * time.Second),
		// LastActivity intentionally zero
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
