// service_test.go — T1: typed-field resolver tests against stub service.
//
// Tests verify that:
//   - Service.List returns correctly projected instances (T1)
//   - State machine transitions are correct (T1, R14)
//   - Nil/empty guards work (T3 — no tautological assertions)
//   - Dead pid → StateNoClaude (business rule)
//   - AskUserQuestion → StateInput (business rule)
//   - Fallback lastActivityAt from pane session (business rule)
package claudeinstance

import (
	"context"
	"testing"
	"time"
)

// ─── Stubs ────────────────────────────────────────────────────────────────────

// stubJsonls is a JsonlsService stub.
type stubJsonls struct {
	m map[string]ConversationSummary // keyed by cwd
}

func (s *stubJsonls) ConversationsByCwd(_ context.Context) (map[string]ConversationSummary, error) {
	if s == nil {
		return map[string]ConversationSummary{}, nil
	}
	return s.m, nil
}

// stubPanes is a TmuxPaneReader stub.
type stubPanes struct {
	host  string
	panes []*TmuxPaneSummary
}

func (s *stubPanes) PanesByCommand(_ context.Context, _, _ string) ([]*TmuxPaneSummary, error) {
	return s.panes, nil
}

func (s *stubPanes) Host() string { return s.host }

// stubPs is a PsReader stub that maps pid → cwd.
type stubPs struct {
	cwds map[int]string
}

func (s *stubPs) LoadCwd(_ context.Context, pid int) (string, error) {
	if s == nil {
		return "", nil
	}
	if cwd, ok := s.cwds[pid]; ok {
		return cwd, nil
	}
	return "", nil
}

// stubAccount is an AccountReader stub.
type stubAccount struct {
	acct *Account
}

func (s *stubAccount) ActiveAccount(_ context.Context) (*Account, error) {
	if s == nil {
		return nil, nil
	}
	return s.acct, nil
}

// stubSnapshot is a SnapshotReader stub.
type stubSnapshot struct {
	records map[string][]Record // keyed by sessionUUID
}

func (s *stubSnapshot) ReadSnapshot(_ context.Context, _, sessionUUID string) ([]Record, bool) {
	if s == nil || s.records == nil {
		return nil, false
	}
	r, ok := s.records[sessionUUID]
	return r, ok
}

// stubLiveness is a LivenessChecker stub.
type stubLiveness struct {
	alive map[int]bool
}

func (s *stubLiveness) IsAlive(pid int) bool {
	if s == nil {
		return true
	}
	return s.alive[pid]
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

var testNow = time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)

func fixedClock(t time.Time) func() time.Time { return func() time.Time { return t } }

func assistantRec(t time.Time, stopReason string, content []ContentItem) Record {
	return Record{
		Timestamp: t,
		Type:      "assistant",
		Message:   &Message{StopReason: stopReason, Content: content},
	}
}

func userRec(t time.Time, content []ContentItem) Record {
	return Record{
		Timestamp: t,
		Type:      "user",
		Message:   &Message{Content: content},
	}
}

func systemRec(t time.Time, subtype string) Record {
	return Record{
		Timestamp: t,
		Type:      "system",
		System:    &SystemInfo{Subtype: subtype},
	}
}

// ─── Tests ────────────────────────────────────────────────────────────────────

// TestList_EmptyWhenNoPaneReader: Service.List returns [] when Panes is nil.
func TestList_EmptyWhenNoPaneReader(t *testing.T) {
	p := New(Inputs{}) // Panes == nil
	got, err := p.List(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %d instances, want 0 when Panes is nil", len(got))
	}
}

// TestList_EmptyWhenNoPanes: Service.List returns [] when PanesByCommand returns nothing.
func TestList_EmptyWhenNoPanes(t *testing.T) {
	p := New(Inputs{
		Panes: &stubPanes{host: "local", panes: nil},
	})
	got, err := p.List(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %d instances, want 0 when no panes", len(got))
	}
}

// TestList_DeadPidYieldsNoClaude: a pane whose pid is not alive → StateNoClaude.
func TestList_DeadPidYieldsNoClaude(t *testing.T) {
	const pid = 1234
	p := New(Inputs{
		Panes: &stubPanes{
			host:  "local",
			panes: []*TmuxPaneSummary{{PaneID: "p1", CurrentCommand: "claude", CurrentPid: pid}},
		},
		Ps:       &stubPs{cwds: map[int]string{pid: "/work"}},
		Liveness: &stubLiveness{alive: map[int]bool{pid: false}},
		Clock:    fixedClock(testNow),
	})
	got, err := p.List(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d instances, want 1", len(got))
	}
	if got[0].State != StateNoClaude {
		t.Errorf("state = %v, want StateNoClaude (dead pid)", got[0].State)
	}
}

