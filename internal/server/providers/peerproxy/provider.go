package peerproxy

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// InvalidationEvent is the resolver-facing notification that a node on
// a peer may have changed. Mirrors the shape every other provider
// emits so the resolver layer (and the future DataLoader-driven
// Subscription path) can treat federation indifferently.
type InvalidationEvent struct {
	NodeID NodeID
	Peer   string
	Reason string
	At     time.Time
}

// Provider routes node-id reads to the right peer adapter and fans
// per-peer subscription streams out to local subscribers.
//
// Per ADR-011 §7 federation is "just another provider" — this struct
// keeps that promise. Resolvers use Get / Subscribe; the provider
// hides the per-peer machinery.
type Provider struct {
	logger *slog.Logger

	mu          sync.RWMutex
	adapters    map[string]*PeerAdapter
	clients     map[string]*Client
	peerCancels map[string]context.CancelFunc
	spawnCounts map[string]int // incremented each time startPeer is called; test seam

	subMu sync.Mutex
	subs  map[chan InvalidationEvent]struct{}

	startCtx    context.Context
	startCancel context.CancelFunc
	wg          sync.WaitGroup
	started     bool
	closed      bool

	// opts holds construction-time options so AddPeer can reuse them
	// when building clients for dynamically-added peers.
	opts providerOptions
}

// ProviderOption configures a Provider at construction time. The only
// option today (`WithTLSConfig`) lets callers — most importantly tests
// against `httptest.NewTLSServer` — supply a custom *tls.Config without
// having to override `http.DefaultClient` globally.
type ProviderOption func(*providerOptions)

type providerOptions struct {
	tlsConfig *tls.Config
}

// WithTLSConfig overrides the *tls.Config the Provider's per-peer
// Clients use when peer.TLS is true. Production code should leave this
// unset (the default trust store applies); tests pass the cert bundle
// from `httptest.NewTLSServer().Client().Transport` so the self-signed
// cert is accepted.
//
// Callers MUST NOT set `InsecureSkipVerify: true` outside tests — the
// daemon's only defence against MITM on a TLS peer is cert verification.
func WithTLSConfig(cfg *tls.Config) ProviderOption {
	return func(o *providerOptions) { o.tlsConfig = cfg }
}

// NewProvider constructs a provider from a fully-loaded
// FederationConfig. The provider does not read the config file itself;
// the daemon owns the config-loading lifecycle.
//
// Each peer gets its own Client — websocket multiplexing is per-
// connection, and one connection per peer keeps the failure model
// simple.
func NewProvider(cfg FederationConfig, logger *slog.Logger, opts ...ProviderOption) *Provider {
	if logger == nil {
		logger = slog.Default()
	}
	options := providerOptions{}
	for _, opt := range opts {
		opt(&options)
	}
	p := &Provider{
		logger:      logger,
		adapters:    make(map[string]*PeerAdapter, len(cfg.Peers)),
		clients:     make(map[string]*Client, len(cfg.Peers)),
		peerCancels: make(map[string]context.CancelFunc, len(cfg.Peers)),
		spawnCounts: make(map[string]int, len(cfg.Peers)),
		subs:        map[chan InvalidationEvent]struct{}{},
		opts:        options,
	}
	for _, peer := range cfg.Peers {
		client := buildClient(peer, options.tlsConfig)
		p.adapters[peer.Name] = NewPeerAdapter(peer, client)
		p.clients[peer.Name] = client
	}
	return p
}

