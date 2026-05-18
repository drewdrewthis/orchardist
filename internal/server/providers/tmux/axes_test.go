// axes_test.go — unit tests for the typed secondary-axis accessors
// (ADR-022 Phase 1): PaneByID, PanesByCwd, PanesByCommand, PanesBySession.
//
// Each test seeds the provider's in-memory stores once, then calls the
// accessor and asserts results — there is no N+1: a single Snapshot() read
// backs every accessor call.
package tmux

import (
	"testing"

	provider "github.com/drewdrewthis/git-orchard-rs/internal/server/adapter"
)

// stubPsGetter is a minimal PanePsGetter for unit tests.
type stubPsGetter struct {
	cwds     map[int]string
	commands map[int]string
}

func (s *stubPsGetter) CwdForPid(_ string, pid int) string     { return s.cwds[pid] }
func (s *stubPsGetter) CommandForPid(_ string, pid int) string { return s.commands[pid] }

// buildTestProvider constructs a Provider with the given panes pre-loaded into
// its store — no real tmux daemon needed.
func buildTestProvider(panes []Pane) *Provider {
	a := NewAdapter("local")
	p := New(a, nil)
	// Seed the store directly with the test panes.
	kv := make(map[PaneKey]Pane, len(panes))
	for _, pn := range panes {
		kv[pn.Key] = pn
	}
	p.panes.ReplaceAll(kv, provider.SourcePoll, panesEqual)
	return p
}

func TestPaneByID_Hit(t *testing.T) {
	pn := Pane{
		Key:       PaneKey{Host: "local", PaneID: "%10"},
		WindowKey: WindowKey{Host: "local", Session: "main", Index: 0},
	}
	p := buildTestProvider([]Pane{pn})

	got, ok := p.PaneByID("local", "%10")
	if !ok {
		t.Fatal("PaneByID: expected hit, got miss")
	}
	if got.Key.PaneID != "%10" {
		t.Errorf("PaneByID: got paneID %q, want %%10", got.Key.PaneID)
	}
}

func TestPaneByID_Miss(t *testing.T) {
	p := buildTestProvider([]Pane{})
	_, ok := p.PaneByID("local", "%99")
	if ok {
		t.Error("PaneByID: expected miss for unknown pane")
	}
}

func TestPanesBySession_ReturnsMatchingPanes(t *testing.T) {
	panes := []Pane{
		{Key: PaneKey{Host: "local", PaneID: "%1"}, WindowKey: WindowKey{Host: "local", Session: "work", Index: 0}},
		{Key: PaneKey{Host: "local", PaneID: "%2"}, WindowKey: WindowKey{Host: "local", Session: "work", Index: 0}},
		{Key: PaneKey{Host: "local", PaneID: "%3"}, WindowKey: WindowKey{Host: "local", Session: "other", Index: 0}},
	}
	p := buildTestProvider(panes)

	got := p.PanesBySession("local", "work")
	if len(got) != 2 {
		t.Errorf("PanesBySession: want 2 panes in 'work', got %d", len(got))
	}
	for _, pn := range got {
		if pn.WindowKey.Session != "work" {
			t.Errorf("PanesBySession: unexpected session %q in results", pn.WindowKey.Session)
		}
	}
}

func TestPanesBySession_EmptyWhenNoMatch(t *testing.T) {
	panes := []Pane{
		{Key: PaneKey{Host: "local", PaneID: "%1"}, WindowKey: WindowKey{Host: "local", Session: "other", Index: 0}},
	}
	p := buildTestProvider(panes)
	got := p.PanesBySession("local", "missing")
	if got == nil {
		t.Error("PanesBySession: must not return nil")
	}
	if len(got) != 0 {
		t.Errorf("PanesBySession: want 0, got %d", len(got))
	}
}