// TestList_IdleWhenNoJsonl: a pane with live pid but no snapshot → StateIdle.
func TestList_IdleWhenNoJsonl(t *testing.T) {
	const pid = 5678
	p := New(Inputs{
		Panes: &stubPanes{
			host:  "local",
			panes: []*TmuxPaneSummary{{PaneID: "p2", CurrentCommand: "claude", CurrentPid: pid}},
		},
		Ps:       &stubPs{cwds: map[int]string{pid: "/work"}},
		Liveness: &stubLiveness{alive: map[int]bool{pid: true}},
		Clock:    fixedClock(testNow),
		// No Jsonls, no Snapshot → idle path.
	})
	got, err := p.List(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d instances, want 1", len(got))
	}
	if got[0].State != StateIdle {
		t.Errorf("state = %v, want StateIdle (no jsonl)", got[0].State)
	}
}

// TestList_WorkingWhenToolUseOpen: a pane with an open tool_use (not AskUserQuestion)
// → StateWorking with InflightToolCount=1.
func TestList_WorkingWhenToolUseOpen(t *testing.T) {
	const (
		pid         = 9001
		sessionUUID = "uuid-working"
		cwd         = "/repo/working"
	)
	recs := []Record{
		userRec(testNow, nil),
		assistantRec(testNow.Add(1*time.Second), "tool_use", []ContentItem{
			{Type: "tool_use", ID: "tu_1", Name: "Bash"},
		}),
	}
	p := New(Inputs{
		Panes: &stubPanes{
			host: "local",
			panes: []*TmuxPaneSummary{
				{PaneID: "p3", CurrentCommand: "claude", CurrentPid: pid},
			},
		},
		Ps:       &stubPs{cwds: map[int]string{pid: cwd}},
		Liveness: &stubLiveness{alive: map[int]bool{pid: true}},
		Jsonls: &stubJsonls{m: map[string]ConversationSummary{
			cwd: {SessionUUID: sessionUUID, Cwd: cwd, ID: "conv-1"},
		}},
		Snapshot: &stubSnapshot{records: map[string][]Record{sessionUUID: recs}},
		Clock:    fixedClock(testNow),
	})
	got, err := p.List(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d instances, want 1", len(got))
	}
	if got[0].State != StateWorking {
		t.Errorf("state = %v, want StateWorking", got[0].State)
	}
	if got[0].InflightToolCount != 1 {
		t.Errorf("inflight = %d, want 1", got[0].InflightToolCount)
	}
}

// TestList_InputWhenAskUserQuestion: open AskUserQuestion → StateInput.
func TestList_InputWhenAskUserQuestion(t *testing.T) {
	const (
		pid         = 9002
		sessionUUID = "uuid-input"
		cwd         = "/repo/input"
	)
	recs := []Record{
		userRec(testNow, nil),
		assistantRec(testNow.Add(1*time.Second), "tool_use", []ContentItem{
			{Type: "tool_use", ID: "ask_1", Name: "AskUserQuestion"},
		}),
	}
	p := New(Inputs{
		Panes: &stubPanes{
			host: "local",
			panes: []*TmuxPaneSummary{
				{PaneID: "p4", CurrentCommand: "claude", CurrentPid: pid},
			},
		},
		Ps:       &stubPs{cwds: map[int]string{pid: cwd}},
		Liveness: &stubLiveness{alive: map[int]bool{pid: true}},
		Jsonls: &stubJsonls{m: map[string]ConversationSummary{
			cwd: {SessionUUID: sessionUUID, Cwd: cwd, ID: "conv-2"},
		}},
		Snapshot: &stubSnapshot{records: map[string][]Record{sessionUUID: recs}},
		Clock:    fixedClock(testNow),
	})
	got, err := p.List(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d instances, want 1", len(got))
	}
	if got[0].State != StateInput {
		t.Errorf("state = %v, want StateInput (AskUserQuestion open)", got[0].State)
	}
}

