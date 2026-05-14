package claudeinstance

// resolver_jsonl_test.go — Phase 2 integration tests: verifies that the
// composer uses jsonl-derived state as the authoritative source, overriding
// the hook-derived heartbeat value.
//
// These tests cover the acceptance criteria from issue #603 Phase 2:
//   - AC#1: jsonl idle wins over hook input (fabrication removal)
//   - AC#2: jsonl working wins over hook idle (open tool_use)
//   - AC#3: AskUserQuestion in jsonl → input state
//   - AC#4: Notification hook=input, jsonl=idle → state=idle (negative test)
//   - AC#5: No jsonl found → idle (not hook value)
//   - AC#6: model field populated from jsonl assistant record
//   - AC#7: inflightToolCount derived from jsonl
//   - AC#8: lastActivityAt quantized to 1-second resolution

import (
	"context"
	"testing"
	"time"

	"github.com/drewdrewthis/git-orchard-rs/internal/server/graphql"
)

// fakeSnapshotReader implements SnapshotReader for tests. Configure
// byKey (keyed by cwd+"|"+sessionID) or a default (records + ok).
// Returns (nil, false) on miss, mirroring FsSnapshotReader on missing file.
type fakeSnapshotReader struct {
	records []Record
	ok      bool
	byKey   map[string][]Record
}

func (f *fakeSnapshotReader) ReadSnapshot(_ context.Context, cwd, sessionID string) ([]Record, bool) {
	if f.byKey != nil {
		if recs, ok := f.byKey[cwd+"|"+sessionID]; ok {
			return recs, true
		}
		return nil, false
	}
	if !f.ok {
		return nil, false
	}
	return f.records, true
}

// newTestComposerWithSnapshot is a helper that wires a composer with a
// fake snapshot reader and fake liveness. The jsonl reader is nil (unused
// in Phase 2 for state derivation; only for lastActivityAt fallback).
func newTestComposerWithSnapshot(
	snap SnapshotReader,
	liveness fakeLiveness,
) *Composer {
	now := time.Date(2026, 5, 14, 10, 0, 0, 0, time.UTC)
	c := NewComposerWith(
		"local",
		nil,
		nil,
		nil,
		liveness,
		nil,
		func() time.Time { return now },
		HeartbeatStaleAfter,
	)
	c.snapshot = snap
	return c
}

// freshHeartbeat builds a heartbeat that is well within the stale window
// so liveness is the only gating factor.
func freshHeartbeat(tmuxSession, sessionID, state, cwd string, pid int) Heartbeat {
	now := time.Date(2026, 5, 14, 10, 0, 0, 0, time.UTC)
	return Heartbeat{
		TmuxSession:     tmuxSession,
		SessionID:       sessionID,
		State:           state,
		ClaudePid:       pid,
		Cwd:             cwd,
		Timestamp:       now.Add(-2 * time.Second),
		LastHeartbeatAt: now.Add(-2 * time.Second),
	}
}

// TestResolver_StateFromJsonl_Idle: heartbeat says "input" (Notification
// fabrication), jsonl ends with end_turn → assert state==idle.
// This is the load-bearing Phase 2 test: the fabricated hook input is gone.
func TestResolver_StateFromJsonl_Idle(t *testing.T) {
	records := []Record{
		userRecord(ts(0), nil),
		assistantRecord(ts(1), "end_turn", nil),
		systemRecord(ts(2), "turn_duration"),
	}
	snap := &fakeSnapshotReader{records: records, ok: true}
	hb := freshHeartbeat("alpha", "uuid-alpha", "input", "/workspace/alpha", 42100)
	c := newTestComposerWithSnapshot(snap, fakeLiveness{alive: map[int]bool{42100: true}})

	out := c.Compose(context.Background(), []Heartbeat{hb})
	if len(out) != 1 {
		t.Fatalf("got %d, want 1", len(out))
	}
	if out[0].State != graphql.InstanceStateIdle {
		t.Errorf("state = %s, want idle (jsonl end_turn wins over hook input)", out[0].State)
	}
}

// TestResolver_StateFromJsonl_Working: heartbeat says "idle", jsonl has an
// open tool_use → assert state==working.
func TestResolver_StateFromJsonl_Working(t *testing.T) {
	records := []Record{
		userRecord(ts(0), nil),
		assistantRecord(ts(1), "tool_use", []ContentItem{
			toolUseContent("bash_001", "Bash"),
		}),
	}
	snap := &fakeSnapshotReader{records: records, ok: true}
	hb := freshHeartbeat("bravo", "uuid-bravo", "idle", "/workspace/bravo", 42200)
	c := newTestComposerWithSnapshot(snap, fakeLiveness{alive: map[int]bool{42200: true}})

	out := c.Compose(context.Background(), []Heartbeat{hb})
	if out[0].State != graphql.InstanceStateWorking {
		t.Errorf("state = %s, want working (jsonl open tool_use wins over hook idle)", out[0].State)
	}
}

