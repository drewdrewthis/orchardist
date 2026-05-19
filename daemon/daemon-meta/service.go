// Package daemonmeta owns the provider freshness counters and per-field
// provenance envelope for the orchard daemon.
//
// Owns: DaemonState, ProviderHealth, Meta.
// Owns: Query.daemonState, Mutation.daemonReload.
//
// The ProviderRegistry interface is defined here (per R4 ISP — the consumer
// defines the interface). Every other domain's provider implements it to
// surface their freshness counters in DaemonState.providers[].
//
// daemonReload is the canonical L5 carve-out: it reloads ~/.orchard/config.json
// in-process and returns the post-reload DaemonState. No script is exec'd —
// the operation affects daemon-internal state, not external truth.
package daemonmeta

import (
	"context"
	"time"
)

// ProviderRegistry is the narrow freshness-counter interface that each domain
// provider must implement to appear in DaemonState.providers[].
//
// This interface is defined in the consumer (daemon-meta) per R4 ISP.
// Provider implementations in other domains depend on this interface —
// never the other way around.
type ProviderRegistry interface {
	// ProviderName returns the stable label for this provider
	// (e.g. "tmux", "git", "gh", "ps"). Must match the labels in
	// DaemonState.providers[].
	ProviderName() string

	// ProviderHealth returns a snapshot of this provider's freshness state.
	ProviderHealth() ProviderHealthSnapshot
}

// ProviderHealthSnapshot is the freshness snapshot returned by ProviderRegistry.
// It mirrors the ProviderHealth GraphQL type.
type ProviderHealthSnapshot struct {
	// Configured is true when this provider is wired into the daemon.
	Configured bool
	// LastSuccessfulRefresh is the RFC3339 timestamp of the last successful
	// refresh; nil until the first success.
	LastSuccessfulRefresh *string
	// LastFailureReason is the most recent failure reason; nil when none observed.
	LastFailureReason *string
	// RefreshCount is the total successful refreshes since daemon boot.
	RefreshCount int64
	// FailureCount is the total refresh failures since daemon boot.
	FailureCount int64
}

// Service is the only API consumers may call in this domain (R2).
// Resolvers and loaders import this interface, not provider.go directly.
type Service interface {
	// DaemonState returns a point-in-time snapshot of daemon health.
	// Cheap — reads in-memory counters, no I/O.
	DaemonState(ctx context.Context) (*DaemonState, error)

	// Reload reloads the daemon's config and re-evaluates provider health.
	// This is the L5 carve-out — in-process, no script. Callers MUST
	// call SetConfigReloader before invoking Reload.
	Reload(ctx context.Context) (*DaemonState, error)
}

// DaemonState is the domain model for daemon-wide health. Separate from the
// GraphQL-generated graphql.DaemonState to avoid a direct dependency on
// generated code inside this package.
type DaemonState struct {
	// StartedAt is the RFC3339 timestamp when the daemon started serving.
	StartedAt string
	// UptimeS is the daemon uptime in whole seconds.
	UptimeS int64
	// Providers is the per-provider health rollup.
	Providers []ProviderHealthSnapshot
	// ProviderNames maps index → provider name for projection.
	ProviderNames []string
}

// ConfigReloader is a function that re-reads and applies the daemon's
// config file in-process. Injected at wiring time to avoid import cycles.
type ConfigReloader func(ctx context.Context) error

// ServiceImpl implements Service. Exported per R11 (public constructors return
// concrete types; consumers depend on the Service interface they define).
type ServiceImpl struct {
	startedAt time.Time
	providers []ProviderRegistry
	reloader  ConfigReloader
}

// NewService constructs a Service that reads freshness counters from the
// registered providers. startedAt is captured at daemon boot and used to
// compute uptime. providers is the ordered list of registered providers;
// call RegisterProvider to grow it after construction.
func NewService(startedAt time.Time) *ServiceImpl {
	return &ServiceImpl{
		startedAt: startedAt,
	}
}

// RegisterProvider adds a provider to the freshness rollup. Order determines
// the index in DaemonState.providers[].
func (s *ServiceImpl) RegisterProvider(p ProviderRegistry) {
	s.providers = append(s.providers, p)
}

// SetConfigReloader injects the function that re-reads ~/.orchard/config.json.
// Must be called before the first Reload invocation.
func (s *ServiceImpl) SetConfigReloader(fn ConfigReloader) {
	s.reloader = fn
}

// DaemonState returns a point-in-time snapshot of daemon health (R2, O4, O5).
func (s *ServiceImpl) DaemonState(ctx context.Context) (*DaemonState, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return s.buildState(), nil
}

// Reload reloads the daemon config in-process (L5 exception, M5 idempotent)
// and returns the post-reload DaemonState (L8, S8).
func (s *ServiceImpl) Reload(ctx context.Context) (*DaemonState, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s.reloader != nil {
		if err := s.reloader(ctx); err != nil {
			return nil, err
		}
	}
	return s.buildState(), nil
}

// buildState constructs a DaemonState from current in-memory counters.
// Pure function over the current provider snapshots — no I/O.
func (s *ServiceImpl) buildState() *DaemonState {
	uptime := int64(time.Since(s.startedAt).Round(time.Second).Seconds())
	names := make([]string, 0, len(s.providers))
	snapshots := make([]ProviderHealthSnapshot, 0, len(s.providers))
	for _, p := range s.providers {
		names = append(names, p.ProviderName())
		snapshots = append(snapshots, p.ProviderHealth())
	}
	return &DaemonState{
		StartedAt:     s.startedAt.UTC().Format(time.RFC3339),
		UptimeS:       uptime,
		ProviderNames: names,
		Providers:     snapshots,
	}
}
