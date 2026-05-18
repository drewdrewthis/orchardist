// Loaders for daemon-meta. Per R3, field resolvers go through loaders;
// no Snapshot() or full-state clone in a field resolver.
//
// DaemonStateLoader coalesces duplicate Query.daemonState calls within a
// single GraphQL request into one Service.DaemonState() call.
// Key: a fixed sentinel string — there is one daemon state, one entry.
package daemonmeta

import (
	"context"

	"github.com/graph-gophers/dataloader/v7"
)

// daemonStateKey is the singleton key used by DaemonStateLoader.
// There is exactly one daemon state; the loader uses a fixed key so
// concurrent field resolutions within one request coalesce (O1, T5).
const daemonStateKey = "daemon-state"

// NewDaemonStateLoader returns a DataLoader that coalesces concurrent
// Query.daemonState calls within one GraphQL request into a single
// Service.DaemonState() invocation.
//
// The batch function fires at most once per request per O1/R3 contract.
func NewDaemonStateLoader(svc Service) *dataloader.Loader[string, *DaemonState] {
	return dataloader.NewBatchedLoader(func(ctx context.Context, keys []string) []*dataloader.Result[*DaemonState] {
		out := make([]*dataloader.Result[*DaemonState], len(keys))
		// One call for the singleton; coalesces all keys (should all be daemonStateKey).
		ds, err := svc.DaemonState(ctx)
		for i := range keys {
			if err != nil {
				out[i] = &dataloader.Result[*DaemonState]{Error: err}
			} else {
				out[i] = &dataloader.Result[*DaemonState]{Data: ds}
			}
		}
		return out
	})
}

// LoadDaemonState is the thin helper resolvers call instead of touching the
// loader directly (keeps dataloader/v7 generics out of resolver code).
func LoadDaemonState(ctx context.Context, loader *dataloader.Loader[string, *DaemonState]) (*DaemonState, error) {
	thunk := loader.Load(ctx, daemonStateKey)
	ds, err := thunk()
	if err != nil {
		return nil, err
	}
	return ds, nil
}
