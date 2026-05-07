package claudeinstance

// Tests for ClaudeInstance.lastActivityAt — AC 3 of issue #443.
//
// Three scenarios from specs/features/schema-claude-instance-last-activity-at.feature:
//
//  1. @unit "instancesEqual treats lastActivityAt as observable"
//     — instancesEqual must return false when only LastActivityAt differs.
//
//  2. @integration "Heartbeat refresh that changes only last_activity emits a nodeChanged event"
//     — a refresh whose only change is a new lastActivityAt must fire one
//     subscriber event.
//
//  3. @integration "Heartbeat refresh where lastActivityAt did not change does NOT emit a noise event"
//     — when lastActivityAt is identical between sweeps, no event must fire.

import (
	"context"
	"testing"
	"time"

	"github.com/drewdrewthis/git-orchard-rs/internal/server/graphql"
)

// ptrStr is a tiny helper that returns a pointer to s. Used throughout
// these tests to build *string values without declaring a local variable
// on every line.
func ptrStr(s string) *string { return &s }

// TestInstancesEqual_TreatsLastActivityAtAsObservable covers the @unit
// scenario:
//
//	"instancesEqual treats lastActivityAt as observable"
//
// Two ClaudeInstance values that are identical except for LastActivityAt
// must NOT be equal — the change-detection path would otherwise silently
// suppress a subscription emit when only activity recency changes.
func TestInstancesEqual_TreatsLastActivityAtAsObservable(t *testing.T) {
	base := &graphql.ClaudeInstance{
		ID:             "ClaudeInstance:local:42100",
		State:          graphql.InstanceStateWorking,
		RcEnabled:      false,
		LastActivityAt: ptrStr("2026-05-07T18:30:00Z"),
	}
	changed := &graphql.ClaudeInstance{
		ID:             "ClaudeInstance:local:42100",
		State:          graphql.InstanceStateWorking,
		RcEnabled:      false,
		LastActivityAt: ptrStr("2026-05-07T18:42:11Z"),
	}

	if instancesEqual(base, changed) {
		t.Error("instancesEqual returned true for instances that differ only in LastActivityAt; " +
			"want false so the subscription path emits an event")
	}

	// Sanity: identical structs must still compare equal.
	if !instancesEqual(base, base) {
		t.Error("instancesEqual(base, base) returned false; want true")
	}
}

