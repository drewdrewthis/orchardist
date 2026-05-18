// resolver_pane_test.go verifies TmuxPane field resolvers against a stubbed TmuxService (T1).
//
// T1: every typed field has a resolver test against a stubbed service.
// T3: assertions are capable of failing (no tautological asserts).
package tmux

import (
	"context"
	"testing"
)

// newPaneResolver builds a TmuxPaneResolvers with a stub service preloaded
// with the given panes.
func newPaneResolver(panes ...Pane) (*TmuxPaneResolvers, *stubService) {
	m := make(map[PaneKey]Pane, len(panes))
	for _, p := range panes {
		m[p.Key] = p
	}
	svc := &stubService{panes: m}
	r := &TmuxPaneResolvers{Svc: svc}
	return r, svc
}

// stubPsGetter implements PanePsGetter for tests.
type stubPsGetter struct {
	cwds     map[int]string
	commands map[int]string
}

func (s *stubPsGetter) CwdForPid(_ string, pid int) string     { return s.cwds[pid] }
func (s *stubPsGetter) CommandForPid(_ string, pid int) string { return s.commands[pid] }

// stubClaudeGetter implements ClaudeInstanceGetter for tests.
type stubClaudeGetter struct {
	instances map[string]*ClaudeInstanceRef // key = paneID
}

func (s *stubClaudeGetter) InstanceForPane(_ context.Context, _, paneID string, _ int) (*ClaudeInstanceRef, bool) {
	ref, ok := s.instances[paneID]
	return ref, ok
}

func testPane() Pane {
	return Pane{
		Key:            PaneKey{Host: "local", PaneID: "%42"},
		WindowKey:      WindowKey{Host: "local", Session: "work", Index: 0},
		Title:          "bash",
		CurrentCommand: "bash",
		CurrentPid:     12345,
		Width:          220,
		Height:         50,
		Dead:           false,
	}
}

func testPaneNode(p Pane) *TmuxPaneNode {
	return projectPaneNode(p)
}

func TestPaneResolver_Title(t *testing.T) {
	p := testPane()
	r, _ := newPaneResolver(p)
	obj := testPaneNode(p)

	title, err := r.Title(context.Background(), obj)
	if err != nil {
		t.Fatalf("Title: unexpected error: %v", err)
	}
	if title != "bash" {
		t.Errorf("Title = %q, want %q", title, "bash")
	}
}

func TestPaneResolver_CurrentCommand(t *testing.T) {
	p := testPane()
	r, _ := newPaneResolver(p)
	obj := testPaneNode(p)

	cmd, err := r.CurrentCommand(context.Background(), obj)
	if err != nil {
		t.Fatalf("CurrentCommand: unexpected error: %v", err)
	}
	if cmd != "bash" {
		t.Errorf("CurrentCommand = %q, want %q", cmd, "bash")
	}
}

func TestPaneResolver_CurrentPid(t *testing.T) {
	p := testPane()
	r, _ := newPaneResolver(p)
	obj := testPaneNode(p)

	pid, err := r.CurrentPid(context.Background(), obj)
	if err != nil {
		t.Fatalf("CurrentPid: unexpected error: %v", err)
	}
	if pid == nil {
		t.Fatal("CurrentPid = nil, want non-nil")
	}
	if *pid != 12345 {
		t.Errorf("CurrentPid = %d, want 12345", *pid)
	}
}

func TestPaneResolver_CurrentPid_Zero(t *testing.T) {
	p := testPane()
	p.CurrentPid = 0
	r, _ := newPaneResolver(p)
	obj := testPaneNode(p)

	pid, err := r.CurrentPid(context.Background(), obj)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pid != nil {
		t.Errorf("CurrentPid = %v, want nil for zero pid", pid)
	}
}

func TestPaneResolver_Width_Height(t *testing.T) {
	p := testPane()
	r, _ := newPaneResolver(p)
	obj := testPaneNode(p)

	w, err := r.Width(context.Background(), obj)
	if err != nil {
		t.Fatalf("Width: %v", err)
	}
	if w != 220 {
		t.Errorf("Width = %d, want 220", w)
	}

	h, err := r.Height(context.Background(), obj)
	if err != nil {
		t.Fatalf("Height: %v", err)
	}
	if h != 50 {
		t.Errorf("Height = %d, want 50", h)
	}
}

