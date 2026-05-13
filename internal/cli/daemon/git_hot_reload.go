package daemon

import (
	"context"
	"log/slog"
	"sync/atomic"

	configprovider "github.com/drewdrewthis/git-orchard-rs/internal/server/providers/config"
	gitprovider "github.com/drewdrewthis/git-orchard-rs/internal/server/providers/git"
)

// gitConfigSubscriber bridges configprovider.Provider invalidations to
// gitprovider.Provider.ApplyProjects so adds/removes of repos in
// ~/.orchard/config.json apply without a daemon restart (issue #571).
//
// The wire shape mirrors peerproxy.ConfigWatcher: a tiny goroutine
// listens on a Subscribe channel and re-runs the lister + diff each
// time the file changes. Coalescing comes "for free" from the underlying
// configprovider.Provider.run loop, which calls reload + emits one event
// per fsnotify burst.
type gitConfigSubscriber struct {
	lister      gitProjectLister
	gitProvider *gitprovider.Provider
	logger      *slog.Logger
	doneCh      chan struct{}

	// applyCount is incremented immediately before each ApplyProjects
	// call. Exposed via ApplyCount() as a test seam.
	applyCount atomic.Int64
}

func newGitConfigSubscriber(lister gitProjectLister, gitProvider *gitprovider.Provider, logger *slog.Logger) *gitConfigSubscriber {
	if logger == nil {
		logger = slog.Default()
	}
	return &gitConfigSubscriber{
		lister:      lister,
		gitProvider: gitProvider,
		logger:      logger,
		doneCh:      make(chan struct{}),
	}
}

// start spawns a goroutine that reads invalidations from events and
// calls ApplyProjects with the latest List result. Returns when ctx is
// cancelled or the events channel closes.
//
// start is not idempotent — call it once per subscriber.
func (s *gitConfigSubscriber) start(ctx context.Context, events <-chan configprovider.InvalidationEvent) {
	go s.run(ctx, events)
}

func (s *gitConfigSubscriber) run(ctx context.Context, events <-chan configprovider.InvalidationEvent) {
	defer close(s.doneCh)
	for {
		select {
		case <-ctx.Done():
			return
		case _, ok := <-events:
			if !ok {
				return
			}
			projs, err := s.snapshotProjects(ctx)
			if err != nil {
				s.logger.Warn("git hot-reload: lister failed; keeping existing projects",
					"err", err)
				continue
			}
			s.applyCount.Add(1)
			if err := s.gitProvider.ApplyProjects(projs); err != nil {
				s.logger.Warn("git hot-reload: ApplyProjects error after config reload",
					"err", err)
			}
		}
	}
}

// snapshotProjects calls the lister and converts the result into the
// git provider's Project shape.
func (s *gitConfigSubscriber) snapshotProjects(ctx context.Context) ([]gitprovider.Project, error) {
	repos, err := s.lister.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]gitprovider.Project, 0, len(repos))
	for _, r := range repos {
		out = append(out, gitprovider.Project{ID: string(r.ID), Dir: r.Path})
	}
	return out, nil
}

// ApplyCount returns the number of times ApplyProjects has been called.
// Tests rely on this to assert hot-reload fired exactly once per edit.
func (s *gitConfigSubscriber) ApplyCount() int {
	return int(s.applyCount.Load())
}

// close waits for the run goroutine to exit. Callers must cancel the
// context they passed to start before calling close — start exits when
// the context is cancelled OR the events channel closes.
func (s *gitConfigSubscriber) close() {
	<-s.doneCh
}
