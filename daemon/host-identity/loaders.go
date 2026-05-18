package hostidentity

import (
	"context"
	"time"

	"github.com/graph-gophers/dataloader/v7"

	graphql "github.com/drewdrewthis/git-orchard-rs/internal/server/graphql"
)

// Loaders holds request-scoped DataLoader instances for the host-identity
// domain. Per ADR-022: one loader per axis. Per R3: field resolvers go through
// loaders — no Snapshot() or direct provider calls in field resolvers.
//
// One Loaders value lives for the lifetime of one GraphQL operation.
type Loaders struct {
	// HostByID coalesces multiple Host lookups by machine id within a single
	// GraphQL request. Per O1: key axis is HostID (stable, correct).
	HostByID *dataloader.Loader[string, *graphql.Host]

	// batchCount is the number of batch invocations — used by T5 coalescing tests.
	batchCount *batchCounter
}

// batchCounter is a thread-safe call counter for DataLoader batch functions.
type batchCounter struct {
	n int
}

func (c *batchCounter) inc() { c.n++ }

// Value returns the current count. Safe only when no concurrent increment runs.
func (c *batchCounter) Value() int { return c.n }

// BatchCount returns the number of batch invocations. Used by coalescing tests (T5).
func (l *Loaders) BatchCount() int { return l.batchCount.Value() }

// NewLoaders builds a fresh Loaders bundle bound to the given Service.
// Per R3: resolvers call Loaders, not the Service directly.
// Per O1: key is the machine id string — matches the HostID axis.
func NewLoaders(svc Service) *Loaders {
	counter := &batchCounter{}

	batchFn := func(ctx context.Context, ids []string) []*dataloader.Result[*graphql.Host] {
		counter.inc()
		return loadHostsByID(ctx, svc, ids)
	}

	opts := []dataloader.Option[string, *graphql.Host]{
		// 1ms wait window — matches gqlgen handler tick. Short enough to feel
		// synchronous; long enough to coalesce resolver fan-outs into one batch.
		dataloader.WithWait[string, *graphql.Host](1 * time.Millisecond),
		// NoCache: Host is already cached in the Provider; no double caching.
		dataloader.WithCache[string, *graphql.Host](&dataloader.NoCache[string, *graphql.Host]{}),
	}

	return &Loaders{
		HostByID:   dataloader.NewBatchedLoader(batchFn, opts...),
		batchCount: counter,
	}
}

// loadHostsByID is the batch function for the HostByID loader.
// Per O10: batches all lookups into one GetMany call.
func loadHostsByID(ctx context.Context, svc Service, ids []string) []*dataloader.Result[*graphql.Host] {
	out := make([]*dataloader.Result[*graphql.Host], len(ids))

	// Deduplicate IDs before fetching.
	unique := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		unique[id] = struct{}{}
	}

	// Fetch each unique host. v1: only the local machine is known.
	results := make(map[string]*graphql.Host, len(unique))
	for id := range unique {
		h, err := svc.Host(ctx, HostID(id))
		if err != nil || h == nil {
			// Unknown / error — leave absent from results; caller gets a stub.
			continue
		}
		results[id] = h
	}

	for i, id := range ids {
		if h, ok := results[id]; ok {
			out[i] = &dataloader.Result[*graphql.Host]{Data: h}
		} else {
			// Return a stub node so field resolvers don't nil-panic when the
			// id is valid but not yet known (e.g. cold boot race).
			out[i] = &dataloader.Result[*graphql.Host]{Data: &graphql.Host{ID: id}}
		}
	}
	return out
}