func TestPaneResolver_Dead(t *testing.T) {
	p := testPane()
	p.Dead = true
	r, _ := newPaneResolver(p)
	obj := testPaneNode(p)

	dead, err := r.Dead(context.Background(), obj)
	if err != nil {
		t.Fatalf("Dead: %v", err)
	}
	if !dead {
		t.Errorf("Dead = false, want true")
	}
}

func TestPaneResolver_Window_UsesLoader(t *testing.T) {
	p := testPane()
	w := Window{
		Key:  WindowKey{Host: "local", Session: "work", Index: 0},
		Name: "main",
	}
	m := map[PaneKey]Pane{p.Key: p}
	wm := map[WindowKey]Window{w.Key: w}
	svc := &stubService{panes: m, windows: wm}
	r := &TmuxPaneResolvers{Svc: svc}
	obj := testPaneNode(p)

	// Attach a loader to context (simulates HTTP middleware).
	loaders := NewRequestLoaders(svc)
	ctx := WithLoaders(context.Background(), loaders)

	wNode, err := r.Window(ctx, obj)
	if err != nil {
		t.Fatalf("Window: %v", err)
	}
	if wNode == nil {
		t.Fatal("Window = nil, want non-nil")
	}
	if wNode.Index != 0 {
		t.Errorf("Window.Index = %d, want 0", wNode.Index)
	}
}

func TestPaneResolver_ClaudeInstance_Present(t *testing.T) {
	p := testPane()
	ref := &ClaudeInstanceRef{
		ID:          "ClaudeInstance:local:12345",
		SessionUUID: "test-uuid",
		State:       "idle",
	}
	r := &TmuxPaneResolvers{
		Svc: &stubService{panes: map[PaneKey]Pane{p.Key: p}},
		ClaudeGetter: &stubClaudeGetter{
			instances: map[string]*ClaudeInstanceRef{"%42": ref},
		},
	}
	obj := testPaneNode(p)

	got, err := r.ClaudeInstance(context.Background(), obj)
	if err != nil {
		t.Fatalf("ClaudeInstance: %v", err)
	}
	if got == nil {
		t.Fatal("ClaudeInstance = nil, want ref")
	}
	if got.SessionUUID != "test-uuid" {
		t.Errorf("SessionUUID = %q, want test-uuid", got.SessionUUID)
	}
}

func TestPaneResolver_ClaudeInstance_Absent(t *testing.T) {
	p := testPane()
	r := &TmuxPaneResolvers{
		Svc: &stubService{panes: map[PaneKey]Pane{p.Key: p}},
		ClaudeGetter: &stubClaudeGetter{instances: map[string]*ClaudeInstanceRef{}},
	}
	obj := testPaneNode(p)

	got, err := r.ClaudeInstance(context.Background(), obj)
	if err != nil {
		t.Fatalf("ClaudeInstance: %v", err)
	}
	if got != nil {
		t.Errorf("ClaudeInstance = %+v, want nil", got)
	}
}

func TestPaneResolver_Process_Present(t *testing.T) {
	p := testPane()
	r := &TmuxPaneResolvers{
		Svc: &stubService{panes: map[PaneKey]Pane{p.Key: p}},
		PsGetter: &stubPsGetter{
			cwds:     map[int]string{12345: "/home/user/work"},
			commands: map[int]string{12345: "bash"},
		},
	}
	obj := testPaneNode(p)

	proc, err := r.Process(context.Background(), obj)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if proc == nil {
		t.Fatal("Process = nil, want ref")
	}
	if proc.PID != 12345 {
		t.Errorf("Process.PID = %d, want 12345", proc.PID)
	}
}

func TestPaneResolver_NilService(t *testing.T) {
	r := &TmuxPaneResolvers{Svc: nil}
	obj := &TmuxPaneNode{ID: "TmuxPane:local:%1", PaneID: "%1"}

	// All field resolvers must return zero values, not panic, when Svc is nil.
	if _, err := r.Title(context.Background(), obj); err != nil {
		t.Errorf("Title with nil svc: %v", err)
	}
	if _, err := r.Dead(context.Background(), obj); err != nil {
		t.Errorf("Dead with nil svc: %v", err)
	}
}

// Ensure stubService satisfies TmuxService at compile time.
var _ TmuxService = (*stubService)(nil)
