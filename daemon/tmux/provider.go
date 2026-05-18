// provider.go wires the tmux Adapter, in-memory Stores, and the watch loop
// into the TmuxService surface. Per the constitution (R2) consumers import
// TmuxService; provider.go is internal to this package.
package tmux

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// DefaultPollInterval is the watcher tick rate. Tunable via WithPollInterval.
const DefaultPollInterval = 1 * time.Second

// ActiveAttachedWindow is how recent a client's last_activity must be for
// TmuxSession.activeAttached to be true.
const ActiveAttachedWindow = 5 * time.Minute

// Provider is the concrete TmuxService implementation. It owns the in-memory
// caches (one per node type), the poll/watch loop, and invalidation fan-out.
// All exported methods satisfy TmuxService (R2).
//
// R13: server uses RWMutex (read-heavy). subsMu uses Mutex (balanced).
type Provider struct {
	adapter      *Adapter
	pollInterval time.Duration
	logger       *slog.Logger

	// Per-type in-memory caches (RWMutex inside each store).
	sessionsMu sync.RWMutex
	sessions   map[SessionKey]Session

	windowsMu sync.RWMutex
	windows   map[WindowKey]Window

	panesMu sync.RWMutex
	panes   map[PaneKey]Pane

	clientsMu sync.RWMutex
	clients   map[ClientKey]Client

	serverMu sync.RWMutex
	server   ServerInfo

	// Subscription fan-out.
	subsMu sync.Mutex
	subs   []chan SessionChangeEvent

	tickerSignal chan struct{}

	// Observability: how many times has the batch-fetch fired? (O4)
	fetchCount atomic.Int64
}

// New constructs a Provider for the given adapter. Call Start to begin polling.
func New(a *Adapter, logger *slog.Logger) *Provider {
	if logger == nil {
		logger = slog.Default()
	}
	return &Provider{
		adapter:      a,
		pollInterval: DefaultPollInterval,
		logger:       logger,
		sessions:     make(map[SessionKey]Session),
		windows:      make(map[WindowKey]Window),
		panes:        make(map[PaneKey]Pane),
		clients:      make(map[ClientKey]Client),
		tickerSignal: make(chan struct{}, 1),
	}
}

// WithPollInterval lets tests bypass the 1s default.
func (p *Provider) WithPollInterval(d time.Duration) *Provider {
	if d > 0 {
		p.pollInterval = d
	}
	return p
}

// Adapter exposes the wrapped adapter for tests / introspection.
func (p *Provider) Adapter() *Adapter { return p.adapter }

// --- TmuxService implementation ---

// Host returns the host id baked into every key the adapter emits.
func (p *Provider) Host() HostID { return p.adapter.Host() }

// Server returns the cached ServerInfo. Fast — no shellout.
func (p *Provider) Server() ServerInfo {
	p.serverMu.RLock()
	defer p.serverMu.RUnlock()
	return p.server
}

// PaneByID returns the pane matching (host, paneID) from cache. R3-clean.
func (p *Provider) PaneByID(host, paneID string) (Pane, bool) {
	p.panesMu.RLock()
	defer p.panesMu.RUnlock()
	pn, ok := p.panes[PaneKey{Host: HostID(host), PaneID: paneID}]
	return pn, ok
}

// SessionByName returns the session matching (host, name) from cache.
func (p *Provider) SessionByName(host, name string) (Session, bool) {
	p.sessionsMu.RLock()
	defer p.sessionsMu.RUnlock()
	s, ok := p.sessions[SessionKey{Host: HostID(host), Name: name}]
	return s, ok
}

// WindowByKey returns the window matching (host, session, index) from cache.
func (p *Provider) WindowByKey(host, session string, index int) (Window, bool) {
	p.windowsMu.RLock()
	defer p.windowsMu.RUnlock()
	w, ok := p.windows[WindowKey{Host: HostID(host), Session: session, Index: index}]
	return w, ok
}

// ClientByName returns the client matching (host, clientName) from cache.
func (p *Provider) ClientByName(host, clientName string) (Client, bool) {
	p.clientsMu.RLock()
	defer p.clientsMu.RUnlock()
	c, ok := p.clients[ClientKey{Host: HostID(host), ClientName: clientName}]
	return c, ok
}

// AllSessions returns a snapshot copy of all cached sessions.
func (p *Provider) AllSessions() []Session {
	p.sessionsMu.RLock()
	defer p.sessionsMu.RUnlock()
	out := make([]Session, 0, len(p.sessions))
	for _, s := range p.sessions {
		out = append(out, s)
	}
	return out
}