// TestProvider_Subscribe_FiresOnLastActivityChange covers the @integration
// scenario:
//
//	"Heartbeat refresh that changes only last_activity emits a nodeChanged event"
//
// The test boots a Provider with a staticReader, subscribes, triggers an
// initial sweep to populate the cache, then triggers a second sweep that
// produces the same instance with an updated LastActivityAt — only that
// field changes. The subscriber must receive exactly one event.
func TestProvider_Subscribe_FiresOnLastActivityChange(t *testing.T) {
	now := time.Date(2026, 5, 7, 18, 30, 0, 0, time.UTC)
	clock := func() time.Time { return now }

	const pid = 42100
	baseTS := "2026-05-07T18:30:00Z"
	laterTS := "2026-05-07T18:42:11Z"

	// Heartbeat for the initial sweep: baseTS as LastActivity.
	baseActivity, _ := time.Parse(time.RFC3339, baseTS)

	r := &staticReader{
		heartbeats: []Heartbeat{{
			TmuxSession:     "alpha",
			SessionID:       "uuid-alpha",
			State:           "working",
			ClaudePid:       pid,
			Timestamp:       now.Add(-2 * time.Second),
			LastHeartbeatAt: now.Add(-2 * time.Second),
			LastActivity:    baseActivity,
		}},
	}
	c := NewComposerWith("local", nil, nil, nil,
		fakeLiveness{alive: map[int]bool{pid: true}},
		clock,
		HeartbeatStaleAfter,
	)
	p := NewWith("local", r, c, clock)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events := p.Subscribe(ctx)

	// Populate cache with the initial heartbeat.
	if err := p.Refresh(ctx, "boot"); err != nil {
		t.Fatalf("initial Refresh: %v", err)
	}

	// Drain any initial event (new key appearing always fires).
	select {
	case <-events:
	case <-time.After(50 * time.Millisecond):
	}

	// Re-sweep with a new LastActivity and nothing else changed.
	laterActivity, _ := time.Parse(time.RFC3339, laterTS)
	r.heartbeats = []Heartbeat{{
		TmuxSession:     "alpha",
		SessionID:       "uuid-alpha",
		State:           "working",
		ClaudePid:       pid,
		Timestamp:       now.Add(-2 * time.Second),
		LastHeartbeatAt: now.Add(-2 * time.Second),
		LastActivity:    laterActivity,
	}}

	if err := p.Refresh(ctx, "activity-changed"); err != nil {
		t.Fatalf("second Refresh: %v", err)
	}

	select {
	case ev := <-events:
		if ev.Key.ClaudePid != pid {
			t.Errorf("event pid = %d, want %d", ev.Key.ClaudePid, pid)
		}
		if ev.Reason != "activity-changed" {
			t.Errorf("event reason = %q, want activity-changed", ev.Reason)
		}
		// Verify the cache carries the new lastActivityAt.
		inst, _, err := p.Get(ctx, ev.Key)
		if err != nil {
			t.Fatalf("Get after event: %v", err)
		}
		if inst.LastActivityAt == nil || *inst.LastActivityAt == "" {
			t.Fatal("cached instance has nil/empty LastActivityAt after event")
		}
		gotParsed, err := time.Parse(time.RFC3339Nano, *inst.LastActivityAt)
		if err != nil {
			gotParsed, err = time.Parse(time.RFC3339, *inst.LastActivityAt)
			if err != nil {
				t.Fatalf("LastActivityAt %q not parseable: %v", *inst.LastActivityAt, err)
			}
		}
		wantParsed, _ := time.Parse(time.RFC3339, laterTS)
		if !gotParsed.Equal(wantParsed) {
			t.Errorf("event payload LastActivityAt = %v, want %v", gotParsed, wantParsed)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("no event received after lastActivityAt change; want one event")
	}
}

// TestProvider_Subscribe_SilentOnLastActivityUnchanged covers the
// @integration scenario:
//
//	"Heartbeat refresh where lastActivityAt did not change does NOT emit a noise event"
//
// When the sweep produces an identical instance (same LastActivityAt and
// all other fields), the subscriber must NOT receive an event.
func TestProvider_Subscribe_SilentOnLastActivityUnchanged(t *testing.T) {
	now := time.Date(2026, 5, 7, 18, 30, 0, 0, time.UTC)
	clock := func() time.Time { return now }

	const pid = 42200
	const sameTS = "2026-05-07T18:30:00Z"
	sameActivity, _ := time.Parse(time.RFC3339, sameTS)

	hb := Heartbeat{
		TmuxSession:     "bravo",
		SessionID:       "uuid-bravo",
		State:           "idle",
		ClaudePid:       pid,
		Timestamp:       now.Add(-2 * time.Second),
		LastHeartbeatAt: now.Add(-2 * time.Second),
		LastActivity:    sameActivity,
	}
	r := &staticReader{heartbeats: []Heartbeat{hb}}
	c := NewComposerWith("local", nil, nil, nil,
		fakeLiveness{alive: map[int]bool{pid: true}},
		clock,
		HeartbeatStaleAfter,
	)
	p := NewWith("local", r, c, clock)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events := p.Subscribe(ctx)

	// First sweep — populates the cache (may fire one event for new key).
	if err := p.Refresh(ctx, "boot"); err != nil {
		t.Fatalf("first Refresh: %v", err)
	}
	// Drain the new-key event.
	select {
	case <-events:
	case <-time.After(50 * time.Millisecond):
	}

	// Second sweep with IDENTICAL data — no change in LastActivityAt.
	if err := p.Refresh(ctx, "noop"); err != nil {
		t.Fatalf("second Refresh: %v", err)
	}

	select {
	case ev := <-events:
		t.Errorf("unexpected event for unchanged lastActivityAt: %+v", ev)
	case <-time.After(100 * time.Millisecond):
		// No event — desired.
	}
}
