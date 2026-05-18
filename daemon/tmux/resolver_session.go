// resolver_session.go contains field resolvers for the TmuxSession GraphQL type.
// R6: one file per GraphQL type.
// R3: all field reads go through SessionByNameLoader — no Snapshot() in field paths.
package tmux

import (
	"context"
	"slices"
)

// TmuxSessionSort enum values. These mirror the schema enum.
type TmuxSessionSortEnum int

const (
	TmuxSessionSortLastActivity TmuxSessionSortEnum = iota
	TmuxSessionSortName
)

// TmuxSessionResolvers holds the TmuxSession field resolver implementations.
type TmuxSessionResolvers struct {
	Svc TmuxService
}

// loadSession fetches the session data for obj via the per-request loader when
// available, falling back to a direct service call. This is the R3 pattern:
// field resolver goes through a loader, not Snapshot().
func (r *TmuxSessionResolvers) loadSession(ctx context.Context, obj *TmuxSessionNode) (Session, bool) {
	if r.Svc == nil || obj == nil {
		return Session{}, false
	}
	host, name, ok := ParseSessionID(obj.ID)
	if !ok {
		return Session{}, false
	}
	// Use per-request loader when available.
	if l := LoadersFromContext(ctx); l != nil {
		thunk := l.SessionByName.Load(ctx, SessionKey{Host: HostID(host), Name: name})
		s, found := thunk()
		return s, found
	}
	// Fallback: direct service call (no loader context, e.g. subscription goroutines).
	return r.Svc.SessionByName(host, name)
}

// Server resolves TmuxSession.server.
func (r *TmuxSessionResolvers) Server(ctx context.Context, obj *TmuxSessionNode) (*TmuxServerNode, error) {
	if r.Svc == nil {
		return nil, nil
	}
	return projectServerNode(r.Svc.Host(), r.Svc.Server()), nil
}

// CreatedAt resolves TmuxSession.createdAt.
func (r *TmuxSessionResolvers) CreatedAt(ctx context.Context, obj *TmuxSessionNode) (string, error) {
	s, ok := r.loadSession(ctx, obj)
	if !ok || s.CreatedAt.IsZero() {
		return "", nil
	}
	return formatRFC3339OrEmpty(s.CreatedAt), nil
}

// Attached resolves TmuxSession.attached.
func (r *TmuxSessionResolvers) Attached(ctx context.Context, obj *TmuxSessionNode) (bool, error) {
	s, ok := r.loadSession(ctx, obj)
	if !ok {
		return false, nil
	}
	return s.Attached, nil
}

// ActiveAttached resolves TmuxSession.activeAttached.
func (r *TmuxSessionResolvers) ActiveAttached(ctx context.Context, obj *TmuxSessionNode) (bool, error) {
	s, ok := r.loadSession(ctx, obj)
	if !ok {
		return false, nil
	}
	return s.Attached, nil
}

// AttachedClients resolves TmuxSession.attachedClients.
// Uses AllClients() — one cache read, filtered in Go (R3: no N+1 Snapshot() per client).
func (r *TmuxSessionResolvers) AttachedClients(ctx context.Context, obj *TmuxSessionNode) ([]*TmuxClientNode, error) {
	if r.Svc == nil || obj == nil {
		return nil, nil
	}
	s, ok := r.loadSession(ctx, obj)
	if !ok {
		return nil, nil
	}
	all := r.Svc.AllClients()
	out := []*TmuxClientNode{}
	for _, c := range all {
		if c.Session == s.Key.Name {
			out = append(out, projectClientNode(c))
		}
	}
	return out, nil
}

// LastActivityAt resolves TmuxSession.lastActivityAt.
func (r *TmuxSessionResolvers) LastActivityAt(ctx context.Context, obj *TmuxSessionNode) (*string, error) {
	s, ok := r.loadSession(ctx, obj)
	if !ok {
		return nil, nil
	}
	return formatRFC3339OrNil(s.LastActivityAt), nil
}

// Windows resolves TmuxSession.windows.
// Uses AllWindows() — one cache read, filtered by session name (R3).
func (r *TmuxSessionResolvers) Windows(ctx context.Context, obj *TmuxSessionNode) ([]*TmuxWindowNode, error) {
	if r.Svc == nil || obj == nil {
		return nil, nil
	}
	s, ok := r.loadSession(ctx, obj)
	if !ok {
		return nil, nil
	}
	all := r.Svc.AllWindows()
	out := []*TmuxWindowNode{}
	for _, w := range all {
		if w.Key.Session == s.Key.Name {
			out = append(out, projectWindowNode(w))
		}
	}
	return out, nil
}

// CurrentWindow resolves TmuxSession.currentWindow.
// Uses WindowByKeyLoader when available (R3).
func (r *TmuxSessionResolvers) CurrentWindow(ctx context.Context, obj *TmuxSessionNode) (*TmuxWindowNode, error) {
	if r.Svc == nil || obj == nil {
		return nil, nil
	}
	s, ok := r.loadSession(ctx, obj)
	if !ok {
		return nil, nil
	}
	host := string(r.Svc.Host())
	key := WindowKey{Host: HostID(host), Session: s.Key.Name, Index: s.CurrentWindow}

	if l := LoadersFromContext(ctx); l != nil {
		thunk := l.WindowByKey.Load(ctx, key)
		w, wok := thunk()
		if !wok {
			return nil, nil
		}
		return projectWindowNode(w), nil
	}
	w, wok := r.Svc.WindowByKey(host, s.Key.Name, s.CurrentWindow)
	if !wok {
		return nil, nil
	}
	return projectWindowNode(w), nil
}

// sortSessions sorts sessions by sortKey. Exported for use by server resolver.
func sortSessions(sessions []Session, sortKey *TmuxSessionSortEnum) []Session {
	key := TmuxSessionSortLastActivity
	if sortKey != nil {
		key = *sortKey
	}
	switch key {
	case TmuxSessionSortName:
		slices.SortStableFunc(sessions, func(a, b Session) int {
			if a.Key.Name < b.Key.Name {
				return -1
			}
			if a.Key.Name > b.Key.Name {
				return 1
			}
			return 0
		})
	default: // LAST_ACTIVITY
		slices.SortStableFunc(sessions, func(a, b Session) int {
			aZero := a.LastActivityAt.IsZero()
			bZero := b.LastActivityAt.IsZero()
			if aZero != bZero {
				if !aZero {
					return -1
				}
				return 1
			}
			if !a.LastActivityAt.Equal(b.LastActivityAt) {
				if a.LastActivityAt.After(b.LastActivityAt) {
					return -1
				}
				return 1
			}
			if a.Key.Name < b.Key.Name {
				return -1
			}
			if a.Key.Name > b.Key.Name {
				return 1
			}
			return 0
		})
	}
	return sessions
}
