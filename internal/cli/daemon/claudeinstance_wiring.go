// Package daemon — claudeinstance_wiring.go
//
// Thin projection adapters that bridge the concrete provider types
// (*ps.Provider, *tmux.Provider, *claudeaccount.Provider) to the narrow
// interfaces (psInput, tmuxInput, acctInput) that the claudeinstance
// package's NewProcessFinder / NewPaneFinder / NewAccountFinder consume.
//
// These adapters live here rather than in the claudeinstance package to
// avoid an import cycle: claudeinstance → ps/tmux/claudeaccount → (nothing),
// but daemon → all three is fine. The narrow interfaces in claudeinstance
// are satisfied structurally (Go duck typing) — no explicit "implements"
// declaration is required.
//
// The projection from ps.Process → graphql.Process is a copy of
// internal/server/resolvers/loader_bridge.go:projectProcessFromCache.
// Both copies MUST be kept in sync; a comment in loader_bridge.go says so.
package daemon

import (
	"context"
	"path/filepath"
	"strings"
	"time"

	gql "github.com/drewdrewthis/git-orchard-rs/internal/server/graphql"
	claudeaccountprovider "github.com/drewdrewthis/git-orchard-rs/internal/server/providers/claudeaccount"
	psprovider "github.com/drewdrewthis/git-orchard-rs/internal/server/providers/ps"
	tmuxprovider "github.com/drewdrewthis/git-orchard-rs/internal/server/providers/tmux"
)

// ---------------------------------------------------------------------------
// psInputAdapter — bridges *psprovider.Provider to psInput.
// ---------------------------------------------------------------------------

// psInputAdapter wraps the ps provider to implement the narrow psInput
// interface that claudeinstance.NewProcessFinder and claudeinstance.NewPaneFinder
// consume.
type psInputAdapter struct {
	p *psprovider.Provider
}

// GetByPid returns the projected *gql.Process for the given pid on hostID, or
// (nil, false) when the pid is not in the ps cache.
//
// The projection mirrors loader_bridge.go:projectProcessFromCache — both copies
// MUST be kept in sync.
func (a *psInputAdapter) GetByPid(ctx context.Context, hostID string, pid int) (*gql.Process, bool) {
	if a.p == nil || pid <= 0 {
		return nil, false
	}
	proc, _, err := a.p.Get(ctx, psprovider.ProcessID{Host: hostID, PID: pid})
	if err != nil {
		return nil, false
	}
	return projectProcess(&proc, hostID), true
}

// projectProcess projects a psprovider.Process onto *gql.Process.
// Mirrors internal/server/resolvers/loader_bridge.go:projectProcessFromCache.
func projectProcess(p *psprovider.Process, hostID string) *gql.Process {
	out := &gql.Process{
		ID:         p.ID.String(),
		Host:       &gql.Host{ID: hostID},
		Pid:        int64(p.ID.PID),
		Ppid:       int64(p.PPID),
		Command:    p.Command,
		StartedAt:  p.StartedRaw,
		CPUPercent: p.CPUPercent,
		MemBytes:   p.MemBytes,
	}
	if !p.StartedAt.IsZero() {
		out.StartedAt = p.StartedAt.Format(time.RFC3339)
	}
	if p.TTY != "" {
		tty := p.TTY
		out.Tty = &tty
	}
	return out
}

// ---------------------------------------------------------------------------
// Internal narrow interfaces for testability.
// ---------------------------------------------------------------------------

// tmuxSnapshotter is the narrow surface tmuxInputAdapter reads from the tmux
// provider. *tmuxprovider.Provider satisfies this via its Snapshot() method.
// Test stubs implement it inline.
type tmuxSnapshotter interface {
	Snapshot() tmuxprovider.RuntimeSnapshot
}

// psGetter is the narrow surface tmuxInputAdapter needs from the ps provider:
// just the command string for a given pid. Test stubs implement it inline.
type psGetter interface {
	// CommandForPid returns the command basename for the given pid on hostID, or
	// ("", false) when the pid is not in cache.
	CommandForPid(ctx context.Context, hostID string, pid int) (string, bool)
}

// ---------------------------------------------------------------------------
// tmuxInputAdapter — bridges *tmuxprovider.Provider to tmuxInput.
// ---------------------------------------------------------------------------

// tmuxInputAdapter wraps the tmux provider to implement the narrow tmuxInput
// interface that claudeinstance.NewPaneFinder consumes.
//
// When ps is non-nil, PaneBySession iterates ALL panes in the session and
// returns the first one whose foreground process has a Command basename
// containing "claude". This prevents the bug where a vim pane is returned
// first (because it has a non-zero pid) and the caller never sees the claude
// pane in the same session.
//
// When ps is nil (tests, no-ps mode), PaneBySession falls back to returning
// the first pane with a non-zero currentPid — v1 behaviour.
type tmuxInputAdapter struct {
	p  tmuxSnapshotter
	ps psGetter
}

