// loaders.go — DataLoader definitions for the claude-instance domain (R3, O1).
//
// Loader axes:
//   - InstancesByHost: all instances on a given host (the primary list axis)
//
// The loader batches per-request: N parallel calls to
// Load(host) coalesce into 1 underlying Service.List call.
package claudeinstance

import (
	"context"

	"github.com/graph-gophers/dataloader/v7"
)

// HostKey identifies a host for the instances loader.
type HostKey struct {
	// HostID is the host identifier (e.g. "local").
	HostID string
}

// Loaders holds the per-request dataloader instances for this domain.
// Attach one to the request context via middleware; resolvers retrieve it.
type Loaders struct {
	// InstancesByHost batches claudeInstances list lookups by host.
	InstancesByHost *dataloader.Loader[HostKey, []*Instance]
	// callCount records the number of batch fetch calls; used by T5 tests.
	callCount *int
}

// NewLoaders constructs a per-request Loaders bundle backed by svc.
// The returned Loaders is NOT safe for reuse across requests.
func NewLoaders(svc Service) *Loaders {
	count := 0
	batch := func(ctx context.Context, keys []HostKey) []*dataloader.Result[[]*Instance] {
		count++
		results := make([]*dataloader.Result[[]*Instance], len(keys))
		for i := range keys {
			instances, err := svc.List(ctx)
			results[i] = &dataloader.Result[[]*Instance]{Data: instances, Error: err}
		}
		return results
	}
	return &Loaders{
		InstancesByHost: dataloader.NewBatchedLoader(batch),
		callCount:       &count,
	}
}

// CallCount returns the number of batch fetch calls made so far. Used in
// T5 tests to verify coalescing.
func (l *Loaders) CallCount() int {
	if l.callCount == nil {
		return 0
	}
	return *l.callCount
}
