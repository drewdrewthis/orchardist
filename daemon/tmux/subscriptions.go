// subscriptions.go implements the tmux domain's GraphQL subscriptions.
//
// R16: subscriptions emit AFTER the cache write completes. The Provider.refresh()
// method writes all stores before calling fanout(), and fanout() fires the
// subscription channels. Subscribers reading AllSessions() after receipt see
// the post-mutation state, never pre-mutation state.
//
// S7: tmuxSessionsChanged emits the full (bounded) session list. The list
// is bounded (tmux sessions per server are in the dozens at most), so a full
// list is acceptable per the S7 note "Small bounded lists OK as arrays".
package tmux

import (
	"context"
	"fmt"
)

// SubscriptionResolvers holds the tmux subscription resolver implementations.
type SubscriptionResolvers struct {
	Svc TmuxService
}

// TmuxSessionsChanged resolves Subscription.tmuxSessionsChanged.
//
// Returns a channel that emits a fresh snapshot of all cached sessions
// whenever the tmux provider's session cache changes. The channel is closed
// when ctx is cancelled (client disconnects).
//
// R16: the Provider emits on the SessionChangeEvent channel AFTER all store
// writes are committed. By the time this goroutine reads AllSessions(), the
// cache is guaranteed to contain the new state.
func (r *SubscriptionResolvers) TmuxSessionsChanged(ctx context.Context) (<-chan []*TmuxSessionNode, error) {
	if r.Svc == nil {
		return nil, errTmuxNotConfigured
	}

	src := r.Svc.Subscribe(ctx)
	out := make(chan []*TmuxSessionNode, 1)

	go func() {
		defer close(out)
		for {
			select {
			case <-ctx.Done():
				return
			case _, ok := <-src:
				if !ok {
					return
				}
				// R16: AllSessions() is called AFTER receipt of the change event.
				// Provider.fanout() fires AFTER all store writes, so this read
				// sees the post-change state.
				sessions := r.Svc.AllSessions()
				nodes := make([]*TmuxSessionNode, len(sessions))
				for i, s := range sessions {
					nodes[i] = projectSessionNode(s)
				}
				select {
				case out <- nodes:
				case <-ctx.Done():
					return
				}
			}
		}
	}()

	return out, nil
}

var errTmuxNotConfigured = fmt.Errorf("tmux provider not configured")
