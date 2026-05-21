package ps

import (
	"context"
	"log/slog"
)

// Service is the ONLY API consumers outside this package may call (R2).
// Resolvers in other domains MUST depend on this interface, not on
// *Provider directly.
type Service interface {
	// HostID returns the host id this service materialises ProcessIDs for.
	HostID() string

	// List returns a snapshot of every cached Process. Used by Host.processes
	// to enumerate before applying the filter.
	List() []Process

	// Get returns a single Process by key.
	Get(key ProcessID) (Process, bool)

	// Subscribe returns a channel that emits whenever the process table
	// may have changed. The channel closes when ctx is cancelled.
	// Resolvers MUST drain promptly; slow consumers drop events.
	Subscribe(ctx context.Context) <-chan invalidationEvent

	// LoadArgs returns argv for the given pids, batched into at most one
	// shellout per batch window (S10, O10, R3).
	LoadArgs(ctx context.Context, pids []int) (map[int][]string, error)

	// LoadCwd returns the cwd for a single pid (S10, O10, R3).
	LoadCwd(ctx context.Context, pid int) (string, error)

	// LoadCwds returns cwds for multiple pids. Used by cwdPrefix filter.
	LoadCwds(ctx context.Context, pids []int) (map[int]string, error)
}

// Ensure *Provider satisfies Service at compile time.
var _ Service = (*Provider)(nil)

// NewService constructs a Provider-backed Service and starts it.
// hostID is the prefix used in all ProcessID Node ids.
func NewService(ctx context.Context, hostID string, logger *slog.Logger) (Service, error) {
	adapter := NewAdapter(hostID)
	p := NewProvider(adapter, logger)
	if err := p.Start(ctx); err != nil {
		return nil, err
	}
	return p, nil
}
