// subscriptions.go — pullRequestChanged and runChanged subscriptions.
//
// R16: emit AFTER cache write (the provider's Invalidate is called only
// after DropPRCache/DropRunCache — see webhook.go).
//
// R17: each subscription goroutine wraps its loop in a recover-and-log
// handler to prevent panics in the subscription path from killing the daemon.
//
// R10: goroutine ownership — the subscription goroutine is owned by the
// caller's context; it exits when ctx.Done() fires.
//
// S7: subscriptions emit small typed deltas (the specific PullRequest or
// WorkflowRun), not full graph re-fetches.
package gh

import (
	"context"
	"fmt"
	"log/slog"
	"runtime/debug"
	"strings"
)

// PullRequestChangedResolver drives Subscription.pullRequestChanged.
// It filters InvalidationEvents from the provider's Subscribe channel
// for the specific repo+number, re-fetches the PR, and emits it.
type PullRequestChangedResolver struct {
	Svc    Service
	Logger *slog.Logger
}

// NewPullRequestChangedResolver constructs the resolver.
func NewPullRequestChangedResolver(svc Service, logger *slog.Logger) *PullRequestChangedResolver {
	if logger == nil {
		logger = slog.Default()
	}
	return &PullRequestChangedResolver{Svc: svc, Logger: logger}
}

// Subscribe returns a channel that emits the freshly-loaded PullRequest
// every time an invalidation event matches the given repo+number.
//
// R16: we re-fetch from the provider (which holds the post-drop cache
// state) after the invalidation event arrives — the cache was already
// dropped before the event was broadcast.
func (r *PullRequestChangedResolver) Subscribe(ctx context.Context, repo string, number int) (<-chan *PullRequest, error) {
	owner, name, err := SplitRepo(repo)
	if err != nil {
		return nil, fmt.Errorf("pullRequestChanged: malformed repo %q (want owner/name): %w", repo, err)
	}

	wantKey := fmt.Sprintf("PullRequest:%s/%s#%d", owner, name, number)
	events := r.Svc.Subscribe(ctx)
	out := make(chan *PullRequest, 1)

	go func() {
		defer func() {
			if rec := recover(); rec != nil {
				r.Logger.Error("gh: pullRequestChanged subscription panic",
					slog.String("repo", repo),
					slog.Int("number", number),
					slog.Any("panic", rec),
					slog.String("stack", string(debug.Stack())))
			}
			close(out)
		}()
		for {
			select {
			case <-ctx.Done():
				return
			case ev, ok := <-events:
				if !ok {
					return
				}
				if ev.Key != wantKey {
					continue
				}
				pr, err := r.Svc.GetPullRequest(ctx, PullRequestKey{Owner: owner, Name: name, Number: number})
				if err != nil {
					r.Logger.Warn("gh: pullRequestChanged re-fetch failed",
						slog.String("key", wantKey),
						slog.String("err", err.Error()))
					continue
				}
				select {
				case out <- &pr:
				default:
					r.Logger.Warn("gh: pullRequestChanged subscriber lagging, dropping emit", slog.String("key", wantKey))
				}
			}
		}
	}()

	return out, nil
}

// RunChangedResolver drives Subscription.runChanged.
type RunChangedResolver struct {
	Svc    Service
	Logger *slog.Logger
}

// NewRunChangedResolver constructs the resolver.
func NewRunChangedResolver(svc Service, logger *slog.Logger) *RunChangedResolver {
	if logger == nil {
		logger = slog.Default()
	}
	return &RunChangedResolver{Svc: svc, Logger: logger}
}

// Subscribe returns a channel that emits WorkflowRun deltas when the
// provider invalidates a run matching repo+branch.
func (r *RunChangedResolver) Subscribe(ctx context.Context, repo string, branch string) (<-chan *WorkflowRun, error) {
	owner, name, err := SplitRepo(repo)
	if err != nil {
		return nil, fmt.Errorf("runChanged: malformed repo %q: %w", repo, err)
	}

	repoPrefix := fmt.Sprintf("WorkflowRun:%s/%s#", owner, name)
	events := r.Svc.Subscribe(ctx)
	out := make(chan *WorkflowRun, 1)

	go func() {
		defer func() {
			if rec := recover(); rec != nil {
				r.Logger.Error("gh: runChanged subscription panic",
					slog.String("repo", repo),
					slog.String("branch", branch),
					slog.Any("panic", rec),
					slog.String("stack", string(debug.Stack())))
			}
			close(out)
		}()
		for {
			select {
			case <-ctx.Done():
				return
			case ev, ok := <-events:
				if !ok {
					return
				}
				if !strings.HasPrefix(ev.Key, repoPrefix) {
					continue
				}
				// Re-fetch and filter by branch.
				runs, err := r.Svc.ListWorkflowRuns(ctx, owner, name)
				if err != nil {
					r.Logger.Warn("gh: runChanged re-fetch failed",
						slog.String("repo", repo),
						slog.String("err", err.Error()))
					continue
				}
				for i := range runs {
					run := runs[i]
					if run.HeadBranch != branch {
						continue
					}
					if run.ID() != ev.Key {
						continue
					}
					select {
					case out <- &run:
					default:
						r.Logger.Warn("gh: runChanged subscriber lagging, dropping emit", slog.String("key", ev.Key))
					}
					break
				}
			}
		}
	}()

	return out, nil
}
