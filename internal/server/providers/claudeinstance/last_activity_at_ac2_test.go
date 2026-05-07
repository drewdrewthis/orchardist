package claudeinstance

// Tests for ClaudeInstance.lastActivityAt — AC 2 of issue #443.
//
// Seven unit scenarios from specs/features/schema-claude-instance-last-activity-at.feature:
//
//  1. "Resolver prefers heartbeat last_activity over the pane fallback"
//  2. "Resolver falls back to the TmuxPane's lastActivityAt when heartbeat last_activity is absent"
//  3. "Resolver falls back to the TmuxPane's lastActivityAt when heartbeat last_activity is empty string"
//  4. "Resolver returns null when heartbeat lacks last_activity AND no pane matches"
//  5. "Resolver returns null when heartbeat lacks last_activity AND the matched pane has no lastActivityAt"
//  6. "Heartbeat parser accepts both last_activity (snake_case) and lastActivity (camelCase)"
//  7. "Malformed heartbeat last_activity is treated as absent (does not crash, falls back)"
//
// Design note (option a, per spec):
//   TmuxPane today has no lastActivityAt field. The fallback reads
//   pane.Window.Session.LastActivityAt — existing data, no new schema surface.
//   This is deliberate: session-level recency is the same conceptual signal
//   ("when was this claude instance last touched") without adding a new field.
//   See commit message for rationale.

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/drewdrewthis/git-orchard-rs/internal/server/graphql"
)

// buildPaneWithSessionLastActivityAt constructs a TmuxPane → TmuxWindow →
// TmuxSession graph whose session carries the given lastActivityAt string.
// When lastActivityAt is empty, the session's field is nil (absent).
func buildPaneWithSessionLastActivityAt(lastActivityAt string) *graphql.TmuxPane {
	var sessLastActivity *string
	if lastActivityAt != "" {
		v := lastActivityAt
		sessLastActivity = &v
	}
	sess := &graphql.TmuxSession{
		ID:             "TmuxSession:local:test",
		Name:           "test",
		CreatedAt:      "2026-05-07T18:00:00Z",
		LastActivityAt: sessLastActivity,
	}
	win := &graphql.TmuxWindow{
		ID:      "TmuxWindow:local:test:0",
		Session: sess,
		Name:    "test",
	}
	pane := &graphql.TmuxPane{
		ID:     "TmuxPane:local:%26",
		Window: win,
		PaneID: "%26",
	}
	return pane
}

// heartbeatWithLastActivity returns a fresh Heartbeat with LastActivity set
// to the parsed value of rawTS. If rawTS is empty, LastActivity is zero.
func heartbeatWithLastActivity(rawTS string) Heartbeat {
	now := time.Date(2026, 5, 7, 18, 42, 15, 0, time.UTC)
	hb := Heartbeat{
		TmuxSession:     "alpha",
		SessionID:       "uuid-alpha",
		State:           "working",
		Timestamp:       now.Add(-5 * time.Second),
		LastHeartbeatAt: now.Add(-5 * time.Second),
	}
	if rawTS != "" {
		if t, err := time.Parse(time.RFC3339Nano, rawTS); err == nil {
			hb.LastActivity = t
		} else if t, err := time.Parse(time.RFC3339, rawTS); err == nil {
			hb.LastActivity = t
		}
		// Malformed rawTS → LastActivity stays zero (intentional for scenario 7).
	}
	return hb
}

// newComposerForTest builds a Composer with a fixed clock suitable for
// AC 2 tests. The pane finder returns a static pane keyed by session name.
func newComposerForTest(pane *graphql.TmuxPane) *Composer {
	now := time.Date(2026, 5, 7, 18, 42, 15, 0, time.UTC)
	var pf PaneFinder
	if pane != nil {
		pf = &fakePaneFinder{
			bySession: map[string]*graphql.TmuxPane{"alpha": pane},
		}
	} else {
		pf = &fakePaneFinder{} // no matches
	}
	return NewComposerWith(
		"local",
		pf,
		&fakeProcessFinder{},
		&fakeAccountFinder{},
		fakeLiveness{alive: map[int]bool{}},
		func() time.Time { return now },
		HeartbeatStaleAfter,
	)
}

// TestLastActivityAt_PrefersHeartbeatOverPane covers scenario 1:
// "Resolver prefers heartbeat last_activity over the pane fallback."
//
// When the heartbeat carries a last_activity, the composer uses it and
// does not consult the pane's session lastActivityAt.
func TestLastActivityAt_PrefersHeartbeatOverPane(t *testing.T) {
	const heartbeatTS = "2026-05-07T18:42:11Z"
	const paneSessionTS = "2026-05-07T18:30:00Z"

	pane := buildPaneWithSessionLastActivityAt(paneSessionTS)
	hb := heartbeatWithLastActivity(heartbeatTS)

	c := newComposerForTest(pane)
	out := c.Compose(context.Background(), []Heartbeat{hb})
	if len(out) != 1 {
		t.Fatalf("got %d instances, want 1", len(out))
	}
	inst := out[0]
	if inst.LastActivityAt == nil {
		t.Fatal("LastActivityAt is nil; expected heartbeat value")
	}
	got := *inst.LastActivityAt
	// Parsed time must equal heartbeatTS, not paneSessionTS.
	wantParsed, _ := time.Parse(time.RFC3339, heartbeatTS)
	gotParsed, err := time.Parse(time.RFC3339Nano, got)
	if err != nil {
		gotParsed, err = time.Parse(time.RFC3339, got)
		if err != nil {
			t.Fatalf("LastActivityAt %q is not parseable as RFC3339/RFC3339Nano: %v", got, err)
		}
	}
	if !gotParsed.Equal(wantParsed) {
		t.Errorf("LastActivityAt = %v, want heartbeat value %v (not pane %s)", gotParsed, wantParsed, paneSessionTS)
	}
}

