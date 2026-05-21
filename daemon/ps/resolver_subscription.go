package ps

import (
	"context"
	"log/slog"
)

// SubscriptionResolver implements Subscription.processes — pushes a
// snapshot of every cached process whenever the ps provider invalidates
// (R6: one file per concern, R16: emit AFTER cache write).
//
// The provider's fanout channel carries an invalidationEvent per changed
// key. The subscription handler drains these events and pushes a full
// snapshot — the snapshot is the post-write state because the provider
// calls replaceAll() before fanout() (R16).
type SubscriptionResolver struct {
	svc    Service
	logger *slog.Logger
}

// NewSubscriptionResolver constructs a SubscriptionResolver.
func NewSubscriptionResolver(svc Service, logger *slog.Logger) *SubscriptionResolver {
	if logger == nil {
		logger = slog.Default()
	}
	return &SubscriptionResolver{svc: svc, logger: logger}
}

// Processes implements Subscription.processes: [Process!]!
//
// Emits a fresh snapshot of every cached process whenever the provider
// invalidates. Subscribers that want live process lists use this;
// subscribers that want a single process use Host.processes(filter:{pidIn}).
//
// Per R16: the snapshot is read AFTER the cache write because the
// provider writes the cache, then fans out the event. Fast path:
//   1. Provider calls replaceAll (cache write).
//   2. Provider calls fanout (emits event).
//   3. This goroutine receives the event, reads cache → snapshot.
//
// Per R17: the goroutine is wrapped in a panic-recover.
func (r *SubscriptionResolver) Processes(ctx context.Context) (<-chan []*ProcessProjection, error) {
	if r.svc == nil {
		return nil, errPSNotConfigured
	}
	src := r.svc.Subscribe(ctx)
	hostID := r.svc.HostID()
	out := make(chan []*ProcessProjection, 1)

	go func() {
		defer func() {
			if rc := recover(); rc != nil {
				r.logger.Error("ps: subscription goroutine panic", "panic", rc)
			}
			close(out)
		}()
		for {
			select {
			case <-ctx.Done():
				return
			case _, ok := <-src:
				if !ok {
					return
				}
				// Read the cache AFTER the invalidation event (R16).
				snap := r.svc.List()
				procs := make([]*ProcessProjection, 0, len(snap))
				for i := range snap {
					procs = append(procs, ProjectProcess(&snap[i], hostID))
				}
				select {
				case out <- procs:
				case <-ctx.Done():
					return
				}
			}
		}
	}()

	return out, nil
}

// errPSNotConfigured is returned when the ps service was not wired.
var errPSNotConfigured = errorString("ps: service not configured")

// errorString is a simple string error (R8: one error style per module).
type errorString string

func (e errorString) Error() string { return string(e) }