// newTmuxInputAdapter constructs a tmuxInputAdapter with an optional ps
// provider for command-basename cross-checking in PaneBySession.
func newTmuxInputAdapter(p *tmuxprovider.Provider, ps *psprovider.Provider) *tmuxInputAdapter {
	a := &tmuxInputAdapter{p: p}
	if ps != nil {
		a.ps = &psGetterAdapter{p: ps}
	}
	return a
}

// PaneByPid walks the tmux pane snapshot and returns the first pane whose
// foreground pid matches, or (nil, false) when no match is found.
func (a *tmuxInputAdapter) PaneByPid(ctx context.Context, hostID string, pid int) (*gql.TmuxPane, bool) {
	if a.p == nil || pid <= 0 {
		return nil, false
	}
	for _, pn := range a.p.Snapshot().Panes {
		if string(pn.Key.Host) == hostID && pn.CurrentPid == pid {
			return projectPane(pn), true
		}
	}
	return nil, false
}

// PaneBySession walks the tmux pane snapshot and returns the first pane in the
// named session that is owned by a claude process.
//
// When a.ps is non-nil, every candidate pane's foreground pid is looked up in
// the ps snapshot; only panes whose process Command basename contains "claude"
// are eligible. This ensures that a session containing [vim, claude] returns
// the claude pane even when vim is iterated first.
//
// When a.ps is nil (tests, no-ps mode), the first pane with a non-zero
// currentPid is returned — v1 behaviour sufficient when ps data is unavailable.
func (a *tmuxInputAdapter) PaneBySession(ctx context.Context, hostID, session string) (*gql.TmuxPane, bool) {
	if a.p == nil || session == "" {
		return nil, false
	}
	for _, pn := range a.p.Snapshot().Panes {
		if string(pn.Key.Host) != hostID || pn.WindowKey.Session != session {
			continue
		}
		if pn.CurrentPid <= 0 {
			continue
		}
		if a.ps == nil {
			// No ps provider: return first pane with a non-zero pid (v1 behaviour).
			return projectPane(pn), true
		}
		// ps cross-check: only return the pane if its foreground process Command
		// basename contains "claude".
		cmd, ok := a.ps.CommandForPid(ctx, hostID, pn.CurrentPid)
		if !ok {
			continue
		}
		if strings.Contains(strings.ToLower(filepath.Base(cmd)), "claude") {
			return projectPane(pn), true
		}
	}
	return nil, false
}

// psGetterAdapter wraps *psprovider.Provider to satisfy the psGetter interface,
// bridging the concrete provider type to the narrow interface tmuxInputAdapter
// needs for testability.
type psGetterAdapter struct {
	p *psprovider.Provider
}

// CommandForPid returns the command basename for the given pid on hostID. It
// wraps ps.Provider.Get and projects only the Command field. Returns ("", false)
// when the pid is not in cache or the provider errors.
func (a *psGetterAdapter) CommandForPid(ctx context.Context, hostID string, pid int) (string, bool) {
	if a.p == nil || pid <= 0 {
		return "", false
	}
	proc, _, err := a.p.Get(ctx, psprovider.ProcessID{Host: hostID, PID: pid})
	if err != nil {
		return "", false
	}
	return proc.Command, true
}

// projectPane projects a tmuxprovider.Pane onto *gql.TmuxPane.
func projectPane(pn tmuxprovider.Pane) *gql.TmuxPane {
	out := &gql.TmuxPane{
		ID:             "TmuxPane:" + string(pn.Key.Host) + ":" + pn.Key.PaneID,
		PaneID:         pn.Key.PaneID,
		CurrentCommand: pn.CurrentCommand,
	}
	if pn.CurrentPid > 0 {
		pid := int64(pn.CurrentPid)
		out.CurrentPid = &pid
	}
	return out
}

// ---------------------------------------------------------------------------
// acctInputAdapter — bridges *claudeaccountprovider.Provider to acctInput.
// ---------------------------------------------------------------------------

// acctInputAdapter wraps the claudeaccount provider to implement the narrow
// acctInput interface that claudeinstance.NewAccountFinder consumes.
type acctInputAdapter struct {
	p *claudeaccountprovider.Provider
}

// ActiveAccount returns the active Claude CLI account for the given host, or
// (nil, false) when no account is authenticated or the provider errors.
//
// v1 returns only the first account; the briefing specifies one account per
// host for this release (ADR-011 §5.1).
func (a *acctInputAdapter) ActiveAccount(ctx context.Context, hostID string) (*gql.ClaudeAccount, bool) {
	if a.p == nil {
		return nil, false
	}
	accounts, err := a.p.List(ctx)
	if err != nil || len(accounts) == 0 {
		return nil, false
	}
	return a.p.ToGraphQL(accounts[0]), true
}