// TestLastActivityAt_FallsBackToPaneWhenAbsent covers scenario 2:
// "Resolver falls back to the TmuxPane's lastActivityAt when heartbeat
// last_activity is absent."
func TestLastActivityAt_FallsBackToPaneWhenAbsent(t *testing.T) {
	const paneSessionTS = "2026-05-07T18:30:00Z"

	pane := buildPaneWithSessionLastActivityAt(paneSessionTS)
	hb := heartbeatWithLastActivity("") // no last_activity
	hb.LastActivity = time.Time{}       // ensure zero

	c := newComposerForTest(pane)
	out := c.Compose(context.Background(), []Heartbeat{hb})
	if len(out) != 1 {
		t.Fatalf("got %d instances, want 1", len(out))
	}
	inst := out[0]
	if inst.LastActivityAt == nil {
		t.Fatal("LastActivityAt is nil; expected pane session fallback value")
	}
	got := *inst.LastActivityAt
	wantParsed, _ := time.Parse(time.RFC3339, paneSessionTS)
	gotParsed, err := time.Parse(time.RFC3339Nano, got)
	if err != nil {
		gotParsed, err = time.Parse(time.RFC3339, got)
		if err != nil {
			t.Fatalf("LastActivityAt %q is not parseable: %v", got, err)
		}
	}
	if !gotParsed.Equal(wantParsed) {
		t.Errorf("LastActivityAt = %v, want pane session value %v", gotParsed, wantParsed)
	}
}

