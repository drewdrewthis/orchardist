// subscriptions.go — hostServiceChanged subscription.
//
// Per R16 events are emitted AFTER the cache write. Subscribers receive
// the updated HostServiceSnapshot in each event — not a stale pre-mutation
// value. The provider's fanOut fires after refreshOne updates the cache
// (see provider.go), so this layer just bridges the channel to the
// GraphQL subscription writer.
package hostservices

import (
	"context"
	"log/slog"
)

// SubscribeHostServiceChanged subscribes to invalidation events for all
// watched services and calls onChange with the updated resolver each time
// a service snapshot changes.
//
// Runs until ctx is cancelled. Wraps the event loop in panic-recover per
// R17 — a panicking subscription writer must not kill the daemon.
func SubscribeHostServiceChanged(
	ctx context.Context,
	svc ServiceReader,
	onChange func(*HostServiceResolver),
) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("hostservices: SubscribeHostServiceChanged panic", "recovered", r)
		}
	}()

	ch := svc.Subscribe(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-ch:
			if !ok {
				return
			}
			// Fetch the UPDATED snapshot from the service (R16 — emit after write).
			snap, err := svc.ByID(ctx, ev.Key)
			if err != nil {
				slog.Warn("hostservices: subscription fetch failed", "key", ev.Key, "err", err)
				continue
			}
			onChange(&HostServiceResolver{Snap: snap})
		}
	}
}
