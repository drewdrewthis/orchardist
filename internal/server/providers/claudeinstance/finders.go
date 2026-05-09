package claudeinstance

// finders.go — production adapters that bridge the sibling provider packages
// (ps, tmux, claudeaccount) to the narrow interfaces (ProcessFinder,
// PaneFinder, AccountFinder) that Composer consumes.
//
// Each adapter is a thin projection layer. The canonical projection shape for
// ps.Process → graphql.Process lives in
// internal/server/resolvers/loader_bridge.go:projectProcessFromCache — the
// inline duplication below is intentional to avoid a resolvers → claudeinstance
// import cycle. Both copies MUST be kept in sync.
//
// Constructors follow a nil-safe pattern: when the underlying provider is nil
// the constructor returns a nil interface value (not a typed-nil wrapper), so
// the Composer's "if c.procs == nil" guards remain reliable.

import (
	"context"
	"path/filepath"
	"strings"

	gql "github.com/drewdrewthis/git-orchard-rs/internal/server/graphql"
)

// ---------------------------------------------------------------------------
// Narrow input interfaces — the seam between production providers and tests.
// ---------------------------------------------------------------------------

// psInput is the narrow read surface NewProcessFinder consumes. The
// production *psprovider.Provider does not implement this directly; daemon
// wiring supplies a wrapping adapter. Test stubs implement it inline.
type psInput interface {
	// GetByPid returns the projected graphql.Process for the given pid on
	// hostID, or (nil, false) when the pid is not in cache.
	GetByPid(ctx context.Context, hostID string, pid int) (*gql.Process, bool)
}

// tmuxInput is the narrow read surface NewPaneFinder consumes.
type tmuxInput interface {
	// PaneByPid returns the pane whose foreground pid matches, or (nil, false).
	PaneByPid(ctx context.Context, hostID string, pid int) (*gql.TmuxPane, bool)
	// PaneBySession returns the first suitable pane in the named tmux session,
	// or (nil, false) when the session is absent or has no candidate panes.
	PaneBySession(ctx context.Context, hostID, session string) (*gql.TmuxPane, bool)
}

// acctInput is the narrow read surface NewAccountFinder consumes.
type acctInput interface {
	// ActiveAccount returns the active Claude account for hostID, or (nil,
	// false) when no account is authenticated.
	ActiveAccount(ctx context.Context, hostID string) (*gql.ClaudeAccount, bool)
}

// ---------------------------------------------------------------------------
// processFinder
// ---------------------------------------------------------------------------

// processFinder implements ProcessFinder via the psInput narrow interface.
type processFinder struct {
	ps psInput
}

// NewProcessFinder wraps a psInput provider as a ProcessFinder. When p is nil
// the function returns a nil ProcessFinder interface — Composer's nil guard
// handles that cleanly.
//
// For daemon wiring, supply a *psprovider.Provider wrapped in a thin adapter
// that projects ps.Process to *graphql.Process. Test stubs implement psInput
// directly.
func NewProcessFinder(p psInput) ProcessFinder {
	if p == nil {
		return nil
	}
	return &processFinder{ps: p}
}

// FindByPid satisfies ProcessFinder. Delegates to the injected psInput, which
// must perform the projection from the raw provider type. Returns (nil, false)
// on miss or when pid <= 0.
func (f *processFinder) FindByPid(ctx context.Context, hostID string, pid int) (*gql.Process, bool) {
	if pid <= 0 {
		return nil, false
	}
	return f.ps.GetByPid(ctx, hostID, pid)
}

// ---------------------------------------------------------------------------
// paneFinder
// ---------------------------------------------------------------------------

// paneFinder implements PaneFinder via the tmuxInput (and optional psInput)
// narrow interfaces.
type paneFinder struct {
	tmux tmuxInput
	ps   psInput // optional: when non-nil, FindBySession cross-checks cmd contains "claude"
}

// NewPaneFinder wraps a tmuxInput provider as a PaneFinder. The optional ps
// parameter enables cmd-basename cross-checking in FindBySession: when wired,
// only panes whose currentPid resolves to a process whose Command basename
// contains "claude" are returned.
//
// When ps is nil the cmd check is skipped and the first pane with a non-zero
// currentPid is returned (v1 behaviour sufficient for AC #5 and AC #6).
//
// NOTE: feature file scenario line 64 expects cmd cross-checking. Wire ps when
// available to honour that scenario.
//
// When p is nil, a nil PaneFinder interface is returned.
func NewPaneFinder(p tmuxInput, ps ...psInput) PaneFinder {
	if p == nil {
		return nil
	}
	pf := &paneFinder{tmux: p}
	if len(ps) > 0 {
		pf.ps = ps[0]
	}
	return pf
}

// FindByPid satisfies PaneFinder. Returns (nil, false) when claudePid <= 0.
func (f *paneFinder) FindByPid(ctx context.Context, hostID string, claudePid int) (*gql.TmuxPane, bool) {
	if claudePid <= 0 {
		return nil, false
	}
	return f.tmux.PaneByPid(ctx, hostID, claudePid)
}

// FindBySession satisfies PaneFinder. Returns the first pane in the named tmux
// session. When the paneFinder was constructed with a psInput, only panes whose
// foreground process has a Command basename containing "claude" are eligible.
// Returns (nil, false) when tmuxSession is empty.
func (f *paneFinder) FindBySession(ctx context.Context, hostID, tmuxSession string) (*gql.TmuxPane, bool) {
	if tmuxSession == "" {
		return nil, false
	}
	pane, ok := f.tmux.PaneBySession(ctx, hostID, tmuxSession)
	if !ok || pane == nil {
		return nil, false
	}
	if f.ps == nil || pane.CurrentPid == nil || *pane.CurrentPid <= 0 {
		return pane, true
	}
	// ps cross-check: only return the pane if its foreground process looks
	// like a claude process (Command basename contains "claude").
	proc, found := f.ps.GetByPid(ctx, hostID, int(*pane.CurrentPid))
	if !found || proc == nil {
		return nil, false
	}
	if !strings.Contains(strings.ToLower(filepath.Base(proc.Command)), "claude") {
		return nil, false
	}
	return pane, true
}

// ---------------------------------------------------------------------------
// accountFinder
// ---------------------------------------------------------------------------

// accountFinder implements AccountFinder via the acctInput narrow interface.
type accountFinder struct {
	acct acctInput
}

// NewAccountFinder wraps an acctInput provider as an AccountFinder. When p is
// nil the function returns a nil AccountFinder interface.
func NewAccountFinder(p acctInput) AccountFinder {
	if p == nil {
		return nil
	}
	return &accountFinder{acct: p}
}

// Active satisfies AccountFinder. Returns the first active account, or (nil,
// false) when none is available.
func (f *accountFinder) Active(ctx context.Context, hostID string) (*gql.ClaudeAccount, bool) {
	return f.acct.ActiveAccount(ctx, hostID)
}