// TestList_IdleAfterEndTurn: end_turn followed by turn_duration → StateIdle.
func TestList_IdleAfterEndTurn(t *testing.T) {
	const (
		pid         = 9003
		sessionUUID = "uuid-idle"
		cwd         = "/repo/idle"
	)
	recs := []Record{
		userRec(testNow, nil),
		assistantRec(testNow.Add(1*time.Second), "end_turn", nil),
		systemRec(testNow.Add(2*time.Second), "turn_duration"),
	}
	p := New(Inputs{
		Panes: &stubPanes{
			host: "local",
			panes: []*TmuxPaneSummary{
				{PaneID: "p5", CurrentCommand: "claude", CurrentPid: pid},
			},
		},
		Ps:       &stubPs{cwds: map[int]string{pid: cwd}},
		Liveness: &stubLiveness{alive: map[int]bool{pid: true}},
		Jsonls: &stubJsonls{m: map[string]ConversationSummary{
			cwd: {SessionUUID: sessionUUID, Cwd: cwd, ID: "conv-3"},
		}},
		Snapshot: &stubSnapshot{records: map[string][]Record{sessionUUID: recs}},
		Clock:    fixedClock(testNow),
	})
	got, err := p.List(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d instances, want 1", len(got))
	}
	if got[0].State != StateIdle {
		t.Errorf("state = %v, want StateIdle (end_turn)", got[0].State)
	}
}

// TestList_SessionUUIDPopulated: sessionUUID field is set when conversation match found.
func TestList_SessionUUIDPopulated(t *testing.T) {
	const (
		pid         = 9004
		sessionUUID = "uuid-conv"
		cwd         = "/repo/conv"
	)
	p := New(Inputs{
		Panes: &stubPanes{
			host: "local",
			panes: []*TmuxPaneSummary{
				{PaneID: "p6", CurrentCommand: "claude", CurrentPid: pid},
			},
		},
		Ps:       &stubPs{cwds: map[int]string{pid: cwd}},
		Liveness: &stubLiveness{alive: map[int]bool{pid: true}},
		Jsonls: &stubJsonls{m: map[string]ConversationSummary{
			cwd: {SessionUUID: sessionUUID, Cwd: cwd, ID: "conv-4"},
		}},
		Clock: fixedClock(testNow),
	})
	got, err := p.List(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d instances, want 1", len(got))
	}
	if got[0].SessionUUID != sessionUUID {
		t.Errorf("sessionUUID = %q, want %q", got[0].SessionUUID, sessionUUID)
	}
}

// TestList_AccountAttached: account is attached to each instance.
func TestList_AccountAttached(t *testing.T) {
	const pid = 9005
	p := New(Inputs{
		Panes: &stubPanes{
			host: "local",
			panes: []*TmuxPaneSummary{
				{PaneID: "p7", CurrentCommand: "claude", CurrentPid: pid},
			},
		},
		Ps:       &stubPs{cwds: map[int]string{pid: "/work"}},
		Liveness: &stubLiveness{alive: map[int]bool{pid: true}},
		Account: &stubAccount{acct: &Account{
			ID:    "account-1",
			Email: "user@example.com",
		}},
		Clock: fixedClock(testNow),
	})
	got, err := p.List(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d instances, want 1", len(got))
	}
	if got[0].Account == nil {
		t.Fatal("account = nil, want non-nil")
	}
	if got[0].Account.ID != "account-1" {
		t.Errorf("account.ID = %q, want %q", got[0].Account.ID, "account-1")
	}
}

// TestList_LastActivityAtFromPane: when jsonl has no timestamp, falls back to
// pane.LastActivityAt.
func TestList_LastActivityAtFromPane(t *testing.T) {
	const pid = 9006
	const paneLastActivity = "2026-05-18T11:00:00Z"
	p := New(Inputs{
		Panes: &stubPanes{
			host: "local",
			panes: []*TmuxPaneSummary{
				{
					PaneID:         "p8",
					CurrentCommand: "claude",
					CurrentPid:     pid,
					LastActivityAt: paneLastActivity,
				},
			},
		},
		Ps:       &stubPs{cwds: map[int]string{pid: "/work"}},
		Liveness: &stubLiveness{alive: map[int]bool{pid: true}},
		// No Snapshot → zero LastActivityAt from jsonl; should fall back.
		Clock: fixedClock(testNow),
	})
	got, err := p.List(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d instances, want 1", len(got))
	}
	if got[0].LastActivityAt.IsZero() {
		t.Error("LastActivityAt is zero, expected fallback from pane.LastActivityAt")
	}
	expected, _ := time.Parse(time.RFC3339, paneLastActivity)
	if !got[0].LastActivityAt.Equal(expected) {
		t.Errorf("LastActivityAt = %v, want %v", got[0].LastActivityAt, expected)
	}
}

