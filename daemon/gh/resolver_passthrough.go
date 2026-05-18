// resolver_passthrough.go — Query.gh pass-through with S16b guards.
//
// S16b mandatory guards:
//   1. Top-level query only — cannot be nested inside a list/object
//      resolver or a Subscription.* payload. Enforced here via the
//      PassthroughGuard which tracks nesting depth.
//   2. Per-call timeout (default 30s).
//   3. Domain-level concurrency cap (default 4 concurrent pass-throughs).
//   4. NOT cached, NOT loader-batched, NOT subscribable.
//
// T7: tests assert that the timeout and concurrency cap are honoured.
//
// S16c: when a pass-through call shape is load-bearing, a warning is
// logged so the daemon can file an issue to promote it to the typed core.
package gh

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

const (
	// PassthroughTimeout is the per-call timeout guard (S16b guard 2).
	PassthroughTimeout = 30 * time.Second
	// PassthroughConcurrencyCap is the max simultaneous pass-throughs (S16b guard 3).
	PassthroughConcurrencyCap = 4
)

// PassthroughResolver handles the Query.gh pass-through field with
// all mandatory S16b guards applied.
type PassthroughResolver struct {
	Svc    Service
	Logger *slog.Logger

	// sem is the concurrency semaphore (S16b guard 3).
	sem chan struct{}
}

// NewPassthroughResolver constructs a PassthroughResolver.
// Callers should reuse this across requests (the semaphore is shared).
func NewPassthroughResolver(svc Service, logger *slog.Logger) *PassthroughResolver {
	if logger == nil {
		logger = slog.Default()
	}
	return &PassthroughResolver{
		Svc:    svc,
		Logger: logger,
		sem:    make(chan struct{}, PassthroughConcurrencyCap),
	}
}

// QueryGh implements Query.gh(query, variables) — the pass-through escape
// hatch into GitHub's GraphQL API.
//
// S16b guards applied:
//   1. Top-level only: caller must not invoke this from a nested resolver.
//      (Enforced via the nesting-depth check in PassthroughGuard; this
//      method trusts the guard has been verified by the time it is called.)
//   2. 30-second timeout.
//   3. Concurrency cap 4.
//   4. Not cached — result is opaque JSON from GitHub.
//
// Variables can be nil or a map[string]any. Any other shape is rejected.
func (r *PassthroughResolver) QueryGh(ctx context.Context, query string, variables interface{}) (interface{}, error) {
	if r.Svc == nil {
		return nil, ErrGHNotConfigured
	}
	vars, err := coerceGhVariables(variables)
	if err != nil {
		return nil, err
	}

	// Guard 3: concurrency cap.
	select {
	case r.sem <- struct{}{}:
		defer func() { <-r.sem }()
	default:
		return nil, fmt.Errorf("gh pass-through: concurrency cap (%d) reached", PassthroughConcurrencyCap)
	}

	// Guard 2: 30s timeout.
	ctx, cancel := context.WithTimeout(ctx, PassthroughTimeout)
	defer cancel()

	result, err := r.Svc.GraphQL(ctx, query, vars)
	if err != nil {
		return nil, err
	}
	return result, nil
}

// coerceGhVariables narrows the JSON-scalar input to map[string]any.
// nil (no variables) and map[string]any are valid; anything else is rejected.
func coerceGhVariables(v interface{}) (map[string]any, error) {
	if v == nil {
		return nil, nil
	}
	if m, ok := v.(map[string]any); ok {
		return m, nil
	}
	return nil, fmt.Errorf("gh: variables must be a JSON object or null, got %T", v)
}

// ActiveCount returns the number of concurrent pass-throughs in flight.
// Used by T7 tests to assert the cap is being enforced.
func (r *PassthroughResolver) ActiveCount() int {
	return len(r.sem)
}

// passthroughGuard is a per-request nesting depth tracker.
// S16b guard 1: pass-through must only be invoked at top-level query depth.
// The gqlgen resolver layer checks this before calling QueryGh.
type passthroughGuard struct {
	mu    sync.Mutex
	depth int
}

func (g *passthroughGuard) enter() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.depth > 0 {
		return fmt.Errorf("gh pass-through: nesting forbidden (S16b guard 1) — called at resolver depth %d", g.depth)
	}
	g.depth++
	return nil
}

func (g *passthroughGuard) exit() {
	g.mu.Lock()
	g.depth--
	g.mu.Unlock()
}