func TestPanesByCwd_ReturnsMatchingPanes(t *testing.T) {
	panes := []Pane{
		{Key: PaneKey{Host: "local", PaneID: "%1"}, CurrentPid: 100},
		{Key: PaneKey{Host: "local", PaneID: "%2"}, CurrentPid: 200},
		{Key: PaneKey{Host: "local", PaneID: "%3"}, CurrentPid: 300},
	}
	ps := &stubPsGetter{
		cwds: map[int]string{
			100: "/repos/foo",
			200: "/repos/foo/sub",
			300: "/repos/bar",
		},
	}
	p := buildTestProvider(panes)

	got := p.PanesByCwd("local", "/repos/foo", ps)
	if len(got) != 2 {
		t.Errorf("PanesByCwd: want 2 (exact + subdir), got %d", len(got))
	}
	ids := map[string]bool{}
	for _, pn := range got {
		ids[pn.Key.PaneID] = true
	}
	if !ids["%1"] || !ids["%2"] {
		t.Errorf("PanesByCwd: expected %%1 and %%2, got %v", ids)
	}
}

func TestPanesByCwd_NoPrefixFalsePositive(t *testing.T) {
	// /repos/foobar must NOT match when cwd is /repos/foo
	panes := []Pane{
		{Key: PaneKey{Host: "local", PaneID: "%1"}, CurrentPid: 100},
	}
	ps := &stubPsGetter{cwds: map[int]string{100: "/repos/foobar"}}
	p := buildTestProvider(panes)

	got := p.PanesByCwd("local", "/repos/foo", ps)
	if len(got) != 0 {
		t.Errorf("PanesByCwd: prefix false positive: got %d panes", len(got))
	}
}

func TestPanesByCommand_BasenameMatch(t *testing.T) {
	panes := []Pane{
		{Key: PaneKey{Host: "local", PaneID: "%1"}, CurrentPid: 100, CurrentCommand: "node"},
		{Key: PaneKey{Host: "local", PaneID: "%2"}, CurrentPid: 200, CurrentCommand: "vim"},
	}
	// ps reports the real command for pid 100 — a wrapper that contains "claude"
	ps := &stubPsGetter{
		commands: map[int]string{
			100: "/usr/local/bin/claude",
			200: "/usr/bin/vim",
		},
	}
	p := buildTestProvider(panes)

	got := p.PanesByCommand("local", "claude", ps)
	if len(got) != 1 {
		t.Errorf("PanesByCommand: want 1 claude pane, got %d", len(got))
	}
	if got[0].Key.PaneID != "%1" {
		t.Errorf("PanesByCommand: want %%1, got %q", got[0].Key.PaneID)
	}
}

func TestPanesByCommand_FallbackToCurrentCommand(t *testing.T) {
	// When ps returns "" for the pid, fall back to CurrentCommand.
	panes := []Pane{
		{Key: PaneKey{Host: "local", PaneID: "%5"}, CurrentPid: 500, CurrentCommand: "claude"},
	}
	ps := &stubPsGetter{commands: map[int]string{}} // ps can't resolve
	p := buildTestProvider(panes)

	got := p.PanesByCommand("local", "claude", ps)
	if len(got) != 1 {
		t.Errorf("PanesByCommand: want 1 (fallback), got %d", len(got))
	}
}

func TestPanesByCommand_CaseInsensitive(t *testing.T) {
	panes := []Pane{
		{Key: PaneKey{Host: "local", PaneID: "%7"}, CurrentPid: 700, CurrentCommand: "Claude"},
	}
	ps := &stubPsGetter{commands: map[int]string{700: "Claude"}}
	p := buildTestProvider(panes)

	got := p.PanesByCommand("local", "claude", ps)
	if len(got) != 1 {
		t.Errorf("PanesByCommand: case-insensitive match failed")
	}
}

func TestPanesByCommand_EmptyNeedle(t *testing.T) {
	panes := []Pane{
		{Key: PaneKey{Host: "local", PaneID: "%1"}, CurrentCommand: "zsh"},
	}
	p := buildTestProvider(panes)
	got := p.PanesByCommand("local", "", nil)
	if len(got) != 0 {
		t.Errorf("PanesByCommand: empty needle must return empty slice")
	}
}

// (TestPanesByCommand_SnapshotReadOnce was removed — the body only
// duplicated TestPanesByCommand_BasenameMatch's coverage (count of
// returned matches) without actually verifying snapshot-read count.
// To genuinely assert N+1-free reads we'd need a counting wrapper around
// store.Snapshot — that belongs with the broader daemon module refactor
// where the tmux service exposes typed lookups instead of full snapshots.)
