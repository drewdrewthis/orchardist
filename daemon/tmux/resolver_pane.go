// resolver_pane.go contains field resolvers for the TmuxPane GraphQL type.
// R6: one file per GraphQL type.
// R3: all field reads go through PaneByIDLoader — no Snapshot() in field paths.
// S15b: TmuxPane.process and TmuxPane.claudeInstance are cross-domain back-edges;
//       their resolvers here call the ps/claude-instance service interfaces (R5).
//
// The #612 60s lens-load came from pane.window.session traversal calling Snapshot()
// at each hop. This file fixes that by routing every hop through a loader.
package tmux

import (
	"context"
)

// TmuxPaneResolvers holds the TmuxPane field resolver implementations.
type TmuxPaneResolvers struct {
	Svc           TmuxService
	PsGetter      PanePsGetter      // cross-domain: ps service (R5)
	ClaudeGetter  ClaudeInstanceGetter // cross-domain: claude-instance service (R5)
}

// loadPane fetches pane data via the per-request PaneByIDLoader (R3).
// The loader batches all Load calls within one request into one service call.
func (r *TmuxPaneResolvers) loadPane(ctx context.Context, obj *TmuxPaneNode) (Pane, bool) {
	if r.Svc == nil || obj == nil {
		return Pane{}, false
	}
	host, paneID, ok := ParsePaneID(obj.ID)
	if !ok {
		return Pane{}, false
	}
	key := PaneKey{Host: HostID(host), PaneID: paneID}
	if l := LoadersFromContext(ctx); l != nil {
		thunk := l.PaneByID.Load(ctx, key)
		return thunk()
	}
	// Fallback: direct service call (no loader on context).
	return r.Svc.PaneByID(host, paneID)
}

// Window resolves TmuxPane.window.
// FIXES #612: previously called Snapshot().Windows[key] per-field-call.
// Now uses WindowByKeyLoader — the window hop is batched.
func (r *TmuxPaneResolvers) Window(ctx context.Context, obj *TmuxPaneNode) (*TmuxWindowNode, error) {
	if r.Svc == nil || obj == nil {
		return nil, nil
	}
	p, ok := r.loadPane(ctx, obj)
	if !ok {
		return nil, nil
	}
	host := string(r.Svc.Host())
	key := WindowKey{Host: p.WindowKey.Host, Session: p.WindowKey.Session, Index: p.WindowKey.Index}
	if l := LoadersFromContext(ctx); l != nil {
		thunk := l.WindowByKey.Load(ctx, key)
		w, wok := thunk()
		if !wok {
			return nil, nil
		}
		return projectWindowNode(w), nil
	}
	w, wok := r.Svc.WindowByKey(host, p.WindowKey.Session, p.WindowKey.Index)
	if !wok {
		return nil, nil
	}
	return projectWindowNode(w), nil
}

// Title resolves TmuxPane.title.
func (r *TmuxPaneResolvers) Title(ctx context.Context, obj *TmuxPaneNode) (string, error) {
	p, ok := r.loadPane(ctx, obj)
	if !ok {
		return "", nil
	}
	return p.Title, nil
}

// CurrentCommand resolves TmuxPane.currentCommand.
func (r *TmuxPaneResolvers) CurrentCommand(ctx context.Context, obj *TmuxPaneNode) (string, error) {
	p, ok := r.loadPane(ctx, obj)
	if !ok {
		return "", nil
	}
	return p.CurrentCommand, nil
}

// CurrentPid resolves TmuxPane.currentPid.
func (r *TmuxPaneResolvers) CurrentPid(ctx context.Context, obj *TmuxPaneNode) (*int64, error) {
	p, ok := r.loadPane(ctx, obj)
	if !ok || p.CurrentPid == 0 {
		return nil, nil
	}
	v := int64(p.CurrentPid)
	return &v, nil
}

// Width resolves TmuxPane.width.
func (r *TmuxPaneResolvers) Width(ctx context.Context, obj *TmuxPaneNode) (int64, error) {
	p, ok := r.loadPane(ctx, obj)
	if !ok {
		return 0, nil
	}
	return int64(p.Width), nil
}

// Height resolves TmuxPane.height.
func (r *TmuxPaneResolvers) Height(ctx context.Context, obj *TmuxPaneNode) (int64, error) {
	p, ok := r.loadPane(ctx, obj)
	if !ok {
		return 0, nil
	}
	return int64(p.Height), nil
}

// Dead resolves TmuxPane.dead.
func (r *TmuxPaneResolvers) Dead(ctx context.Context, obj *TmuxPaneNode) (bool, error) {
	p, ok := r.loadPane(ctx, obj)
	if !ok {
		return false, nil
	}
	return p.Dead, nil
}

