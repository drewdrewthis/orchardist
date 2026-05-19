// Package integration contains T4: cross-domain join tests at the GraphQL
// boundary for the claude-instance domain.
//
// Per T4, the join (jsonl + tmux pane + ps) is tested via the domain's
// Resolver.ClaudeInstances method — which is the GraphQL resolver surface —
// NOT at the provider boundary. This exercises the full stack: stubs that
// satisfy the ISP interfaces defined in service.go, the Provider join logic,
// the projection, and the resolver output shape.
//
// In the composed daemon these stubs would be replaced by real provider
// implementations. Here they serve as in-process fakes that respect the
// service interface contract.
package integration

import (
	"context"
	"testing"
	"time"

	claudeinstance "github.com/drewdrewthis/git-orchard-rs/daemon/claude-instance"
)

// ─── Stubs (respect service interfaces, not provider internals) ────────────────

type fakeJsonls struct {
	m map[string]claudeinstance.ConversationSummary
}

func (f *fakeJsonls) ConversationsByCwd(_ context.Context) (map[string]claudeinstance.ConversationSummary, error) {
	return f.m, nil
}

type fakePanes struct {
	host  string
	panes []*claudeinstance.TmuxPaneSummary
}

func (f *fakePanes) PanesByCommand(_ context.Context, _, _ string) ([]*claudeinstance.TmuxPaneSummary, error) {
	return f.panes, nil
}

func (f *fakePanes) Host() string { return f.host }

type fakePs struct {
	cwds map[int]string
}

func (f *fakePs) LoadCwd(_ context.Context, pid int) (string, error) {
	return f.cwds[pid], nil
}

type fakeLiveness struct {
	alive map[int]bool
}

func (f *fakeLiveness) IsAlive(pid int) bool { return f.alive[pid] }

type fakeSnapshot struct {
	data map[string][]claudeinstance.Record
}

func (f *fakeSnapshot) ReadSnapshot(_ context.Context, _, sessionUUID string) ([]claudeinstance.Record, bool) {
	r, ok := f.data[sessionUUID]
	return r, ok
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

var testNow = time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)

// newResolverWithStubs constructs a Resolver backed by in-process fakes.
// This mirrors how the composed daemon would wire real providers.
func newResolverWithStubs(inputs claudeinstance.Inputs) *claudeinstance.Resolver {
	p := claudeinstance.New(inputs)
	loaders := claudeinstance.NewLoaders(p)
	return claudeinstance.NewResolver(p, loaders)
}

// ─── T4 Tests — join tested at the GraphQL resolver boundary ──────────────────

// TestClaudeInstances_EmptyWhenNoPanes: Query.claudeInstances returns [] when
// the tmux provider reports no panes running claude.
func TestClaudeInstances_EmptyWhenNoPanes(t *testing.T) {
	r := newResolverWithStubs(claudeinstance.Inputs{
		Panes: &fakePanes{host: "local", panes: nil},
	})
	got, err := r.ClaudeInstances(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %d instances, want 0 (no panes)", len(got))
	}
}

// TestClaudeInstances_OneInstance_WorkingState: the resolver correctly derives
// StateWorking when the jsonl has an open tool_use.
// This test exercises the full three-way join at the resolver boundary.
func TestClaudeInstances_OneInstance_WorkingState(t *testing.T) {
	const (
		pid         = 1001
		sessionUUID = "uuid-integration-working"
		cwd         = "/workspace/integration"
	)

	recs := []claudeinstance.Record{
		{
			Timestamp: testNow,
			Type:      "user",
			Message:   &claudeinstance.Message{},
		},
		{
			Timestamp: testNow.Add(1 * time.Second),
			Type:      "assistant",
			Message: &claudeinstance.Message{
				StopReason: "tool_use",
				Model:      "claude-opus-4-7",
				Content: []claudeinstance.ContentItem{
					{Type: "tool_use", ID: "tu_integration", Name: "Bash"},
				},
			},
		},
	}

	r := newResolverWithStubs(claudeinstance.Inputs{
		Panes: &fakePanes{
			host: "local",
			panes: []*claudeinstance.TmuxPaneSummary{
				{PaneID: "p-integration", CurrentCommand: "claude", CurrentPid: pid},
			},
		},
		Ps:       &fakePs{cwds: map[int]string{pid: cwd}},
		Liveness: &fakeLiveness{alive: map[int]bool{pid: true}},
		Jsonls: &fakeJsonls{m: map[string]claudeinstance.ConversationSummary{
			cwd: {SessionUUID: sessionUUID, Cwd: cwd, ID: "int-conv-1"},
		}},
		Snapshot: &fakeSnapshot{data: map[string][]claudeinstance.Record{sessionUUID: recs}},
		Clock:    func() time.Time { return testNow },
	})

	got, err := r.ClaudeInstances(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d instances, want 1", len(got))
	}

	inst := got[0]

	// T4: assert GraphQL-level field projection, not provider internals.
	if inst.State != "working" {
		t.Errorf("State = %q, want %q", inst.State, "working")
	}
	if inst.InflightToolCount != 1 {
		t.Errorf("InflightToolCount = %d, want 1", inst.InflightToolCount)
	}
	if inst.Model == nil || *inst.Model != "claude-opus-4-7" {
		t.Errorf("Model = %v, want %q", inst.Model, "claude-opus-4-7")
	}
	if inst.SessionUUID == nil || *inst.SessionUUID != sessionUUID {
		t.Errorf("SessionUUID = %v, want %q", inst.SessionUUID, sessionUUID)
	}
	if inst.ID != "ClaudeInstance:local:1001" {
		t.Errorf("ID = %q, want %q", inst.ID, "ClaudeInstance:local:1001")
	}
}