// buildClient assembles a per-peer Client. When peer.TLS is true and
// tlsCfg is non-nil, the resulting http.Client + websocket.Dialer share
// the supplied tls.Config; otherwise the package defaults apply.
func buildClient(peer PeerConfig, tlsCfg *tls.Config) *Client {
	if !peer.TLS || tlsCfg == nil {
		return NewClient(peer.Address, peer.TLS)
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = tlsCfg
	httpc := &http.Client{Transport: transport}
	dialer := *websocket.DefaultDialer
	dialer.TLSClientConfig = tlsCfg
	return newClient(peer.Address, peer.TLS, httpc, &dialer, time.Now)
}

// Start kicks off the per-peer probe + subscription goroutines. Safe
// to call once; subsequent calls are no-ops. Stop tears them down.
func (p *Provider) Start(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.started || p.closed {
		return nil
	}
	p.started = true
	p.startCtx, p.startCancel = context.WithCancel(ctx)
	for _, a := range p.adapters {
		p.startPeer(a)
	}
	return nil
}

// startPeer spawns the runPeer goroutine for a single adapter with its
// own child context derived from startCtx. The per-peer cancel is stored
// in peerCancels so RemovePeer can cancel it independently.
//
// Callers MUST hold p.mu when writing peerCancels, or call startPeer
// only from Start (which does its own locking). AddPeer holds the lock
// before calling startPeer.
func (p *Provider) startPeer(a *PeerAdapter) {
	ctx, cancel := context.WithCancel(p.startCtx)
	p.peerCancels[a.peer.Name] = cancel
	p.spawnCounts[a.peer.Name]++
	p.wg.Add(1)
	go p.runPeer(ctx, a)
}

// AddPeer dynamically inserts a new peer into the running provider and
// begins probing it. Returns an error when the name is already present
// (use RemovePeer first) or when Start has not been called yet.
//
// AddPeer is safe to call concurrently with other AddPeer / RemovePeer
// invocations.
func (p *Provider) AddPeer(peer PeerConfig) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if !p.started {
		return fmt.Errorf("AddPeer: provider not started — call Start first")
	}
	if _, exists := p.adapters[peer.Name]; exists {
		return fmt.Errorf("AddPeer: peer %q already exists", peer.Name)
	}

	client := buildClient(peer, p.opts.tlsConfig)
	adapter := NewPeerAdapter(peer, client)
	p.adapters[peer.Name] = adapter
	p.clients[peer.Name] = client
	p.startPeer(adapter)
	return nil
}

// RemovePeer cancels a peer's probe goroutine, removes its entries from
// the provider's internal maps, and closes its client transport.
//
// Returns an error when no peer with the given name is configured.
// RemovePeer is safe to call concurrently with AddPeer / RemovePeer.
func (p *Provider) RemovePeer(name string) error {
	p.mu.Lock()
	_, exists := p.adapters[name]
	if !exists {
		p.mu.Unlock()
		return fmt.Errorf("RemovePeer: peer %q not found", name)
	}
	cancel := p.peerCancels[name]
	client := p.clients[name]
	delete(p.adapters, name)
	delete(p.clients, name)
	delete(p.peerCancels, name)
	p.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if client != nil {
		_ = client.Close()
	}
	return nil
}

// Stop cancels the start context, waits for goroutines to drain, and
// closes every transport client. Safe to call repeatedly.
func (p *Provider) Stop() error {
	p.mu.Lock()
	if !p.started || p.closed {
		p.closed = true
		p.mu.Unlock()
		return nil
	}
	p.closed = true
	cancel := p.startCancel
	clients := make([]*Client, 0, len(p.clients))
	for _, c := range p.clients {
		clients = append(clients, c)
	}
	p.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	p.wg.Wait()

	// p.subs is owned by p.subMu (broadcast() reads it under that lock).
	// Snapshot and reset under the same lock to avoid racing with
	// broadcast() during the wg.Wait drain.
	p.subMu.Lock()
	subs := p.subs
	p.subs = map[chan InvalidationEvent]struct{}{}
	for ch := range subs {
		close(ch)
	}
	p.subMu.Unlock()

	for _, c := range clients {
		_ = c.Close()
	}
	return nil
}

// Peers returns the configured peer rows in deterministic (config-
// declared) order. Resolvers use this to populate `Host.peers` without
// caring how the underlying transport works.
func (p *Provider) Peers() []PeerConfig {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]PeerConfig, 0, len(p.adapters))
	for _, a := range p.adapters {
		out = append(out, a.Peer())
	}
	// adapter map iteration is unordered; sort by name for stable output.
	sortPeers(out)
	return out
}

// Reachability returns the last-known reachable bit for a peer. The
// boolean is false until the first probe succeeds — matches what users
// see in the dashboard before the daemon has had a chance to talk.
func (p *Provider) Reachability(name string) (reachable bool, lastReachedAt time.Time, ok bool) {
	p.mu.RLock()
	a, present := p.adapters[name]
	p.mu.RUnlock()
	if !present {
		return false, time.Time{}, false
	}
	r, t := a.Reachable()
	return r, t, true
}

