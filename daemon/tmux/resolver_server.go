// resolver_server.go contains field resolvers for the TmuxServer GraphQL type.
// R6: one file per GraphQL type.
// R3: all field reads go through the service/loader — no Snapshot() calls.
package tmux

import (
	"context"
)

// TmuxServerResolvers holds the TmuxServer field resolver implementations.
type TmuxServerResolvers struct {
	Svc TmuxService
}

// Pid resolves TmuxServer.pid.
func (r *TmuxServerResolvers) Pid(ctx context.Context, obj *TmuxServerNode) (*int64, error) {
	if r.Svc == nil || obj == nil {
		return nil, nil
	}
	info := r.Svc.Server()
	if info.Pid == 0 {
		return nil, nil
	}
	v := int64(info.Pid)
	return &v, nil
}

// Alive resolves TmuxServer.alive.
func (r *TmuxServerResolvers) Alive(ctx context.Context, obj *TmuxServerNode) (bool, error) {
	if r.Svc == nil || obj == nil {
		return false, nil
	}
	return r.Svc.Server().Alive, nil
}

// Sessions resolves TmuxServer.sessions with optional sort.
// Uses AllSessions() — one cache snapshot for the full list (R3: no per-item Snapshot()).
func (r *TmuxServerResolvers) Sessions(ctx context.Context, obj *TmuxServerNode, sortKey *TmuxSessionSortEnum) ([]*TmuxSessionNode, error) {
	if r.Svc == nil || obj == nil {
		return nil, nil
	}
	all := r.Svc.AllSessions()
	all = sortSessions(all, sortKey)
	out := make([]*TmuxSessionNode, len(all))
	for i, s := range all {
		out[i] = projectSessionNode(s)
	}
	return out, nil
}

// Clients resolves TmuxServer.clients.
func (r *TmuxServerResolvers) Clients(ctx context.Context, obj *TmuxServerNode) ([]*TmuxClientNode, error) {
	if r.Svc == nil || obj == nil {
		return nil, nil
	}
	all := r.Svc.AllClients()
	out := make([]*TmuxClientNode, len(all))
	for i, c := range all {
		out[i] = projectClientNode(c)
	}
	return out, nil
}