// TestList_ModelPopulated: model field is populated from jsonl when available.
func TestList_ModelPopulated(t *testing.T) {
	const (
		pid         = 9007
		sessionUUID = "uuid-model"
		cwd         = "/repo/model"
	)
	recs := []Record{
		userRec(testNow, nil),
		{
			Timestamp: testNow.Add(1 * time.Second),
			Type:      "assistant",
			Message: &Message{
				Model:      "claude-opus-4-7",
				StopReason: "end_turn",
			},
		},
	}
	p := New(Inputs{
		Panes: &stubPanes{
			host: "local",
			panes: []*TmuxPaneSummary{
				{PaneID: "p9", CurrentCommand: "claude", CurrentPid: pid},
			},
		},
		Ps:       &stubPs{cwds: map[int]string{pid: cwd}},
		Liveness: &stubLiveness{alive: map[int]bool{pid: true}},
		Jsonls: &stubJsonls{m: map[string]ConversationSummary{
			cwd: {SessionUUID: sessionUUID, Cwd: cwd, ID: "conv-9"},
		}},
		Snapshot: &stubSnapshot{records: map[string][]Record{sessionUUID: recs}},
		Clock:    fixedClock(testNow),
	})
	got, err := p.List(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d instances, want 1", len(got))
	}
	if got[0].Model != "claude-opus-4-7" {
		t.Errorf("model = %q, want %q", got[0].Model, "claude-opus-4-7")
	}
}

// TestList_IDShape_PidKeyed: id format is "ClaudeInstance:<host>:<pid>" when pid > 0.
func TestList_IDShape_PidKeyed(t *testing.T) {
	const pid = 42
	p := New(Inputs{
		Panes: &stubPanes{
			host: "myhost",
			panes: []*TmuxPaneSummary{
				{PaneID: "pX", CurrentCommand: "claude", CurrentPid: pid},
			},
		},
		Liveness: &stubLiveness{alive: map[int]bool{pid: true}},
		Clock:    fixedClock(testNow),
	})
	got, err := p.List(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d instances, want 1", len(got))
	}
	want := "ClaudeInstance:myhost:42"
	if got[0].ID != want {
		t.Errorf("id = %q, want %q", got[0].ID, want)
	}
}

// TestList_IDShape_PaneKeyed: id format is "ClaudeInstance:<host>:pane-<paneID>"
// when pid is 0.
func TestList_IDShape_PaneKeyed(t *testing.T) {
	p := New(Inputs{
		Panes: &stubPanes{
			host: "myhost",
			panes: []*TmuxPaneSummary{
				{PaneID: "pY", CurrentCommand: "claude", CurrentPid: 0},
			},
		},
		Liveness: &stubLiveness{},
		Clock:    fixedClock(testNow),
	})
	got, err := p.List(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d instances, want 1", len(got))
	}
	want := "ClaudeInstance:myhost:pane-pY"
	if got[0].ID != want {
		t.Errorf("id = %q, want %q", got[0].ID, want)
	}
}

// TestList_MultipleInstances_SortedByID: multiple instances are returned in
// deterministic id order.
func TestList_MultipleInstances_SortedByID(t *testing.T) {
	p := New(Inputs{
		Panes: &stubPanes{
			host: "local",
			panes: []*TmuxPaneSummary{
				{PaneID: "p_z", CurrentCommand: "claude", CurrentPid: 300},
				{PaneID: "p_a", CurrentCommand: "claude", CurrentPid: 100},
				{PaneID: "p_m", CurrentCommand: "claude", CurrentPid: 200},
			},
		},
		Liveness: &stubLiveness{alive: map[int]bool{100: true, 200: true, 300: true}},
		Clock:    fixedClock(testNow),
	})
	got, err := p.List(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d instances, want 3", len(got))
	}
	for i := 1; i < len(got); i++ {
		if got[i].ID <= got[i-1].ID {
			t.Errorf("instances not sorted: got[%d].ID=%q <= got[%d].ID=%q", i, got[i].ID, i-1, got[i-1].ID)
		}
	}
}

