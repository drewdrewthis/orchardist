// resolver_window.go contains field resolvers for the TmuxWindow GraphQL type.
// R6: one file per GraphQL type.
// R3: all field reads go through WindowByKeyLoader — no Snapshot() in field paths.
package tmux

import (
	"context"
)

// TmuxWindowResolvers holds the TmuxWindow field resolver implementations.
type TmuxWindowResolvers struct {
	Svc TmuxService
}

// loadWindow fetches window data for obj via the per-request loader (R3).
func (r *TmuxWindowResolvers) loadWindow(ctx context.Context, obj *TmuxWindowNode) (Window, bool) {
	if r.Svc == nil || obj == nil {
		return Window{}, false
	}
	host, session, index, ok := ParseWindowID(obj.ID)
	if !ok {
		return Window{}, false
	}
	key := WindowKey{Host: HostID(host), Session: session, Index: index}
	if l := LoadersFromContext(ctx); l != nil {
		thunk := l.WindowByKey.Load(ctx, key)
		return thunk()
	}
	return r.Svc.WindowByKey(host, session, index)
}

// Session resolves TmuxWindow.session.
// R3: reads session through SessionByNameLoader, not Snapshot().
func (r *TmuxWindowResolvers) Session(ctx context.Context, obj *TmuxWindowNode) (*TmuxSessionNode, error) {
	if r.Svc == nil || obj == nil {
		return nil, nil
	}
	w, ok := r.loadWindow(ctx, obj)
	if !ok {
		return nil, nil
	}
	host := string(r.Svc.Host())
	key := SessionKey{Host: HostID(host), Name: w.Key.Session}
	if l := LoadersFromContext(ctx); l != nil {
		thunk := l.SessionByName.Load(ctx, key)
		s, sok := thunk()
		if !sok {
			return nil, nil
		}
		return projectSessionNode(s), nil
	}
	s, sok := r.Svc.SessionByName(host, w.Key.Session)
	if !sok {
		return nil, nil
	}
	return projectSessionNode(s), nil
}

// Name resolves TmuxWindow.name.
func (r *TmuxWindowResolvers) Name(ctx context.Context, obj *TmuxWindowNode) (string, error) {
	w, ok := r.loadWindow(ctx, obj)
	if !ok {
		return "", nil
	}
	return w.Name, nil
}

// Active resolves TmuxWindow.active.
func (r *TmuxWindowResolvers) Active(ctx context.Context, obj *TmuxWindowNode) (bool, error) {
	w, ok := r.loadWindow(ctx, obj)
	if !ok {
		return false, nil
	}
	return w.Active, nil
}

// Panes resolves TmuxWindow.panes.
// Uses AllPanes() with in-Go filter — one cache read, no N+1 (R3).
func (r *TmuxWindowResolvers) Panes(ctx context.Context, obj *TmuxWindowNode) ([]*TmuxPaneNode, error) {
	if r.Svc == nil || obj == nil {
		return nil, nil
	}
	w, ok := r.loadWindow(ctx, obj)
	if !ok {
		return nil, nil
	}
	all := r.Svc.AllPanes()
	out := []*TmuxPaneNode{}
	for _, p := range all {
		if p.WindowKey.Session == w.Key.Session && p.WindowKey.Index == w.Key.Index {
			out = append(out, projectPaneNode(p))
		}
	}
	return out, nil
}

// CurrentPane resolves TmuxWindow.currentPane.
// Uses PaneByIDLoader (R3).
func (r *TmuxWindowResolvers) CurrentPane(ctx context.Context, obj *TmuxWindowNode) (*TmuxPaneNode, error) {
	if r.Svc == nil || obj == nil {
		return nil, nil
	}
	w, ok := r.loadWindow(ctx, obj)
	if !ok || w.CurrentPane == "" {
		return nil, nil
	}
	host := string(r.Svc.Host())
	key := PaneKey{Host: HostID(host), PaneID: w.CurrentPane}
	if l := LoadersFromContext(ctx); l != nil {
		thunk := l.PaneByID.Load(ctx, key)
		p, pok := thunk()
		if !pok {
			return nil, nil
		}
		return projectPaneNode(p), nil
	}
	p, pok := r.Svc.PaneByID(host, w.CurrentPane)
	if !pok {
		return nil, nil
	}
	return projectPaneNode(p), nil
}