// HasPeer returns true when name is a configured peer.
func (p *Provider) HasPeer(name string) bool {
	p.mu.RLock()
	_, ok := p.adapters[name]
	p.mu.RUnlock()
	return ok
}

// SpawnCount returns the number of times startPeer has been called for name.
// It is intended for tests that need to verify a peer's goroutine was NOT
// restarted across an ApplyPeers call — a count of 1 means the goroutine
// was spawned exactly once and left alone.
func (p *Provider) SpawnCount(name string) int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.spawnCounts[name]
}

// ApplyPeers diffs the current peer set against cfg and issues the minimum
// AddPeer / RemovePeer calls needed to converge on the new config.
//
// Algorithm (name-based diff only — this is the first pass):
//   - Peers in cfg but not in the current set → AddPeer.
//   - Peers in the current set but not in cfg → RemovePeer.
//   - Peers present in both sets → left untouched (goroutine is NOT restarted).
//
// Future passes will tighten the equality check: if address or TLS differ,
// the peer is treated as a remove+add pair. Structure the "both sets" branch
// to accommodate that — the equality predicate is the only thing that changes.
//
// Error policy: best-effort. If one AddPeer or RemovePeer fails, the error is
// collected and the rest of the diff is still applied. All errors are joined
// and returned at the end. This ensures a single misconfigured peer does not
// block convergence for the others.
//
// ApplyPeers is safe to call concurrently with AddPeer / RemovePeer.
func (p *Provider) ApplyPeers(cfg FederationConfig) error {
	// Snapshot the current peer names without holding the lock during
	// AddPeer/RemovePeer calls — those methods acquire p.mu.Lock() themselves,
	// and holding p.mu.RLock() here while calling them would deadlock.
	p.mu.RLock()
	currentNames := make(map[string]struct{}, len(p.adapters))
	for name := range p.adapters {
		currentNames[name] = struct{}{}
	}
	p.mu.RUnlock()

	newNames := make(map[string]struct{}, len(cfg.Peers))
	for _, peer := range cfg.Peers {
		newNames[peer.Name] = struct{}{}
	}

	var errs []error

	// Add peers that are new.
	for _, peer := range cfg.Peers {
		if _, exists := currentNames[peer.Name]; !exists {
			if err := p.AddPeer(peer); err != nil {
				errs = append(errs, err)
			}
		}
		// Peers in both sets: leave untouched for now.
		// Future: if peer.Address != current.Address || peer.TLS != current.TLS → remove+add.
	}

	// Remove peers that are no longer in the config.
	for name := range currentNames {
		if _, exists := newNames[name]; !exists {
			if err := p.RemovePeer(name); err != nil {
				errs = append(errs, err)
			}
		}
	}

	return errors.Join(errs...)
}

// Get implements the transparent forwarder side of Provider[NodeID,
// Node]. The resolver calls this whenever `Query.node(id)` resolves
// against an id whose host segment matches a configured peer.
//
// The peer is selected from the id's host segment (HostFromNodeID).
// Unknown peers return (nil, ErrUnknownPeer); ids without a host
// segment also return ErrUnknownPeer so callers cannot accidentally
// route local ids through the proxy.
func (p *Provider) Get(ctx context.Context, id NodeID) (*PeerNode, error) {
	host := HostFromNodeID(string(id))
	if host == "" {
		return nil, fmt.Errorf("node id %q lacks a host segment", id)
	}
	p.mu.RLock()
	a, ok := p.adapters[host]
	p.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("unknown peer %q", host)
	}
	return a.FetchNode(ctx, id)
}

// Query forwards an arbitrary GraphQL query to a configured peer and
// returns the decoded result. Used by federation-aware resolvers (e.g.
// `Host.processes`) that need richer shapes than the node-id forwarder
// provides.
//
// Returns an error when the peer is not configured. The result's Errors
// slice (if any) is left for the caller to inspect — peerproxy does not
// translate GraphQL errors into Go errors here, since some callers want
// to surface partial data.
func (p *Provider) Query(ctx context.Context, peer string, query string, vars map[string]any) (QueryResult, error) {
	p.mu.RLock()
	c, ok := p.clients[peer]
	p.mu.RUnlock()
	if !ok {
		return QueryResult{}, fmt.Errorf("unknown peer %q", peer)
	}
	return c.Query(ctx, query, vars)
}

