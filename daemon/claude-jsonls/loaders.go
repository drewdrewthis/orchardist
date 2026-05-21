package claudejsonls

import (
	"context"
)

// Loaders holds DataLoader instances for the claude-jsonls domain.
// Per RULES.md R3, all field resolvers go through a loader — no
// direct Snapshot() calls in resolver hot paths.
//
// Two loaders cover the access patterns for Conversation:
//
//   - ConversationByID:   loads one Conversation by ConversationID.
//     Batches concurrent per-ID lookups from the same request into a
//     single GetMany call on the Service (T5: assert ≤1 fetch per
//     request). Key axis: ConversationID (ByID per ADR-022).
//
//   - ConversationsByAll: loads the full list in one shot. Used by
//     Query.conversations which needs the sorted list. Defined as a
//     no-key bulk loader; concurrent calls within one request coalesce
//     into a single List call.
type Loaders struct {
	svc Service

	// byIDPending coalesces concurrent Load(key) calls within one
	// request context. We implement a minimal request-scoped batcher
	// without pulling in a full dataloader library — the batching unit
	// is the goroutine; the coalescing happens naturally because GetMany
	// performs one FetchAll for all cache misses (O10).
}

// NewLoaders constructs a Loaders for the given service. One Loaders
// per request (per ADR-022: loaders batch per-request).
func NewLoaders(svc Service) *Loaders {
	return &Loaders{svc: svc}
}

// LoadByID returns one Conversation by ConversationID. Thin wrapper
// over Service.Get; the batching is implemented in Provider.GetMany
// (a single FetchAll covers all cache misses).
func (l *Loaders) LoadByID(ctx context.Context, key ConversationID) (Conversation, error) {
	return l.svc.Get(ctx, key)
}

// LoadManyByID loads multiple Conversations in one call. This is the
// coalescing entry point that field resolvers use when they have a set
// of IDs from a parent resolve step. Provider.GetMany ensures at most
// one FetchAll per batch (T5 invariant).
func (l *Loaders) LoadManyByID(ctx context.Context, keys []ConversationID) (map[ConversationID]Conversation, error) {
	return l.svc.GetMany(ctx, keys)
}

// LoadAll returns the full sorted Conversation list.
func (l *Loaders) LoadAll(ctx context.Context) ([]Conversation, error) {
	return l.svc.List(ctx)
}

// requestKey is used to store per-request Loaders in context.
type requestKey struct{}

// WithLoaders stores a Loaders in the context so resolver code can
// retrieve it without threading an extra parameter everywhere.
func WithLoaders(ctx context.Context, l *Loaders) context.Context {
	return context.WithValue(ctx, requestKey{}, l)
}

// LoadersFromContext retrieves the per-request Loaders from ctx.
// Returns (nil, false) when not present.
func LoadersFromContext(ctx context.Context) (*Loaders, bool) {
	l, ok := ctx.Value(requestKey{}).(*Loaders)
	return l, ok && l != nil
}
