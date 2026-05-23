package ps

import "context"

// HostExtResolver implements the `extend type Host { processes(filter) }`
// field (R6: one file per logical concern, S15b: extend declared and
// resolved in this domain).
//
// Local host: in-process Service.List() + ApplyProcessFilter.
// Peer host: federation is the daemon shell's concern; this resolver
// returns the local process list only. Peer routing happens at the
// daemon level before this resolver is called.
type HostExtResolver struct {
	svc Service
}

// NewHostExtResolver constructs a HostExtResolver.
func NewHostExtResolver(svc Service) *HostExtResolver {
	return &HostExtResolver{svc: svc}
}

// Processes resolves Host.processes(filter). Per L4: in-process read
// from the cache — no script exec in the field path.
func (r *HostExtResolver) Processes(ctx context.Context, filter *ProcessFilter) ([]*ProcessProjection, error) {
	all := r.svc.List()
	filtered, err := ApplyProcessFilter(ctx, r.svc, all, filter)
	if err != nil {
		return nil, err
	}
	hostID := r.svc.HostID()
	out := make([]*ProcessProjection, 0, len(filtered))
	for i := range filtered {
		out = append(out, ProjectProcess(&filtered[i], hostID))
	}
	return out, nil
}