// Subscribe returns a channel that emits InvalidationEvent for every
// node any peer pushes. Closing ctx (or calling Stop) tears the
// subscription down.
func (p *Provider) Subscribe(ctx context.Context) <-chan InvalidationEvent {
	ch := make(chan InvalidationEvent, 16)
	p.subMu.Lock()
	p.subs[ch] = struct{}{}
	p.subMu.Unlock()

	go func() {
		<-ctx.Done()
		p.subMu.Lock()
		if _, ok := p.subs[ch]; ok {
			delete(p.subs, ch)
			close(ch)
		}
		p.subMu.Unlock()
	}()
	return ch
}

// SubscribePeer returns a stream of events for a single peer. Used by
// the GraphQL `Subscription.peer(host:)` resolver — the frontend cares
// about one peer at a time.
//
// The returned channel closes when ctx is cancelled, the peer is
// unknown, or the peer's underlying websocket dies.
func (p *Provider) SubscribePeer(ctx context.Context, host string) (<-chan InvalidationEvent, error) {
	p.mu.RLock()
	a, ok := p.adapters[host]
	p.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("unknown peer %q", host)
	}
	stream, err := a.Subscribe(ctx)
	if err != nil {
		return nil, err
	}
	out := make(chan InvalidationEvent, 16)
	go func() {
		defer close(out)
		for ev := range stream {
			select {
			case out <- InvalidationEvent{
				NodeID: ev.NodeID,
				Peer:   ev.Peer,
				Reason: ev.Reason,
				At:     ev.At,
			}:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out, nil
}

// runPeer is the per-peer supervisor goroutine. It probes once
// immediately, then re-probes on a coarse interval, and keeps a
// subscription open. The loop is intentionally simple — peerproxy is
// a thin transport layer, not a state machine.
func (p *Provider) runPeer(ctx context.Context, a *PeerAdapter) {
	defer p.wg.Done()

	const probeInterval = 30 * time.Second
	const subRetryDelay = 5 * time.Second

	doProbe := func() {
		probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		if err := a.Probe(probeCtx); err != nil {
			p.logger.Debug("peer unreachable", "peer", a.peer.Name, "err", err)
		}
	}

	doProbe()

	probeTicker := time.NewTicker(probeInterval)
	defer probeTicker.Stop()

	for {
		stream, err := a.Subscribe(ctx)
		if err != nil {
			p.logger.Debug("peer subscribe failed", "peer", a.peer.Name, "err", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(subRetryDelay):
				continue
			}
		}

	streamLoop:
		for {
			select {
			case <-ctx.Done():
				return
			case <-probeTicker.C:
				doProbe()
			case ev, ok := <-stream:
				if !ok {
					break streamLoop
				}
				p.broadcast(InvalidationEvent{
					NodeID: ev.NodeID,
					Peer:   ev.Peer,
					Reason: ev.Reason,
					At:     ev.At,
				})
			}
		}
	}
}

// broadcast fans an event out to every subscriber. Drops on full
// buffers — the watcher goroutine cannot stall on a slow consumer.
func (p *Provider) broadcast(ev InvalidationEvent) {
	p.subMu.Lock()
	defer p.subMu.Unlock()
	for ch := range p.subs {
		select {
		case ch <- ev:
		default:
			p.logger.Warn("peerproxy: subscriber lagging, dropping event",
				"peer", ev.Peer, "node", string(ev.NodeID))
		}
	}
}

// sortPeers sorts a peer slice in place by Name. Pulled out so unit
// tests can use the same comparator without depending on stdlib sort
// at the call site.
func sortPeers(peers []PeerConfig) {
	for i := 1; i < len(peers); i++ {
		for j := i; j > 0 && peers[j-1].Name > peers[j].Name; j-- {
			peers[j-1], peers[j] = peers[j], peers[j-1]
		}
	}
}