// TestProjectInstance_NilReturnsNil: ProjectInstance(nil) returns nil.
func TestProjectInstance_NilReturnsNil(t *testing.T) {
	if ProjectInstance(nil) != nil {
		t.Error("ProjectInstance(nil) should return nil")
	}
}

// TestProjectInstance_StateString: each InstanceState maps to correct string.
func TestProjectInstance_StateString(t *testing.T) {
	cases := []struct {
		state InstanceState
		want  string
	}{
		{StateWorking, "working"},
		{StateIdle, "idle"},
		{StateInput, "input"},
		{StateStalled, "stalled"},
		{StateDead, "dead"},
		{StateNoClaude, "no_claude"},
	}
	for _, tc := range cases {
		inst := &Instance{ID: "test", State: tc.state}
		got := ProjectInstance(inst)
		if got.State != tc.want {
			t.Errorf("state %v: got %q, want %q", tc.state, got.State, tc.want)
		}
	}
}

// TestProjectInstance_InflightToolCount: InflightToolCount is projected correctly.
func TestProjectInstance_InflightToolCount(t *testing.T) {
	inst := &Instance{ID: "x", InflightToolCount: 3}
	got := ProjectInstance(inst)
	if got.InflightToolCount != 3 {
		t.Errorf("InflightToolCount = %d, want 3", got.InflightToolCount)
	}
}

// TestProjectInstance_RcEnabledDefaultFalse: RcEnabled defaults to false
// when the instance has no rc data.
func TestProjectInstance_RcEnabledDefaultFalse(t *testing.T) {
	inst := &Instance{ID: "x"}
	got := ProjectInstance(inst)
	if got.RcEnabled {
		t.Error("RcEnabled should be false when not set")
	}
}

// TestClassifyState_EmptyRecords: empty records → StateIdle with zero LastActivityAt.
// Regression for PR #606 comment r3243103664.
func TestClassifyState_EmptyRecords(t *testing.T) {
	snap := classifyState(nil, testNow)
	if snap.State != StateIdle {
		t.Errorf("state = %v, want StateIdle for nil records", snap.State)
	}
	if !snap.LastActivityAt.IsZero() {
		t.Errorf("LastActivityAt = %v, want zero (empty transcript)", snap.LastActivityAt)
	}
	snap2 := classifyState([]Record{}, testNow)
	if snap2.State != StateIdle {
		t.Errorf("state = %v, want StateIdle for empty slice", snap2.State)
	}
	if !snap2.LastActivityAt.IsZero() {
		t.Errorf("LastActivityAt = %v, want zero (empty slice)", snap2.LastActivityAt)
	}
}

// TestClassifyState_OrphanedToolUseBeforeBoundary: tool_use from a completed
// turn must not contribute to inflight count of the current turn.
func TestClassifyState_OrphanedToolUseBeforeBoundary(t *testing.T) {
	records := []Record{
		userRec(testNow, nil),
		assistantRec(testNow.Add(1*time.Second), "tool_use", []ContentItem{
			{Type: "tool_use", ID: "orphan", Name: "Bash"},
		}),
		assistantRec(testNow.Add(2*time.Second), "end_turn", nil),
		systemRec(testNow.Add(3*time.Second), "turn_duration"),
		// Current turn — clean.
		userRec(testNow.Add(4*time.Second), nil),
		assistantRec(testNow.Add(5*time.Second), "end_turn", nil),
		systemRec(testNow.Add(6*time.Second), "turn_duration"),
	}
	snap := classifyState(records, testNow)
	if snap.State != StateIdle {
		t.Errorf("state = %v, want StateIdle (orphan before boundary)", snap.State)
	}
	if snap.InflightToolCount != 0 {
		t.Errorf("inflight = %d, want 0 (orphan scoped to old turn)", snap.InflightToolCount)
	}
}

// TestDeriveState_DeadPidYieldsNoClaude: dead pid → StateNoClaude, empty snapshot.
func TestDeriveState_DeadPidYieldsNoClaude(t *testing.T) {
	state, snap := deriveState(context.Background(), DeriveState{
		Pid:      999,
		Liveness: &stubLiveness{alive: map[int]bool{999: false}},
		Clock:    fixedClock(testNow),
	})
	if state != StateNoClaude {
		t.Errorf("state = %v, want StateNoClaude", state)
	}
	if snap.InflightToolCount != 0 {
		t.Errorf("snap.InflightToolCount = %d, want 0", snap.InflightToolCount)
	}
}