// TestLastActivityAt_FallsBackToPaneWhenEmptyString covers scenario 3:
// "Resolver falls back to the TmuxPane's lastActivityAt when heartbeat
// last_activity is empty string."
//
// An empty string in the JSON field produces a zero LastActivity in the
// Heartbeat; the composer must treat that the same as absent and fall back.
func TestLastActivityAt_FallsBackToPaneWhenEmptyString(t *testing.T) {
	const paneSessionTS = "2026-05-07T18:30:00Z"

	dir := t.TempDir()
	// Write a heartbeat file with last_activity: "" (empty string).
	writeJSON(t, filepath.Join(dir, "orchard-claude-alpha.json"), map[string]any{
		"tmux_session":  "alpha",
		"session_id":    "uuid-alpha",
		"state":         "working",
		"timestamp":     "2026-05-07T18:42:10Z",
		"last_activity": "",
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
	if !hb.LastActivity.IsZero() {
		t.Errorf("empty-string last_activity should parse to zero; got %v", hb.LastActivity)
	}

	pane := buildPaneWithSessionLastActivityAt(paneSessionTS)
	c := newComposerForTest(pane)
	out := c.Compose(context.Background(), []Heartbeat{hb})
	if len(out) != 1 {
		t.Fatalf("got %d instances, want 1", len(out))
	}
	inst := out[0]
	if inst.LastActivityAt == nil {
		t.Fatal("LastActivityAt is nil; expected pane session fallback for empty-string last_activity")
	}
	got := *inst.LastActivityAt
	wantParsed, _ := time.Parse(time.RFC3339, paneSessionTS)
	gotParsed, err := time.Parse(time.RFC3339Nano, got)
	if err != nil {
		gotParsed, err = time.Parse(time.RFC3339, got)
		if err != nil {
			t.Fatalf("LastActivityAt %q is not parseable: %v", got, err)
		}
	}
	if !gotParsed.Equal(wantParsed) {
		t.Errorf("LastActivityAt = %v, want pane session value %v", gotParsed, wantParsed)
	}
}

// TestLastActivityAt_NullWhenNoPaneAndNoHeartbeat covers scenario 4:
// "Resolver returns null when heartbeat lacks last_activity AND no pane matches."
func TestLastActivityAt_NullWhenNoPaneAndNoHeartbeat(t *testing.T) {
	hb := heartbeatWithLastActivity("")
	hb.LastActivity = time.Time{}

	c := newComposerForTest(nil) // no pane
	out := c.Compose(context.Background(), []Heartbeat{hb})
	if len(out) != 1 {
		t.Fatalf("got %d instances, want 1", len(out))
	}
	if out[0].LastActivityAt != nil {
		t.Errorf("LastActivityAt = %v, want nil when no heartbeat last_activity and no pane", out[0].LastActivityAt)
	}
}

// TestLastActivityAt_NullWhenPaneHasNoLastActivityAt covers scenario 5:
// "Resolver returns null when heartbeat lacks last_activity AND the matched
// pane has no lastActivityAt."
func TestLastActivityAt_NullWhenPaneHasNoLastActivityAt(t *testing.T) {
	// Pane exists but its session has no lastActivityAt (nil).
	pane := buildPaneWithSessionLastActivityAt("") // empty → session.LastActivityAt = nil

	hb := heartbeatWithLastActivity("")
	hb.LastActivity = time.Time{}

	c := newComposerForTest(pane)
	out := c.Compose(context.Background(), []Heartbeat{hb})
	if len(out) != 1 {
		t.Fatalf("got %d instances, want 1", len(out))
	}
	if out[0].LastActivityAt != nil {
		t.Errorf("LastActivityAt = %v, want nil when pane session has no lastActivityAt", out[0].LastActivityAt)
	}
}

// TestLastActivityAt_DualTagParser covers scenario 6:
// "Heartbeat parser accepts both last_activity (snake_case) and lastActivity (camelCase)."
//
// Both naming conventions must produce the same Heartbeat.LastActivity value.
func TestLastActivityAt_DualTagParser(t *testing.T) {
	const rawTS = "2026-05-07T18:42:11Z"
	dir := t.TempDir()

	// snake_case: last_activity
	writeJSON(t, filepath.Join(dir, "orchard-claude-snake.json"), map[string]any{
		"tmux_session":  "snake",
		"session_id":    "uuid-snake",
		"state":         "working",
		"timestamp":     rawTS,
		"last_activity": rawTS,
	})

	// camelCase: lastActivity
	writeJSON(t, filepath.Join(dir, "orchard-claude-camel.json"), map[string]any{
		"tmux_session":  "camel",
		"session_id":    "uuid-camel",
		"state":         "working",
		"timestamp":     rawTS,
		"lastActivity":  rawTS,
	})

	r := NewFileReader(dir)
	hbs, err := r.ReadAll(context.Background())
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if len(hbs) != 2 {
		t.Fatalf("got %d heartbeats, want 2 (snake + camel)", len(hbs))
	}

	// Sorted: camel before snake.
	camel := hbs[0]
	snake := hbs[1]
	if camel.TmuxSession != "camel" {
		t.Errorf("hbs[0].TmuxSession = %s, want camel (alphabetical sort)", camel.TmuxSession)
	}
	if snake.TmuxSession != "snake" {
		t.Errorf("hbs[1].TmuxSession = %s, want snake", snake.TmuxSession)
	}

	if snake.LastActivity.IsZero() {
		t.Error("snake heartbeat: LastActivity is zero; should parse from last_activity field")
	}
	if camel.LastActivity.IsZero() {
		t.Error("camel heartbeat: LastActivity is zero; should parse from lastActivity field")
	}
	if !snake.LastActivity.Equal(camel.LastActivity) {
		t.Errorf("snake.LastActivity %v != camel.LastActivity %v; both forms must produce the same timestamp",
			snake.LastActivity, camel.LastActivity)
	}
}

// TestLastActivityAt_MalformedFallsBackToPane covers scenario 7:
// "Malformed heartbeat last_activity is treated as absent (does not crash,
// falls back)."
//
// A non-timestamp string in last_activity must be silently ignored (LastActivity
// stays zero), causing the composer to fall back to the pane's session.
func TestLastActivityAt_MalformedFallsBackToPane(t *testing.T) {
	const paneSessionTS = "2026-05-07T18:30:00Z"

	dir := t.TempDir()
	writeJSON(t, filepath.Join(dir, "orchard-claude-alpha.json"), map[string]any{
		"tmux_session":  "alpha",
		"session_id":    "uuid-alpha",
		"state":         "working",
		"timestamp":     "2026-05-07T18:42:10Z",
		"last_activity": "not-a-timestamp",
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

	// Malformed value must produce zero LastActivity, not crash.
	if !hb.LastActivity.IsZero() {
		t.Errorf("malformed last_activity should be ignored; got LastActivity = %v", hb.LastActivity)
	}

	// Composer must fall back to the pane's session lastActivityAt.
	pane := buildPaneWithSessionLastActivityAt(paneSessionTS)
	c := newComposerForTest(pane)
	out := c.Compose(context.Background(), []Heartbeat{hb})
	if len(out) != 1 {
		t.Fatalf("got %d instances, want 1", len(out))
	}
	inst := out[0]
	if inst.LastActivityAt == nil {
		t.Fatal("LastActivityAt is nil; expected pane session fallback after malformed last_activity")
	}
	got := *inst.LastActivityAt
	wantParsed, _ := time.Parse(time.RFC3339, paneSessionTS)
	gotParsed, err := time.Parse(time.RFC3339Nano, got)
	if err != nil {
		gotParsed, err = time.Parse(time.RFC3339, got)
		if err != nil {
			t.Fatalf("LastActivityAt %q is not parseable: %v", got, err)
		}
	}
	if !gotParsed.Equal(wantParsed) {
		t.Errorf("LastActivityAt = %v, want pane session value %v", gotParsed, wantParsed)
	}
}
