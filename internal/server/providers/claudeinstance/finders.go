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

// paneFinder implements PaneFinder via the tmuxInput narrow interface.
//
// The cmd-basename cross-check (only return panes whose foreground process is
// "claude") has been moved into the tmuxInput adapter layer
// (tmuxInputAdapter.PaneBySession in daemon/claudeinstance_wiring.go).
// paneFinder is now a pure pass-through: it delegates to the tmuxInput and
// returns whatever the adapter says. This lets the adapter iterate ALL panes
// in a session, rather than the caller rejecting the single pane the adapter
// returns — fixing the [vim, claude] multi-pane bug (issue #468).
type paneFinder struct {
	tmux tmuxInput
}

// NewPaneFinder wraps a tmuxInput provider as a PaneFinder. The adapter is
// expected to already own the cmd-basename cross-check when ps is available
// (see tmuxInputAdapter.PaneBySession in daemon/claudeinstance_wiring.go).
//
// When p is nil, a nil PaneFinder interface is returned.
func NewPaneFinder(p tmuxInput) PaneFinder {
	if p == nil {
		return nil
	}
	return &paneFinder{tmux: p}
}

// FindByPid satisfies PaneFinder. Returns (nil, false) when claudePid <= 0.
func (f *paneFinder) FindByPid(ctx context.Context, hostID string, claudePid int) (*gql.TmuxPane, bool) {
	if claudePid <= 0 {
		return nil, false
	}
	return f.tmux.PaneByPid(ctx, hostID, claudePid)
}

// FindBySession satisfies PaneFinder. Delegates to the tmuxInput adapter,
// which is expected to own the cmd-basename cross-check when ps is available.
// Returns (nil, false) when tmuxSession is empty.
func (f *paneFinder) FindBySession(ctx context.Context, hostID, tmuxSession string) (*gql.TmuxPane, bool) {
	if tmuxSession == "" {
		return nil, false
	}
	return f.tmux.PaneBySession(ctx, hostID, tmuxSession)
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
