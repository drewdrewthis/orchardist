package claudeinstance

// finders_test.go — unit tests for the production adapter constructors in
// finders.go: NewProcessFinder, NewPaneFinder, NewAccountFinder.
//
// Each adapter is tested for: happy path, not-found, nil-provider path.
//
// Tests live in package claudeinstance (not claudeinstance_test) so they
// can construct the unexported input-interface stubs without naming them
// externally — Go structural typing handles the rest.

import (
	"context"
	"testing"

	gql "github.com/drewdrewthis/git-orchard-rs/internal/server/graphql"
)

// ---------------------------------------------------------------------------
// Minimal stubs for the narrow input interfaces.
// Named with "finder" prefix to avoid collisions with the
// fakePaneFinder/fakeProcessFinder/fakeAccountFinder types in composer_test.go.
// ---------------------------------------------------------------------------

// finderPsStub implements psInput. Returns the injected process for matching
// pids; (nil, false) for misses.
type finderPsStub struct {
	byPid map[int]*gql.Process
}

func (s *finderPsStub) GetByPid(_ context.Context, _ string, pid int) (*gql.Process, bool) {
	p, ok := s.byPid[pid]
	return p, ok
}

// finderTmuxStub implements tmuxInput.
type finderTmuxStub struct {
	byPid     map[int]*gql.TmuxPane
	bySession map[string]*gql.TmuxPane
}

func (s *finderTmuxStub) PaneByPid(_ context.Context, _ string, pid int) (*gql.TmuxPane, bool) {
	pane, ok := s.byPid[pid]
	return pane, ok
}

func (s *finderTmuxStub) PaneBySession(_ context.Context, _, session string) (*gql.TmuxPane, bool) {
	pane, ok := s.bySession[session]
	return pane, ok
}

// finderAcctStub implements acctInput.
type finderAcctStub struct {
	account *gql.ClaudeAccount
}

func (s *finderAcctStub) ActiveAccount(_ context.Context, _ string) (*gql.ClaudeAccount, bool) {
	if s.account == nil {
		return nil, false
	}
	return s.account, true
}

// ---------------------------------------------------------------------------
// processFinder tests
// ---------------------------------------------------------------------------

func TestNewProcessFinder_NilProvider_ReturnsNil(t *testing.T) {
	f := NewProcessFinder(nil)
	if f != nil {
		t.Error("expected nil ProcessFinder when provider is nil, got non-nil")
	}
}

func TestProcessFinder_FindByPid_HappyPath(t *testing.T) {
	proc := &gql.Process{ID: "Process:local:1234", Pid: 1234, Command: "claude"}
	stub := &finderPsStub{byPid: map[int]*gql.Process{1234: proc}}
	f := NewProcessFinder(stub)

	got, ok := f.FindByPid(context.Background(), "local", 1234)
	if !ok {
		t.Fatal("FindByPid: expected ok=true, got false")
	}
	if got != proc {
		t.Errorf("FindByPid: got %v, want %v", got, proc)
	}
}

func TestProcessFinder_FindByPid_NotFound(t *testing.T) {
	stub := &finderPsStub{byPid: map[int]*gql.Process{}} // empty
	f := NewProcessFinder(stub)

	got, ok := f.FindByPid(context.Background(), "local", 9999)
	if ok {
		t.Error("FindByPid: expected ok=false for unknown pid, got true")
	}
	if got != nil {
		t.Errorf("FindByPid: expected nil result, got %v", got)
	}
}

func TestProcessFinder_FindByPid_ZeroPid_ReturnsFalse(t *testing.T) {
	// pid <= 0 must return (nil, false) without consulting the backend.
	stub := &finderPsStub{byPid: map[int]*gql.Process{0: {ID: "should-not-be-returned"}}}
	f := NewProcessFinder(stub)

	got, ok := f.FindByPid(context.Background(), "local", 0)
	if ok {
		t.Error("FindByPid(pid=0): expected ok=false")
	}
	if got != nil {
		t.Errorf("FindByPid(pid=0): expected nil result, got %v", got)
	}
}

// ---------------------------------------------------------------------------
// paneFinder tests
// ---------------------------------------------------------------------------

func TestNewPaneFinder_NilProvider_ReturnsNil(t *testing.T) {
	f := NewPaneFinder(nil)
	if f != nil {
		t.Error("expected nil PaneFinder when provider is nil, got non-nil")
	}
}

func TestPaneFinder_FindByPid_HappyPath(t *testing.T) {
	pid := int64(88631)
	pane := &gql.TmuxPane{ID: "TmuxPane:local:%59", CurrentPid: &pid}
	stub := &finderTmuxStub{byPid: map[int]*gql.TmuxPane{88631: pane}}
	f := NewPaneFinder(stub)

	got, ok := f.FindByPid(context.Background(), "local", 88631)
	if !ok {
		t.Fatal("FindByPid: expected ok=true, got false")
	}
	if got != pane {
		t.Errorf("FindByPid: got %v, want %v", got, pane)
	}
}

func TestPaneFinder_FindByPid_NotFound(t *testing.T) {
	stub := &finderTmuxStub{byPid: map[int]*gql.TmuxPane{}}
	f := NewPaneFinder(stub)

	got, ok := f.FindByPid(context.Background(), "local", 99999)
	if ok {
		t.Error("FindByPid: expected ok=false for unknown pid, got true")
	}
	if got != nil {
		t.Errorf("FindByPid: expected nil result, got %v", got)
	}
}