// TestResolver_StateFromJsonl_InputAskUserQuestion: open AskUserQuestion
// in jsonl, hook says anything → assert state==input.
func TestResolver_StateFromJsonl_InputAskUserQuestion(t *testing.T) {
	records := []Record{
		userRecord(ts(0), nil),
		assistantRecord(ts(1), "tool_use", []ContentItem{
			toolUseContent("ask_99", "AskUserQuestion"),
		}),
	}
	snap := &fakeSnapshotReader{records: records, ok: true}
	hb := freshHeartbeat("charlie", "uuid-charlie", "working", "/workspace/charlie", 42300)
	c := newTestComposerWithSnapshot(snap, fakeLiveness{alive: map[int]bool{42300: true}})

	out := c.Compose(context.Background(), []Heartbeat{hb})
	if out[0].State != graphql.InstanceStateInput {
		t.Errorf("state = %s, want input (AskUserQuestion open in jsonl)", out[0].State)
	}
}

// TestResolver_StateFromJsonl_NotificationNoFlip: hook says "input"
// (Notification event fabrication), jsonl says "idle" → assert state==idle.
// This is AC#5 from the issue: the negative test proving Notification-driven
// input events are eliminated.
func TestResolver_StateFromJsonl_NotificationNoFlip(t *testing.T) {
	// jsonl: completed turn (idle)
	records := []Record{
		userRecord(ts(0), nil),
		assistantRecord(ts(1), "end_turn", nil),
		systemRecord(ts(2), "turn_duration"),
	}
	snap := &fakeSnapshotReader{records: records, ok: true}
	// Hook says input — as it would for a Notification event.
	hb := freshHeartbeat("delta", "uuid-delta", "input", "/workspace/delta", 42400)
	c := newTestComposerWithSnapshot(snap, fakeLiveness{alive: map[int]bool{42400: true}})

	out := c.Compose(context.Background(), []Heartbeat{hb})
	if out[0].State == graphql.InstanceStateInput {
		t.Error("state = input, want idle — Notification-driven input fabrication must be removed (Phase 2 AC#5)")
	}
	if out[0].State != graphql.InstanceStateIdle {
		t.Errorf("state = %s, want idle", out[0].State)
	}
}

// TestResolver_FallbackWhenNoJsonl: heartbeat present, no jsonl file.
// When pid is alive → emit idle (not hook value, not no_claude).
// When pid is dead → emit no_claude.
// Decision: if no jsonl, the session has no observable transcript state;
// we fall back to idle when alive, no_claude when dead.
func TestResolver_FallbackWhenNoJsonl(t *testing.T) {
	// No jsonl found (reader returns ok=false)
	snap := &fakeSnapshotReader{ok: false}

	// Alive pid with hook state "working" → falls back to idle (not working)
	hb := freshHeartbeat("echo", "uuid-echo", "working", "/workspace/echo", 42500)
	cAlive := newTestComposerWithSnapshot(snap, fakeLiveness{alive: map[int]bool{42500: true}})
	outAlive := cAlive.Compose(context.Background(), []Heartbeat{hb})
	if outAlive[0].State != graphql.InstanceStateIdle {
		t.Errorf("alive+no-jsonl: state = %s, want idle (not hook value)", outAlive[0].State)
	}

	// Dead pid → no_claude regardless
	cDead := newTestComposerWithSnapshot(snap, fakeLiveness{alive: map[int]bool{42500: false}})
	outDead := cDead.Compose(context.Background(), []Heartbeat{hb})
	if outDead[0].State != graphql.InstanceStateNoClaude {
		t.Errorf("dead+no-jsonl: state = %s, want no_claude", outDead[0].State)
	}
}

// TestResolver_ModelFieldPopulated: jsonl has assistant records with
// message.model="claude-opus-4-7" → assert ClaudeInstance.model=="claude-opus-4-7".
func TestResolver_ModelFieldPopulated(t *testing.T) {
	records := []Record{
		userRecord(ts(0), nil),
		{
			Timestamp: ts(1),
			Type:      "assistant",
			Message: &Message{
				Model:      "claude-opus-4-7",
				StopReason: "end_turn",
				Usage: &MessageUsage{
					InputTokens:  100,
					OutputTokens: 50,
				},
			},
		},
		systemRecord(ts(2), "turn_duration"),
	}
	snap := &fakeSnapshotReader{records: records, ok: true}
	hb := freshHeartbeat("foxtrot", "uuid-foxtrot", "idle", "/workspace/foxtrot", 42600)
	c := newTestComposerWithSnapshot(snap, fakeLiveness{alive: map[int]bool{42600: true}})

	out := c.Compose(context.Background(), []Heartbeat{hb})
	if out[0].Model == nil {
		t.Fatal("Model is nil; want \"claude-opus-4-7\"")
	}
	if *out[0].Model != "claude-opus-4-7" {
		t.Errorf("Model = %q, want \"claude-opus-4-7\"", *out[0].Model)
	}
	if out[0].State != graphql.InstanceStateIdle {
		t.Errorf("state = %s, want idle (end_turn)", out[0].State)
	}
}

