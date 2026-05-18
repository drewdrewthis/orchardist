package hostservices

import "context"

// adapter is the OS-specific I/O surface. Implementations live in
// adapter_darwin.go and adapter_linux.go, selected at compile time via
// build tags.
//
// fetchOne is the only verb v1 needs — the watchlist is small
// (single-digit unit count) and each name is queried independently so a
// missing service never collapses the resolver.
type adapter interface {
	// fetchOne returns the current HostServiceSnapshot for one watched
	// service name on the given host.
	//
	// Contract:
	//   - The unit not existing on the host is NOT an error — the adapter
	//     returns a snapshot with State == StateNotInstalled.
	//   - Only the OS service-manager binary itself missing returns
	//     ErrServiceManagerMissing.
	//   - StateUnknown is reserved for service-manager output the adapter
	//     could not interpret — never for "unit absent".
	//   - Any other failure is wrapped with %w and returned so the provider
	//     can surface it without poisoning peer keys.
	fetchOne(ctx context.Context, machineID, name string) (HostServiceSnapshot, error)
}

// nameToLabel is shared between the macOS adapter and the test stubs so
// the canonical "label form" (raw name as written in config) lives in one
// place. macOS launchd Labels are reverse-DNS by convention, but the
// orchard contract treats the config name as the launchctl Label verbatim.
func nameToLabel(name string) string { return name }
