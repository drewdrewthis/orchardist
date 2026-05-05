package hostservice

import (
	"context"
	"errors"
	"time"
)

// HostServiceID is the cache key. Encodes (host_id, name) so the same
// service name can be tracked across federated hosts later (ADR-011 §7).
// v1 always carries the local host_id; the format is stable from day
// one to keep merge-time work small.
type HostServiceID string

// MakeID composes a HostServiceID from host id + service name. Pure;
// callers (provider, resolvers) use it so the format lives in one place.
func MakeID(hostID, name string) HostServiceID {
	return HostServiceID(hostID + ":" + name)
}

// State mirrors graphql.HostServiceState as plain strings so the adapter
// layer doesn't pull in the gqlgen package. The Provider maps from this
// to the gqlgen enum at the resolver boundary.
type State string

// State constants align 1:1 with the GraphQL enum HostServiceState.
//
// `StateNotInstalled` and `StateUnknown` are kept distinct on purpose
// (see ADR-011 / issue #394). Use `StateNotInstalled` when the OS
// service manager confirmed the unit is absent, and reserve
// `StateUnknown` for output the daemon could not interpret.
const (
	StateActive       State = "active"
	StateInactive     State = "inactive"
	StateFailed       State = "failed"
	StateNotInstalled State = "not_installed"
	StateUnknown      State = "unknown"
)

// Snapshot is the in-memory representation of one watched service. The
// resolver lifts this into graphql.HostService at the resolver boundary.
//
// `Since` and `ExitCode` use pointer types so the resolver can map "no
// value reported by the OS" to GraphQL null. `LogTail` is similar.
type Snapshot struct {
	HostID    string
	Name      string
	State     State
	Since     *time.Time
	ExitCode  *int
	LogTail   *string
	FetchedAt time.Time
}

// PollInterval is the maximum age of a cached Snapshot before the next
// Get call refreshes synchronously. The poll loop refreshes on the same
// cadence proactively. ADR-011 §12 — "states change quickly when unit
// cycles".
const PollInterval = 5 * time.Second

// ErrServiceManagerMissing reports that the OS service-manager binary
// (launchctl on macOS, systemctl on Linux) is not on PATH. The resolver
// surfaces this as a per-field GraphQL error so the rest of the query
// keeps resolving.
var ErrServiceManagerMissing = errors.New("hostservice: OS service manager not installed on PATH")

// Adapter is the OS-specific I/O surface. Implementations live in
// adapter_darwin.go / adapter_linux.go and are selected at compile time
// via build tags.
//
// FetchOne is the only verb v1 needs — the watchlist is small (single-
// digit unit count) and each name is queried independently so a missing
// service never collapses the resolver. A bulk verb is unnecessary
// pre-DataLoader and adds an order-of-results coupling we don't want.
type Adapter interface {
	// FetchOne returns the current Snapshot for one watched name on the
	// given host.
	//
	// Contract:
	//   - The unit not existing on the host is NOT an error — the
	//     adapter returns a Snapshot with State == StateNotInstalled
	//     and no Since/ExitCode/LogTail.
	//   - Only the OS service-manager binary itself missing returns
	//     ErrServiceManagerMissing.
	//   - StateUnknown is reserved for service-manager output the
	//     adapter could not interpret (e.g. an unrecognised state
	//     token) — never for "unit absent".
	//   - Any other failure (parse error, timeout) is wrapped and
	//     returned so the Provider can surface it once on the affected
	//     key without poisoning peers.
	FetchOne(ctx context.Context, hostID, name string) (Snapshot, error)
}