// AllPanes returns a snapshot copy of all cached panes.
func (p *Provider) AllPanes() []Pane {
	p.panesMu.RLock()
	defer p.panesMu.RUnlock()
	out := make([]Pane, 0, len(p.panes))
	for _, pn := range p.panes {
		out = append(out, pn)
	}
	return out
}

// AllClients returns a snapshot copy of all cached clients.
func (p *Provider) AllClients() []Client {
	p.clientsMu.RLock()
	defer p.clientsMu.RUnlock()
	out := make([]Client, 0, len(p.clients))
	for _, c := range p.clients {
		out = append(out, c)
	}
	return out
}

// AllWindows returns a snapshot copy of all cached windows.
func (p *Provider) AllWindows() []Window {
	p.windowsMu.RLock()
	defer p.windowsMu.RUnlock()
	out := make([]Window, 0, len(p.windows))
	for _, w := range p.windows {
		out = append(out, w)
	}
	return out
}

// PanesByCwd returns panes by foreground-process cwd. ADR-022 axis: PanesByCwd.
func (p *Provider) PanesByCwd(host, cwd string, ps PanePsGetter) []Pane {
	if cwd == "" {
		return []Pane{}
	}
	panes := p.AllPanes()
	var out []Pane
	for _, pn := range panes {
		if string(pn.Key.Host) != host || pn.CurrentPid <= 0 {
			continue
		}
		paneCwd := ps.CwdForPid(host, pn.CurrentPid)
		if paneCwd == "" {
			continue
		}
		if paneCwd != cwd && !strings.HasPrefix(paneCwd, cwd+"/") {
			continue
		}
		out = append(out, pn)
	}
	if out == nil {
		return []Pane{}
	}
	return out
}

// PanesByCommand returns panes by foreground command basename. ADR-022 axis: PanesByCommand.
func (p *Provider) PanesByCommand(host, basenameContains string, ps PanePsGetter) []Pane {
	if basenameContains == "" {
		return []Pane{}
	}
	needle := strings.ToLower(basenameContains)
	panes := p.AllPanes()
	var out []Pane
	for _, pn := range panes {
		if string(pn.Key.Host) != host {
			continue
		}
		if paneCommandMatches(pn, host, ps, needle) {
			out = append(out, pn)
		}
	}
	if out == nil {
		return []Pane{}
	}
	return out
}

// PanesBySession returns every pane in a given tmux session.
func (p *Provider) PanesBySession(host, sessionName string) []Pane {
	if sessionName == "" {
		return []Pane{}
	}
	panes := p.AllPanes()
	var out []Pane
	for _, pn := range panes {
		if string(pn.Key.Host) != host || pn.WindowKey.Session != sessionName {
			continue
		}
		out = append(out, pn)
	}
	if out == nil {
		return []Pane{}
	}
	return out
}

// CapturePane shells out via the adapter; not cached.
func (p *Provider) CapturePane(ctx context.Context, key PaneKey, start, end int, full, stripAnsi bool) (string, error) {
	return p.adapter.CapturePane(ctx, key, start, end, full, stripAnsi)
}

// CapturePaneTail wraps the adapter's tail-capture.
func (p *Provider) CapturePaneTail(ctx context.Context, key PaneKey, lines int, stripAnsi bool) (string, error) {
	return p.adapter.CapturePaneTail(ctx, key, lines, stripAnsi)
}

// Subscribe returns a <-chan that receives SessionChangeEvent after each cache write.
// The channel is closed when ctx is done. R12: returns <-chan, not bare chan. R16: event
// is emitted after the cache write completes in refresh().
func (p *Provider) Subscribe(ctx context.Context) <-chan SessionChangeEvent {
	ch := make(chan SessionChangeEvent, 8)
	p.subsMu.Lock()
	p.subs = append(p.subs, ch)
	p.subsMu.Unlock()
	go func() {
		<-ctx.Done()
		p.subsMu.Lock()
		defer p.subsMu.Unlock()
		for i, c := range p.subs {
			if c == ch {
				p.subs = append(p.subs[:i], p.subs[i+1:]...)
				close(ch)
				return
			}
		}
	}()
	return ch
}

// PokeRefresh forces an immediate refresh outside the regular tick.
func (p *Provider) PokeRefresh() {
	select {
	case p.tickerSignal <- struct{}{}:
	default:
	}
}

// Start performs the initial fetch and starts the background poll loop.
// Returns the first-fetch error so the daemon sees a broken environment at boot.
func (p *Provider) Start(ctx context.Context) error {
	if err := p.refresh(ctx); err != nil {
		return fmt.Errorf("tmux provider: initial fetch: %w", err)
	}
	go p.pollLoop(ctx)
	return nil
}

