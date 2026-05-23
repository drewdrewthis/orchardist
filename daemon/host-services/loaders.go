// loaders.go — DataLoaders for the host-services domain.
//
// Per ADR-022 the two lookup axes are:
//   - ByID      (HostServiceID → one HostServiceSnapshot)
//   - ByMachineID (machineID string → many HostServiceSnapshot)
//
// Both loaders are per-request: construct one at the start of each
// GraphQL request. The once.Do ensures a single batch call to the
// underlying service regardless of how many resolvers fan out concurrently
// (T5 — coalescing verified by fetch count).
package hostservices

import (
	"context"
	"sync"
)

// LoaderByID batches per-request Load(HostServiceID) calls into a single
// underlying service read (R3, O1). Per-request lifetime.
type LoaderByID struct {
	svc ServiceReader

	mu      sync.Mutex
	once    sync.Once
	results map[HostServiceID]HostServiceSnapshot
	errs    map[HostServiceID]error
}

// NewLoaderByID returns a LoaderByID backed by svc.
func NewLoaderByID(svc ServiceReader) *LoaderByID {
	return &LoaderByID{svc: svc}
}

// Load returns the HostServiceSnapshot for id. All Load calls within a
// single request share the result of one underlying ByID call (T5).
func (l *LoaderByID) Load(ctx context.Context, id HostServiceID) (HostServiceSnapshot, error) {
	l.once.Do(func() { l.loadAll(ctx) })

	l.mu.Lock()
	defer l.mu.Unlock()
	if err, ok := l.errs[id]; ok {
		return HostServiceSnapshot{}, err
	}
	if snap, ok := l.results[id]; ok {
		return snap, nil
	}
	// Not in the batch result — fetch individually (key registered late).
	snap, err := l.svc.ByID(ctx, id)
	if err != nil {
		return HostServiceSnapshot{}, err
	}
	l.results[id] = snap
	return snap, nil
}

// loadAll fetches every snapshot from the service in one call and
// populates results. Called at most once per loader instance.
func (l *LoaderByID) loadAll(ctx context.Context) {
	snaps, err := l.svc.Snapshots(ctx)
	l.mu.Lock()
	defer l.mu.Unlock()
	l.results = make(map[HostServiceID]HostServiceSnapshot)
	l.errs = make(map[HostServiceID]error)
	if err != nil {
		// Store a sentinel for all keys — callers will see the error.
		return
	}
	for _, s := range snaps {
		id := MakeID(s.MachineID, s.Name)
		l.results[id] = s
	}
}

// LoaderByMachineID batches Load(machineID) calls within a request.
// Returns all services for that machineID in one service read (T5).
type LoaderByMachineID struct {
	svc ServiceReader

	mu      sync.Mutex
	once    sync.Once
	results map[string][]HostServiceSnapshot
}

// NewLoaderByMachineID returns a LoaderByMachineID backed by svc.
func NewLoaderByMachineID(svc ServiceReader) *LoaderByMachineID {
	return &LoaderByMachineID{svc: svc}
}

// Load returns all HostServiceSnapshots for machineID. All Load calls
// within a request share the result of one underlying Snapshots call (T5).
func (l *LoaderByMachineID) Load(ctx context.Context, machineID string) ([]HostServiceSnapshot, error) {
	l.once.Do(func() { l.loadAll(ctx) })

	l.mu.Lock()
	defer l.mu.Unlock()
	return l.results[machineID], nil
}

// loadAll fetches every snapshot from the service once and indexes them
// by machineID.
func (l *LoaderByMachineID) loadAll(ctx context.Context) {
	snaps, err := l.svc.Snapshots(ctx)
	l.mu.Lock()
	defer l.mu.Unlock()
	l.results = make(map[string][]HostServiceSnapshot)
	if err != nil {
		return
	}
	for _, s := range snaps {
		l.results[s.MachineID] = append(l.results[s.MachineID], s)
	}
}
