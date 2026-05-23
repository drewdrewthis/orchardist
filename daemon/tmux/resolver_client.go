// resolver_client.go contains field resolvers for the TmuxClient GraphQL type.
// R6: one file per GraphQL type.
// R3: all field reads use ClientByName — no Snapshot() in field paths.
package tmux

import (
	"context"
)

// TmuxClientResolvers holds the TmuxClient field resolver implementations.
type TmuxClientResolvers struct {
	Svc TmuxService
}

// loadClient fetches client data by parsing the node ID and calling the service (R3).
func (r *TmuxClientResolvers) loadClient(ctx context.Context, obj *TmuxClientNode) (Client, bool) {
	if r.Svc == nil || obj == nil {
		return Client{}, false
	}
	host, name, ok := ParseClientID(obj.ID)
	if !ok {
		return Client{}, false
	}
	return r.Svc.ClientByName(host, name)
}

// Server resolves TmuxClient.server.
func (r *TmuxClientResolvers) Server(ctx context.Context, obj *TmuxClientNode) (*TmuxServerNode, error) {
	if r.Svc == nil {
		return nil, nil
	}
	return projectServerNode(r.Svc.Host(), r.Svc.Server()), nil
}

// Session resolves TmuxClient.session.
// Uses SessionByNameLoader (R3).
func (r *TmuxClientResolvers) Session(ctx context.Context, obj *TmuxClientNode) (*TmuxSessionNode, error) {
	if r.Svc == nil || obj == nil {
		return nil, nil
	}
	c, ok := r.loadClient(ctx, obj)
	if !ok {
		return nil, nil
	}
	host := string(r.Svc.Host())
	key := SessionKey{Host: HostID(host), Name: c.Session}
	if l := LoadersFromContext(ctx); l != nil {
		thunk := l.SessionByName.Load(ctx, key)
		s, sok := thunk()
		if !sok {
			return nil, nil
		}
		return projectSessionNode(s), nil
	}
	s, sok := r.Svc.SessionByName(host, c.Session)
	if !sok {
		return nil, nil
	}
	return projectSessionNode(s), nil
}

// Tty resolves TmuxClient.tty.
func (r *TmuxClientResolvers) Tty(ctx context.Context, obj *TmuxClientNode) (string, error) {
	c, ok := r.loadClient(ctx, obj)
	if !ok {
		return "", nil
	}
	return c.TTY, nil
}

// Hostname resolves TmuxClient.hostname.
func (r *TmuxClientResolvers) Hostname(ctx context.Context, obj *TmuxClientNode) (string, error) {
	c, ok := r.loadClient(ctx, obj)
	if !ok {
		return "", nil
	}
	return c.Hostname, nil
}

// TermName resolves TmuxClient.termName.
func (r *TmuxClientResolvers) TermName(ctx context.Context, obj *TmuxClientNode) (string, error) {
	c, ok := r.loadClient(ctx, obj)
	if !ok {
		return "", nil
	}
	return c.TermName, nil
}

// AttachedAt resolves TmuxClient.attachedAt.
func (r *TmuxClientResolvers) AttachedAt(ctx context.Context, obj *TmuxClientNode) (string, error) {
	c, ok := r.loadClient(ctx, obj)
	if !ok {
		return "", nil
	}
	return formatRFC3339OrEmpty(c.AttachedAt), nil
}

// LastActivityAt resolves TmuxClient.lastActivityAt.
func (r *TmuxClientResolvers) LastActivityAt(ctx context.Context, obj *TmuxClientNode) (*string, error) {
	c, ok := r.loadClient(ctx, obj)
	if !ok {
		return nil, nil
	}
	return formatRFC3339OrNil(c.LastActivityAt), nil
}

// Readonly resolves TmuxClient.readonly.
func (r *TmuxClientResolvers) Readonly(ctx context.Context, obj *TmuxClientNode) (bool, error) {
	c, ok := r.loadClient(ctx, obj)
	if !ok {
		return false, nil
	}
	return c.Readonly, nil
}

// CurrentWindow resolves TmuxClient.currentWindow.
// Uses WindowByKeyLoader (R3).
func (r *TmuxClientResolvers) CurrentWindow(ctx context.Context, obj *TmuxClientNode) (*TmuxWindowNode, error) {
	if r.Svc == nil || obj == nil {
		return nil, nil
	}
	c, ok := r.loadClient(ctx, obj)
	if !ok || c.CurrentWindow < 0 {
		return nil, nil
	}
	host := string(r.Svc.Host())
	key := WindowKey{Host: HostID(host), Session: c.Session, Index: c.CurrentWindow}
	if l := LoadersFromContext(ctx); l != nil {
		thunk := l.WindowByKey.Load(ctx, key)
		w, wok := thunk()
		if !wok {
			return nil, nil
		}
		return projectWindowNode(w), nil
	}
	w, wok := r.Svc.WindowByKey(host, c.Session, c.CurrentWindow)
	if !wok {
		return nil, nil
	}
	return projectWindowNode(w), nil
}

// CurrentPane resolves TmuxClient.currentPane.
// Uses PaneByIDLoader (R3).
func (r *TmuxClientResolvers) CurrentPane(ctx context.Context, obj *TmuxClientNode) (*TmuxPaneNode, error) {
	if r.Svc == nil || obj == nil {
		return nil, nil
	}
	c, ok := r.loadClient(ctx, obj)
	if !ok || c.CurrentPane == "" {
		return nil, nil
	}
	host := string(r.Svc.Host())
	key := PaneKey{Host: HostID(host), PaneID: c.CurrentPane}
	if l := LoadersFromContext(ctx); l != nil {
		thunk := l.PaneByID.Load(ctx, key)
		p, pok := thunk()
		if !pok {
			return nil, nil
		}
		return projectPaneNode(p), nil
	}
	p, pok := r.Svc.PaneByID(host, c.CurrentPane)
	if !pok {
		return nil, nil
	}
	return projectPaneNode(p), nil
}
