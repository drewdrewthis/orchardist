// Package hostservices implements the host-services domain for the orchard daemon.
//
// Per R1 the package owns everything related to launchd (macOS) and
// systemd-user (Linux) unit monitoring: the in-process cache, OS-specific
// adapters, DataLoaders, field resolvers, mutations, and the pass-through
// escape hatch.
//
// Consumers MUST import only this package — never provider.go types
// directly (R2). The Service struct is the sole narrowing point.
package hostservices

import (
	"context"
	"errors"
	"time"
)

// State mirrors the GraphQL HostServiceState enum as plain strings so the
// adapter layer does not need to import gqlgen. Resolvers map from State
// to the generated enum at the GraphQL boundary.
type State string

// State constants align 1:1 with the GraphQL enum HostServiceState.
//
// StateNotInstalled and StateUnknown are kept distinct (S5 / R14):
//   - StateNotInstalled: OS service manager confirmed the unit is absent.
//   - StateUnknown: service manager output could not be parsed.
const (
	StateActive       State = "active"
	StateInactive     State = "inactive"
	StateFailed       State = "failed"
	StateNotInstalled State = "not_installed"
	StateUnknown      State = "unknown"
)

// HostServiceID is the stable cache key for a watched service. Encodes
// (machineID, name) so the same service name can be tracked across
// federated hosts. Format: "<machineID>:<name>".
type HostServiceID string

// MakeID composes a HostServiceID from host machineID + service name.
// Pure function — callers use it so the format lives in one place.
func MakeID(machineID, name string) HostServiceID {
	return HostServiceID(machineID + ":" + name)
}

// HostServiceSnapshot is the in-memory representation of one watched
// service. Resolvers project this into the GraphQL HostService type.
//
// Since and ExitCode use pointer types so resolvers can map "no value
// reported by the OS" to GraphQL null. LogTail is similar.
type HostServiceSnapshot struct {
	MachineID string
	Name      string
	State     State
	Since     *time.Time
	ExitCode  *int
	LogTail   *string
	FetchedAt time.Time
}

// ErrServiceManagerMissing is returned by the Adapter when the OS
// service-manager binary (launchctl on macOS, systemctl on Linux) is
// not on PATH. The resolver surfaces this as a per-field GraphQL error
// so the rest of the query keeps resolving.
var ErrServiceManagerMissing = errors.New("hostservices: OS service manager not installed on PATH")

// InvalidationEvent is the per-key signal a Provider emits when a
// service's snapshot may have changed. Subscription writers consume
// these to push deltas to GraphQL subscribers.
type InvalidationEvent struct {
	Key    HostServiceID
	Reason string
	At     time.Time
}

// HostIdentityReader is the narrow host-identity interface this domain
// needs. Per R4 the consumer (this package) defines the interface in its
// own module; the host-identity provider must satisfy it.
type HostIdentityReader interface {
	// MachineID returns the OS-assigned machine identity (IOPlatformUUID
	// on macOS, /etc/machine-id on Linux).
	MachineID(ctx context.Context) (string, error)
}

// ServiceReader is the resolver-facing read API. Resolvers depend on
// this interface, never on *Service directly (R4).
type ServiceReader interface {
	// Snapshots returns every snapshot currently in cache. The returned
	// slice is a copy — safe for the caller to hold without locking.
	Snapshots(ctx context.Context) ([]HostServiceSnapshot, error)

	// ByID returns the snapshot for one service by its HostServiceID.
	ByID(ctx context.Context, id HostServiceID) (HostServiceSnapshot, error)

	// ByMachineID returns all snapshots for a given machine ID.
	ByMachineID(ctx context.Context, machineID string) ([]HostServiceSnapshot, error)

	// Subscribe returns a channel that receives invalidation events each
	// time a service refreshes. Channel closes when ctx is cancelled.
	Subscribe(ctx context.Context) <-chan InvalidationEvent

	// MachineID returns the machineID this service tracks.
	MachineID() string
}

// Service wraps Provider to form the public API surface (R2). It is the
// only type resolvers and other consumers should import from this package.
type Service struct {
	provider *provider
}

// New constructs a Service with the platform-specific adapter, the
// machineID from host-identity, and the service watchlist.
func New(machineID string, services []string) *Service {
	return NewWith(newAdapter(), machineID, services, time.Now)
}

// NewWith is the test-friendly constructor — accepts an injected Adapter
// and clock so tests can drive freshness deterministically.
func NewWith(a adapter, machineID string, services []string, clock func() time.Time) *Service {
	return &Service{
		provider: newProvider(a, machineID, services, clock),
	}
}

// Start hydrates the cache once synchronously, then starts the poll loop.
// The poll loop terminates when ctx is cancelled.
func (s *Service) Start(ctx context.Context) error {
	return s.provider.start(ctx)
}

// Snapshots returns every cached snapshot. Copy-safe — callers may hold
// the returned slice without locking. Implements ServiceReader.
func (s *Service) Snapshots(_ context.Context) ([]HostServiceSnapshot, error) {
	return s.provider.snapshots(), nil
}

// ByID returns the snapshot for one service by its HostServiceID.
// Implements ServiceReader.
func (s *Service) ByID(_ context.Context, id HostServiceID) (HostServiceSnapshot, error) {
	return s.provider.byID(id)
}

// ByMachineID returns all snapshots for a given machineID.
// Implements ServiceReader.
func (s *Service) ByMachineID(_ context.Context, machineID string) ([]HostServiceSnapshot, error) {
	return s.provider.byMachineID(machineID), nil
}

// Subscribe returns a read-only channel of invalidation events (R12).
// Implements ServiceReader.
func (s *Service) Subscribe(ctx context.Context) <-chan InvalidationEvent {
	return s.provider.subscribe(ctx)
}

// MachineID returns the machineID this Service tracks.
// Implements ServiceReader.
func (s *Service) MachineID() string {
	return s.provider.machineID
}

// Compile-time assertion that *Service satisfies ServiceReader.
var _ ServiceReader = (*Service)(nil)
