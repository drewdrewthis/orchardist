package claudeinstance

// Tests for ClaudeInstance.lastActivityAt — AC 1 of issue #443.
//
// Three unit scenarios from specs/features/schema-claude-instance-last-activity-at.feature:
//
//   1. "lastActivityAt field is declared on ClaudeInstance and is nullable String"
//      — verified by compile-time field access on graphql.ClaudeInstance.
//
//   2. "lastActivityAt resolver returns RFC3339 string for an instance with a
//      fresh heartbeat last_activity"
//      — parseFile → Heartbeat.LastActivity populated; composeOne → inst.LastActivityAt set.
//
//   3. "lastActivityAt resolver returns RFC3339Nano when the heartbeat
//      last_activity is sub-second"
//      — sub-second precision is preserved through the full pipeline.

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/drewdrewthis/git-orchard-rs/internal/server/graphql"
)

// TestLastActivityAt_FieldExistsAndIsNullable verifies that the
// ClaudeInstance GraphQL model carries a nullable *string LastActivityAt
// field — the schema contract for AC 1, scenario 1.
//
// This test will fail to compile if the field is absent or has the wrong
// type, which is sufficient proof that the schema change landed.
func TestLastActivityAt_FieldExistsAndIsNullable(t *testing.T) {
	inst := graphql.ClaudeInstance{}
	// Nullable means the zero value is nil (pointer).
	if inst.LastActivityAt != nil {
		t.Errorf("zero-value ClaudeInstance.LastActivityAt should be nil (nullable); got %v", inst.LastActivityAt)
	}
	// Assign to confirm the type is *string (compile-time check).
	v := "2026-05-07T18:42:11Z"
	inst.LastActivityAt = &v
	if inst.LastActivityAt == nil || *inst.LastActivityAt != v {
		t.Errorf("LastActivityAt = %v, want %q", inst.LastActivityAt, v)
	}
}

// TestLastActivityAt_RFC3339FromHeartbeat verifies scenario 2:
// a heartbeat with last_activity "2026-05-07T18:42:11Z" produces a
// ClaudeInstance whose LastActivityAt is the same RFC3339 string.
func TestLastActivityAt_RFC3339FromHeartbeat(t *testing.T) {
	const rawTS = "2026-05-07T18:42:11Z"
	dir := t.TempDir()

	writeJSON(t, filepath.Join(dir, "orchard-claude-alpha.json"), map[string]any{
		"tmux_session":  "alpha",
		"session_id":    "uuid-alpha",
		"state":         "working",
		"timestamp":     rawTS,
		"last_activity": rawTS,
	})

	r := NewFileReader(dir)
	hbs, err := r.ReadAll(context.Background())
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if len(hbs) != 1 {
		t.Fatalf("got %d heartbeats, want 1", len(hbs))
	}
	hb := hbs[0]
	if hb.LastActivity.IsZero() {
		t.Fatal("Heartbeat.LastActivity is zero; expected it to be parsed from last_activity field")
	}

	// Build a composer and compose the instance.
	now := time.Date(2026, 5, 7, 18, 42, 15, 0, time.UTC) // 4s after activity
	c := NewComposerWith(
		"local",
		&fakePaneFinder{},
		&fakeProcessFinder{},
		&fakeAccountFinder{},
		fakeLiveness{alive: map[int]bool{}},
		func() time.Time { return now },
		HeartbeatStaleAfter,
	)
	out := c.Compose(context.Background(), hbs)
	if len(out) != 1 {
		t.Fatalf("got %d instances, want 1", len(out))
	}
	inst := out[0]
	if inst.LastActivityAt == nil {
		t.Fatal("ClaudeInstance.LastActivityAt is nil; expected RFC3339 string")
	}

	// Must parse cleanly as RFC3339 or RFC3339Nano.
	got := *inst.LastActivityAt
	if _, err := time.Parse(time.RFC3339Nano, got); err != nil {
		if _, err2 := time.Parse(time.RFC3339, got); err2 != nil {
			t.Errorf("LastActivityAt %q does not parse as RFC3339/RFC3339Nano: %v", got, err)
		}
	}

	// Round-trip: parsed time must equal original.
	parsed, _ := time.Parse(time.RFC3339, rawTS)
	gotParsed, _ := time.Parse(time.RFC3339Nano, got)
	if !gotParsed.Equal(parsed) {
		t.Errorf("LastActivityAt round-trip mismatch: got %v, want %v", gotParsed, parsed)
	}
}

// TestLastActivityAt_RFC3339NanoSubSecond verifies scenario 3:
// a heartbeat with last_activity "2026-05-07T18:42:11.123456Z" (sub-second)
// produces a LastActivityAt that preserves the sub-second precision.
func TestLastActivityAt_RFC3339NanoSubSecond(t *testing.T) {
	const rawTS = "2026-05-07T18:42:11.123456Z"
	dir := t.TempDir()

	writeJSON(t, filepath.Join(dir, "orchard-claude-subsec.json"), map[string]any{
		"tmux_session":  "subsec",
		"session_id":    "uuid-subsec",
		"state":         "idle",
		"timestamp":     rawTS,
		"last_activity": rawTS,
	})

	r := NewFileReader(dir)
	hbs, err := r.ReadAll(context.Background())
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if len(hbs) != 1 {
		t.Fatalf("got %d heartbeats, want 1", len(hbs))
	}
	hb := hbs[0]
	if hb.LastActivity.IsZero() {
		t.Fatal("Heartbeat.LastActivity is zero; expected sub-second timestamp")
	}
	// Verify the nanosecond component was preserved in the in-memory struct.
	if hb.LastActivity.Nanosecond() == 0 {
		t.Errorf("Heartbeat.LastActivity has no sub-second component; got %v", hb.LastActivity)
	}

	now := time.Date(2026, 5, 7, 18, 42, 15, 0, time.UTC)
	c := NewComposerWith(
		"local",
		&fakePaneFinder{},
		&fakeProcessFinder{},
		&fakeAccountFinder{},
		fakeLiveness{alive: map[int]bool{}},
		func() time.Time { return now },
		HeartbeatStaleAfter,
	)
	out := c.Compose(context.Background(), hbs)
	if len(out) != 1 {
		t.Fatalf("got %d instances, want 1", len(out))
	}
	inst := out[0]
	if inst.LastActivityAt == nil {
		t.Fatal("ClaudeInstance.LastActivityAt is nil")
	}

	got := *inst.LastActivityAt

	// Must parse as RFC3339Nano (since it has sub-second precision).
	parsed, err := time.Parse(time.RFC3339Nano, got)
	if err != nil {
		t.Fatalf("LastActivityAt %q does not parse as RFC3339Nano: %v", got, err)
	}

	// The parsed time must equal the original (sub-second preserved).
	original, _ := time.Parse(time.RFC3339Nano, rawTS)
	if !parsed.Equal(original) {
		t.Errorf("sub-second precision lost: got %v, want %v", parsed, original)
	}
	if parsed.Nanosecond() == 0 {
		t.Errorf("sub-second component missing in output: got %q", got)
	}
}

// writeJSON and writeRaw helpers are defined in adapter_test.go and are
// shared across all _test.go files in this package.
