package peerproxy

import (
	"context"
	"sync"
	"time"
)

// LocalInvalidator is a small broadcaster the daemon uses to publish
// any-source invalidations back through the federation surface. Without
// it, `Subscription.peer(host: "*")` would have nothing to emit on a
// leaf daemon — the existing per-provider Subscribe channels are
// per-domain and not aggregated anywhere.
//
// This is intentionally separate from Provider: the Provider is the
// *consumer* of remote events; LocalInvalidator is the *producer* of
// local events that get forwarded outward. Tests inject synthetic
// events through Push; the production daemon wires real provider
// subscriptions into Push as workstreams need it.
type LocalInvalidator struct {
	now func() time.Time

	mu   sync.Mutex
	subs map[chan InvalidationEvent]struct{}
}

// NewLocalInvalidator constructs an empty broker.
func NewLocalInvalidator() *LocalInvalidator {
	return &LocalInvalidator{
		now:  time.Now,
		subs: map[chan InvalidationEvent]struct{}{},
	}
}

// Push publishes an event to every active subscriber. Drops on full
// buffers — slow subscribers miss events but the producer never
// blocks.
func (l *LocalInvalidator) Push(ev InvalidationEvent) {
	if ev.At.IsZero() {
		ev.At = l.now()
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	for ch := range l.subs {
		select {
		case ch <- ev:
		default:
		}
	}
}

// Subscribe returns a channel that receives every Push for as long as
// ctx is alive. The channel closes when ctx fires.
func (l *LocalInvalidator) Subscribe(ctx context.Context) <-chan InvalidationEvent {
	ch := make(chan InvalidationEvent, 16)
	l.mu.Lock()
	l.subs[ch] = struct{}{}
	l.mu.Unlock()

	go func() {
		<-ctx.Done()
		l.mu.Lock()
		if _, ok := l.subs[ch]; ok {
			delete(l.subs, ch)
			close(ch)
		}
		l.mu.Unlock()
	}()
	return ch
}

// SubscriberCount reports the number of active subscribers — exported
// for tests that need to wait until the websocket has propagated the
// subscription frame to the resolver before pushing.
func (l *LocalInvalidator) SubscriberCount() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.subs)
}