// TestResolver_InflightToolCount: jsonl with 3 open tool_uses and 1 closed
// → assert ClaudeInstance.inflightToolCount==2.
func TestResolver_InflightToolCount(t *testing.T) {
	records := []Record{
		userRecord(ts(0), nil),
		assistantRecord(ts(1), "tool_use", []ContentItem{
			toolUseContent("tu_1", "Bash"),
			toolUseContent("tu_2", "Read"),
			toolUseContent("tu_3", "Edit"),
		}),
		// One tool_result closes tu_1; tu_2 and tu_3 remain open.
		userRecord(ts(2), []ContentItem{
			toolResultContent("tu_1"),
		}),
	}
	snap := &fakeSnapshotReader{records: records, ok: true}
	hb := freshHeartbeat("golf", "uuid-golf", "working", "/workspace/golf", 42700)
	c := newTestComposerWithSnapshot(snap, fakeLiveness{alive: map[int]bool{42700: true}})

	out := c.Compose(context.Background(), []Heartbeat{hb})
	if out[0].InflightToolCount != 2 {
		t.Errorf("inflightToolCount = %d, want 2 (3 opened, 1 closed)", out[0].InflightToolCount)
	}
	if out[0].State != graphql.InstanceStateWorking {
		t.Errorf("state = %s, want working (2 open tool_uses)", out[0].State)
	}
}

// TestResolver_LastActivityAtQuantized: lastActivityAt is quantized to
// 1-second resolution so sub-second streaming changes do not fire
// subscription events. Two composes with sub-second difference must
// produce the same lastActivityAt string.
func TestResolver_LastActivityAtQuantized(t *testing.T) {
	baseTime := time.Date(2026, 5, 14, 10, 0, 0, 0, time.UTC)
	// Two timestamps in the same second but different nanoseconds.
	t1 := baseTime.Add(100 * time.Millisecond)
	t2 := baseTime.Add(750 * time.Millisecond)

	makeRecords := func(ts time.Time) []Record {
		return []Record{
			{
				Timestamp: ts,
				Type:      "assistant",
				Message:   &Message{StopReason: "tool_use"},
			},
		}
	}

	snap1 := &fakeSnapshotReader{records: makeRecords(t1), ok: true}
	snap2 := &fakeSnapshotReader{records: makeRecords(t2), ok: true}

	hb := freshHeartbeat("hotel", "uuid-hotel", "working", "/workspace/hotel", 42800)
	liveness := fakeLiveness{alive: map[int]bool{42800: true}}

	c1 := newTestComposerWithSnapshot(snap1, liveness)
	c2 := newTestComposerWithSnapshot(snap2, liveness)

	out1 := c1.Compose(context.Background(), []Heartbeat{hb})
	out2 := c2.Compose(context.Background(), []Heartbeat{hb})

	if out1[0].LastActivityAt == nil || out2[0].LastActivityAt == nil {
		t.Fatal("LastActivityAt is nil; expected quantized timestamp")
	}
	if *out1[0].LastActivityAt != *out2[0].LastActivityAt {
		t.Errorf(
			"LastActivityAt not quantized: %q != %q (sub-second diff should collapse to same second)",
			*out1[0].LastActivityAt, *out2[0].LastActivityAt,
		)
	}
	// Verify the format is RFC3339 (whole-second, no sub-second component).
	got := *out1[0].LastActivityAt
	if _, err := time.Parse(time.RFC3339, got); err != nil {
		t.Errorf("LastActivityAt %q is not valid RFC3339: %v", got, err)
	}
}

// TestResolver_DeadPidForcesNoClaude: pid is dead even though jsonl says
// working → must force no_claude. Dead pid always wins.
func TestResolver_DeadPidForcesNoClaude(t *testing.T) {
	records := []Record{
		userRecord(ts(0), nil),
		assistantRecord(ts(1), "tool_use", []ContentItem{
			toolUseContent("tu_alive", "Bash"),
		}),
	}
	snap := &fakeSnapshotReader{records: records, ok: true}
	hb := freshHeartbeat("india", "uuid-india", "working", "/workspace/india", 42900)
	// pid confirmed dead
	c := newTestComposerWithSnapshot(snap, fakeLiveness{alive: map[int]bool{42900: false}})

	out := c.Compose(context.Background(), []Heartbeat{hb})
	if out[0].State != graphql.InstanceStateNoClaude {
		t.Errorf("state = %s, want no_claude (dead pid always wins)", out[0].State)
	}
}