// Refresh runs an immediate fetch. Useful in tests.
func (p *Provider) Refresh(ctx context.Context) error { return p.refresh(ctx) }

// FetchCount returns the number of adapter FetchAll calls made. O4 observability.
func (p *Provider) FetchCount() int64 { return p.fetchCount.Load() }

// refresh performs one full adapter fetch and updates all caches atomically per type.
// The subscription fanout fires AFTER all stores are updated (R16).
func (p *Provider) refresh(ctx context.Context) error {
	p.fetchCount.Add(1)
	snap, err := p.adapter.FetchAll(ctx)
	if err != nil {
		return err
	}

	now := time.Now()

	p.serverMu.Lock()
	p.server = snap.Server
	p.serverMu.Unlock()

	sessChanged := p.replaceSessions(snap.Sessions)
	p.replaceWindows(snap.Windows)
	p.replacePanes(snap.Panes)
	p.replaceClients(snap.Clients)

	// Emit AFTER all cache writes (R16).
	if sessChanged {
		ev := SessionChangeEvent{Reason: "poll", At: now}
		p.fanout(ev)
	}
	return nil
}

// replaceSessions atomically swaps the sessions map. Returns true when the
// map contents changed (used to gate subscription fanout).
func (p *Provider) replaceSessions(next map[SessionKey]Session) bool {
	p.sessionsMu.Lock()
	defer p.sessionsMu.Unlock()
	changed := !sessionMapsEqual(p.sessions, next)
	p.sessions = next
	return changed
}

func (p *Provider) replaceWindows(next map[WindowKey]Window) {
	p.windowsMu.Lock()
	p.windows = next
	p.windowsMu.Unlock()
}

func (p *Provider) replacePanes(next map[PaneKey]Pane) {
	p.panesMu.Lock()
	p.panes = next
	p.panesMu.Unlock()
}

func (p *Provider) replaceClients(next map[ClientKey]Client) {
	p.clientsMu.Lock()
	p.clients = next
	p.clientsMu.Unlock()
}

// pollLoop runs the poll ticker and ticker-signal loop. R17: top-level
// goroutine has panic-recover + structured logging so a panic in FetchAll
// doesn't silently kill the read path.
func (p *Provider) pollLoop(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			p.logger.Error("tmux provider: poll loop panic", "panic", r)
		}
	}()
	t := time.NewTicker(p.pollInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := p.refresh(ctx); err != nil {
				p.logger.Warn("tmux provider: refresh failed", "err", err)
			}
		case <-p.tickerSignal:
			if err := p.refresh(ctx); err != nil {
				p.logger.Warn("tmux provider: forced refresh failed", "err", err)
			}
		}
	}
}

// fanout sends ev to all current subscribers. Best-effort (drops on full buffer).
func (p *Provider) fanout(ev SessionChangeEvent) {
	p.subsMu.Lock()
	subs := append([]chan SessionChangeEvent(nil), p.subs...)
	p.subsMu.Unlock()
	for _, c := range subs {
		select {
		case c <- ev:
		default:
		}
	}
}

// --- Equality helpers ---

func sessionMapsEqual(a, b map[SessionKey]Session) bool {
	if len(a) != len(b) {
		return false
	}
	for k, av := range a {
		bv, ok := b[k]
		if !ok || !sessionsEqual(av, bv) {
			return false
		}
	}
	return true
}

func sessionsEqual(a, b Session) bool {
	return a.Key == b.Key &&
		a.Attached == b.Attached &&
		a.AttachedCount == b.AttachedCount &&
		a.WindowCount == b.WindowCount &&
		a.CurrentWindow == b.CurrentWindow &&
		a.LastActivityAt.Equal(b.LastActivityAt) &&
		a.CreatedAt.Equal(b.CreatedAt)
}

// paneCommandMatches checks whether a pane's foreground command contains needle.
// Prefers the ps-resolved basename; falls back to tmux pane_current_command.
func paneCommandMatches(pn Pane, host string, ps PanePsGetter, needle string) bool {
	if ps != nil && pn.CurrentPid > 0 {
		cmd := ps.CommandForPid(host, pn.CurrentPid)
		if cmd != "" {
			return strings.Contains(strings.ToLower(filepath.Base(cmd)), needle)
		}
	}
	return strings.Contains(strings.ToLower(filepath.Base(pn.CurrentCommand)), needle)
}

// --- Compile-time assertions ---

var _ TmuxService = (*Provider)(nil)