// TestClaudeInstances_DeadPid_NoClaude: a pane whose pid is dead produces a
// StateNoClaude instance — not excluded from the result set. Dead instances
// are observable at the GraphQL boundary so clients can surface them.
func TestClaudeInstances_DeadPid_NoClaude(t *testing.T) {
	const pid = 2001

	r := newResolverWithStubs(claudeinstance.Inputs{
		Panes: &fakePanes{
			host: "local",
			panes: []*claudeinstance.TmuxPaneSummary{
				{PaneID: "p-dead", CurrentCommand: "claude", CurrentPid: pid},
			},
		},
		Ps:       &fakePs{cwds: map[int]string{pid: "/dead/cwd"}},
		Liveness: &fakeLiveness{alive: map[int]bool{pid: false}},
		Clock:    func() time.Time { return testNow },
	})

	got, err := r.ClaudeInstances(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d instances, want 1 (dead pane still produces an instance)", len(got))
	}
	if got[0].State != "no_claude" {
		t.Errorf("State = %q, want %q (dead pid)", got[0].State, "no_claude")
	}
}

// TestClaudeInstances_InputState_AskUserQuestion: open AskUserQuestion →
// State=input at the GraphQL boundary.
func TestClaudeInstances_InputState_AskUserQuestion(t *testing.T) {
	const (
		pid         = 3001
		sessionUUID = "uuid-input-integration"
		cwd         = "/workspace/input"
	)

	recs := []claudeinstance.Record{
		{Timestamp: testNow, Type: "user", Message: &claudeinstance.Message{}},
		{
			Timestamp: testNow.Add(1 * time.Second),
			Type:      "assistant",
			Message: &claudeinstance.Message{
				StopReason: "tool_use",
				Content:    []claudeinstance.ContentItem{{Type: "tool_use", ID: "ask_int", Name: "AskUserQuestion"}},
			},
		},
	}

	r := newResolverWithStubs(claudeinstance.Inputs{
		Panes: &fakePanes{
			host: "local",
			panes: []*claudeinstance.TmuxPaneSummary{
				{PaneID: "p-input", CurrentCommand: "claude", CurrentPid: pid},
			},
		},
		Ps:       &fakePs{cwds: map[int]string{pid: cwd}},
		Liveness: &fakeLiveness{alive: map[int]bool{pid: true}},
		Jsonls:   &fakeJsonls{m: map[string]claudeinstance.ConversationSummary{cwd: {SessionUUID: sessionUUID, Cwd: cwd, ID: "int-conv-input"}}},
		Snapshot: &fakeSnapshot{data: map[string][]claudeinstance.Record{sessionUUID: recs}},
		Clock:    func() time.Time { return testNow },
	})

	got, err := r.ClaudeInstances(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d instances, want 1", len(got))
	}
	if got[0].State != "input" {
		t.Errorf("State = %q, want %q", got[0].State, "input")
	}
}

// TestClaudeInstances_MultipleInstances_DeterministicOrder: two instances are
// returned in ascending ID order.
func TestClaudeInstances_MultipleInstances_DeterministicOrder(t *testing.T) {
	r := newResolverWithStubs(claudeinstance.Inputs{
		Panes: &fakePanes{
			host: "local",
			panes: []*claudeinstance.TmuxPaneSummary{
				{PaneID: "pB", CurrentCommand: "claude", CurrentPid: 200},
				{PaneID: "pA", CurrentCommand: "claude", CurrentPid: 100},
			},
		},
		Liveness: &fakeLiveness{alive: map[int]bool{100: true, 200: true}},
		Clock:    func() time.Time { return testNow },
	})

	got, err := r.ClaudeInstances(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d instances, want 2", len(got))
	}
	if got[0].ID >= got[1].ID {
		t.Errorf("instances not sorted: %q >= %q", got[0].ID, got[1].ID)
	}
}

// TestClaudeInstances_ConversationIDPassthrough: conversationID is carried to
// the GraphQL projection for back-edge resolution by the claude-jsonls domain.
func TestClaudeInstances_ConversationIDPassthrough(t *testing.T) {
	const (
		pid         = 4001
		sessionUUID = "uuid-conv-passthrough"
		cwd         = "/workspace/conv"
		convID      = "conversation-42"
	)

	r := newResolverWithStubs(claudeinstance.Inputs{
		Panes: &fakePanes{
			host:  "local",
			panes: []*claudeinstance.TmuxPaneSummary{{PaneID: "pC", CurrentCommand: "claude", CurrentPid: pid}},
		},
		Ps:       &fakePs{cwds: map[int]string{pid: cwd}},
		Liveness: &fakeLiveness{alive: map[int]bool{pid: true}},
		Jsonls:   &fakeJsonls{m: map[string]claudeinstance.ConversationSummary{cwd: {SessionUUID: sessionUUID, Cwd: cwd, ID: convID}}},
		Clock:    func() time.Time { return testNow },
	})

	got, err := r.ClaudeInstances(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d instances, want 1", len(got))
	}
	if got[0].ConversationID != convID {
		t.Errorf("ConversationID = %q, want %q", got[0].ConversationID, convID)
	}
}