// WatchingClients resolves TmuxPane.watchingClients.
// Uses AllClients() — one cache read filtered by currentPane (R3).
func (r *TmuxPaneResolvers) WatchingClients(ctx context.Context, obj *TmuxPaneNode) ([]*TmuxClientNode, error) {
	if r.Svc == nil || obj == nil {
		return nil, nil
	}
	p, ok := r.loadPane(ctx, obj)
	if !ok {
		return []*TmuxClientNode{}, nil
	}
	all := r.Svc.AllClients()
	out := []*TmuxClientNode{}
	for _, c := range all {
		if c.CurrentPane == p.Key.PaneID {
			out = append(out, projectClientNode(c))
		}
	}
	return out, nil
}

// Process resolves TmuxPane.process — cross-domain back-edge (S15b).
// Calls PsGetter (the ps domain service interface, R5). Never triggers a
// fresh ps shellout — cache-only by contract (schema doc).
func (r *TmuxPaneResolvers) Process(ctx context.Context, obj *TmuxPaneNode) (*ProcessRef, error) {
	if r.PsGetter == nil || obj == nil {
		return nil, nil
	}
	p, ok := r.loadPane(ctx, obj)
	if !ok || p.CurrentPid == 0 {
		return nil, nil
	}
	host, _, hok := ParsePaneID(obj.ID)
	if !hok {
		return nil, nil
	}
	// The ps domain owns the Process type; we return a ref that the ps resolver
	// picks up through the cross-domain field wiring (S15b, R5).
	cwd := r.PsGetter.CwdForPid(host, p.CurrentPid)
	cmd := r.PsGetter.CommandForPid(host, p.CurrentPid)
	if cwd == "" && cmd == "" {
		return nil, nil
	}
	return &ProcessRef{
		Host: host,
		PID:  p.CurrentPid,
	}, nil
}

// ClaudeInstance resolves TmuxPane.claudeInstance — cross-domain back-edge (S15b).
// Calls ClaudeGetter (the claude-instance domain service interface, R5).
// R14: field is named claudeInstance, not chatMute (naming honesty fix).
func (r *TmuxPaneResolvers) ClaudeInstance(ctx context.Context, obj *TmuxPaneNode) (*ClaudeInstanceRef, error) {
	if r.ClaudeGetter == nil || obj == nil {
		return nil, nil
	}
	p, ok := r.loadPane(ctx, obj)
	if !ok || p.CurrentPid == 0 {
		return nil, nil
	}
	host, paneID, hok := ParsePaneID(obj.ID)
	if !hok {
		return nil, nil
	}
	ref, found := r.ClaudeGetter.InstanceForPane(ctx, host, paneID, p.CurrentPid)
	if !found {
		return nil, nil
	}
	return ref, nil
}

// Content resolves TmuxPane.content (last N lines). On-demand shellout (schema-documented).
func (r *TmuxPaneResolvers) Content(ctx context.Context, obj *TmuxPaneNode, lines *int64, stripAnsi *bool) (string, error) {
	if r.Svc == nil || obj == nil {
		return "", nil
	}
	p, ok := r.loadPane(ctx, obj)
	if !ok {
		return "", nil
	}
	n := 50
	if lines != nil {
		n = int(*lines)
	}
	strip := true
	if stripAnsi != nil {
		strip = *stripAnsi
	}
	return r.Svc.CapturePaneTail(ctx, p.Key, n, strip)
}

// ContentRange resolves TmuxPane.contentRange. On-demand shellout.
func (r *TmuxPaneResolvers) ContentRange(ctx context.Context, obj *TmuxPaneNode, startLine, endLine int64, stripAnsi *bool) (string, error) {
	if r.Svc == nil || obj == nil {
		return "", nil
	}
	p, ok := r.loadPane(ctx, obj)
	if !ok {
		return "", nil
	}
	strip := true
	if stripAnsi != nil {
		strip = *stripAnsi
	}
	return r.Svc.CapturePane(ctx, p.Key, int(startLine), int(endLine), false, strip)
}

// ContentFull resolves TmuxPane.contentFull. On-demand shellout.
func (r *TmuxPaneResolvers) ContentFull(ctx context.Context, obj *TmuxPaneNode, stripAnsi *bool) (string, error) {
	if r.Svc == nil || obj == nil {
		return "", nil
	}
	p, ok := r.loadPane(ctx, obj)
	if !ok {
		return "", nil
	}
	strip := true
	if stripAnsi != nil {
		strip = *stripAnsi
	}
	return r.Svc.CapturePane(ctx, p.Key, 0, 0, true, strip)
}

// ProcessRef is the minimal cross-domain ref the tmux domain returns for
// TmuxPane.process. The ps domain's resolver picks it up via the field wiring.
type ProcessRef struct {
	Host string
	PID  int
}