func TestPaneFinder_FindByPid_ZeroPid_ReturnsFalse(t *testing.T) {
	stub := &finderTmuxStub{byPid: map[int]*gql.TmuxPane{0: {ID: "should-not-be-returned"}}}
	f := NewPaneFinder(stub)

	got, ok := f.FindByPid(context.Background(), "local", 0)
	if ok {
		t.Error("FindByPid(pid=0): expected ok=false")
	}
	if got != nil {
		t.Errorf("FindByPid(pid=0): expected nil result, got %v", got)
	}
}

func TestPaneFinder_FindBySession_HappyPath(t *testing.T) {
	pid := int64(88631)
	pane := &gql.TmuxPane{ID: "TmuxPane:local:%59", CurrentPid: &pid}
	stub := &finderTmuxStub{bySession: map[string]*gql.TmuxPane{"alpha": pane}}
	f := NewPaneFinder(stub)

	got, ok := f.FindBySession(context.Background(), "local", "alpha")
	if !ok {
		t.Fatal("FindBySession: expected ok=true, got false")
	}
	if got != pane {
		t.Errorf("FindBySession: got %v, want %v", got, pane)
	}
}

func TestPaneFinder_FindBySession_NotFound(t *testing.T) {
	stub := &finderTmuxStub{bySession: map[string]*gql.TmuxPane{}}
	f := NewPaneFinder(stub)

	got, ok := f.FindBySession(context.Background(), "local", "nosuchsession")
	if ok {
		t.Error("FindBySession: expected ok=false for unknown session, got true")
	}
	if got != nil {
		t.Errorf("FindBySession: expected nil result, got %v", got)
	}
}

func TestPaneFinder_FindBySession_EmptySession_ReturnsFalse(t *testing.T) {
	stub := &finderTmuxStub{bySession: map[string]*gql.TmuxPane{"": {ID: "should-not-be-returned"}}}
	f := NewPaneFinder(stub)

	got, ok := f.FindBySession(context.Background(), "local", "")
	if ok {
		t.Error("FindBySession(session=''): expected ok=false")
	}
	if got != nil {
		t.Errorf("FindBySession(session=''): expected nil result, got %v", got)
	}
}

func TestPaneFinder_FindBySession_WithPsCrossCheck_ClaudeProcess(t *testing.T) {
	// When ps is wired and the pane's foreground pid resolves to a process
	// whose Command contains "claude", the pane should be returned.
	pid := int64(42)
	pane := &gql.TmuxPane{ID: "TmuxPane:local:%1", CurrentPid: &pid}
	tmuxStub := &finderTmuxStub{bySession: map[string]*gql.TmuxPane{"alpha": pane}}
	psStub := &finderPsStub{byPid: map[int]*gql.Process{42: {Command: "claude"}}}

	f := NewPaneFinder(tmuxStub, psStub)
	got, ok := f.FindBySession(context.Background(), "local", "alpha")
	if !ok {
		t.Fatal("FindBySession with claude ps: expected ok=true")
	}
	if got != pane {
		t.Errorf("FindBySession: got %v, want %v", got, pane)
	}
}

func TestPaneFinder_FindBySession_WithPsCrossCheck_NonClaudeProcess(t *testing.T) {
	// When ps is wired and the pane's foreground pid resolves to a non-claude
	// process, the pane should NOT be returned.
	pid := int64(42)
	pane := &gql.TmuxPane{ID: "TmuxPane:local:%1", CurrentPid: &pid}
	tmuxStub := &finderTmuxStub{bySession: map[string]*gql.TmuxPane{"alpha": pane}}
	psStub := &finderPsStub{byPid: map[int]*gql.Process{42: {Command: "zsh"}}}

	f := NewPaneFinder(tmuxStub, psStub)
	got, ok := f.FindBySession(context.Background(), "local", "alpha")
	if ok {
		t.Error("FindBySession with non-claude ps: expected ok=false")
	}
	if got != nil {
		t.Errorf("FindBySession with non-claude ps: expected nil, got %v", got)
	}
}

// ---------------------------------------------------------------------------
// accountFinder tests
// ---------------------------------------------------------------------------

func TestNewAccountFinder_NilProvider_ReturnsNil(t *testing.T) {
	f := NewAccountFinder(nil)
	if f != nil {
		t.Error("expected nil AccountFinder when provider is nil, got non-nil")
	}
}

func TestAccountFinder_Active_HappyPath(t *testing.T) {
	acct := &gql.ClaudeAccount{ID: "ClaudeAccount:local:dev@example.com"}
	stub := &finderAcctStub{account: acct}
	f := NewAccountFinder(stub)

	got, ok := f.Active(context.Background(), "local")
	if !ok {
		t.Fatal("Active: expected ok=true, got false")
	}
	if got != acct {
		t.Errorf("Active: got %v, want %v", got, acct)
	}
}

func TestAccountFinder_Active_NotFound(t *testing.T) {
	stub := &finderAcctStub{account: nil} // no account configured
	f := NewAccountFinder(stub)

	got, ok := f.Active(context.Background(), "local")
	if ok {
		t.Error("Active: expected ok=false when no account, got true")
	}
	if got != nil {
		t.Errorf("Active: expected nil result, got %v", got)
	}
}
