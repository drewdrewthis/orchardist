// subscriptions.go — Subscription.worktreeChanged resolver (R16).
//
// The subscription emits the repo's full worktree list whenever the
// git provider invalidates any worktree belonging to the named repo.
// Emission happens AFTER the cache write (R16: emit after write, not before)
// because consumeInvalidations in provider.go calls refreshKey before
// broadcasting — so by the time the subscription pushes a delta, the
// provider's store already holds the fresh value.
package git

import (
	"context"
	"log/slog"
)

// SubscriptionResolver owns the git subscription resolver.
type SubscriptionResolver struct {
	svc    Service
	loader *WorktreesByProjectLoader
	logger *slog.Logger
}

// NewSubscriptionResolver creates a resolver backed by the service and loader.
func NewSubscriptionResolver(svc Service, loader *WorktreesByProjectLoader, logger *slog.Logger) *SubscriptionResolver {
	if logger == nil {
		logger = slog.Default()
	}
	return &SubscriptionResolver{svc: svc, loader: loader, logger: logger}
}

// WorktreeChanged resolves Subscription.worktreeChanged(repo: ID!).
// Returns a channel that emits the full worktree list for the named repo
// whenever any worktree in that repo changes.
//
// The argument repo is the Repo ID, not the slug. Emission happens AFTER
// provider.go writes the fresh value to the cache (R16).
func (r *SubscriptionResolver) WorktreeChanged(ctx context.Context, repoID string) (<-chan []Worktree, error) {
	out := make(chan []Worktree, 4)
	ch := r.svc.Subscribe(ctx)

	go func() {
		defer close(out)
		defer func() {
			if rec := recover(); rec != nil {
				r.logger.Error("git subscription goroutine panicked", "panic", rec)
			}
		}()
		for {
			select {
			case <-ctx.Done():
				return
			case inv, ok := <-ch:
				if !ok {
					return
				}
				// Filter to the requested repo (by project ID).
				if inv.ProjectID != repoID {
					continue
				}
				// Fetch the fresh worktree list from the service (which
				// reads from the provider's already-updated cache — R16).
				wts, err := r.svc.ListWorktrees(ctx, repoID)
				if err != nil {
					r.logger.Warn("git subscription: ListWorktrees failed",
						"repoID", repoID, "err", err)
					continue
				}
				select {
				case out <- wts:
				case <-ctx.Done():
					return
				default:
					// Subscriber is slow — drop rather than block (O7 fan-out bounded).
					r.logger.Warn("git subscription: slow subscriber, dropping delta",
						"repoID", repoID)
				}
			}
		}
	}()
	return out, nil
}
