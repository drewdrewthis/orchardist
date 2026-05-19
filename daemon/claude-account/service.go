package claudeaccount

import (
	"context"
	"time"
)

// Service is the ONLY API consumers may import from this domain (R2).
// Resolvers and other modules import daemon/claude-account and depend on
// this interface, never on Provider directly.
//
// Cross-domain back-edges:
//   - ClaudeAccount.host      → resolved by HostReader (host-identity domain)
//   - ClaudeAccount.instances → resolved by InstancesReader (claude-instance domain)
//
// Both interfaces are defined here (R4 ISP: the consumer defines the
// interface in its own module). v1 fills them with stub implementations.
type Service interface {
	// List returns every cached Account, refreshing when stale.
	List(ctx context.Context) ([]Account, error)

	// Get returns one Account by ID, refreshing on cache miss or staleness.
	Get(ctx context.Context, key AccountID) (Account, bool, error)

	// Subscribe returns a channel of InvalidationEvents for as long as
	// ctx is alive. Channel is receive-only per R12.
	Subscribe(ctx context.Context) <-chan InvalidationEvent

	// LastError returns the most recent refresh error and when it happened.
	// Time is zero when err is nil.
	LastError() (time.Time, error)

	// Adapter exposes the underlying ShellAdapter for the pass-through
	// resolver (S16b).
	Adapter() *ShellAdapter
}

// HostReader is the narrow interface this domain needs from host-identity
// to resolve ClaudeAccount.host (R4, R5). Implemented by daemon/host-identity;
// v1 fills with a stub that constructs the Host from the id prefix alone.
type HostReader interface {
	GetHostByID(ctx context.Context, hostID string) (HostStub, error)
}

// HostStub carries just the host fields ClaudeAccount.host needs.
// Avoids importing host-identity types (R5 anti-corruption layer).
type HostStub struct {
	ID string
}

// InstancesReader is the narrow interface this domain needs from
// claude-instance to resolve ClaudeAccount.instances (R4, R5).
// v1 returns an empty slice; the claude-instance agent fills it.
type InstancesReader interface {
	InstancesByAccount(ctx context.Context, email string) ([]InstanceStub, error)
}

// InstanceStub carries just the fields ClaudeAccount.instances needs.
type InstanceStub struct {
	ID string
}

// NewService wraps a Provider and returns it as the Service interface.
// Callers outside this package should depend on Service, not *Provider.
func NewService(p *Provider) Service {
	return p
}
