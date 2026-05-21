// Package tmux is the orchard daemon's tmux domain module.
//
// It owns: TmuxServer, TmuxSession, TmuxWindow, TmuxPane, TmuxClient.
// It serves: Query.tmuxServer, Query.tmuxSessions, Query.tmuxPanes, Query.tmux (pass-through).
// It publishes: Subscription.tmuxSessionsChanged.
// It writes: Mutation.sendTextToPane, Mutation.killPane, Mutation.newWindow.
//
// Architecture (R1, R2):
//
//	consumers → TmuxService (service.go) → Provider (provider.go) → Adapter (adapter.go) → `tmux` CLI
//
// Consumers import only this package's TmuxService interface. They do not reach
// into provider.go or adapter.go types directly (R2, R4, R5).
package tmux

import (
	"context"
	"time"
)

// TmuxService is the sole API consumers of this domain may call (R2).
//
// The concrete implementation is *Provider. Consumers depend on this interface
// (R4 ISP) and define narrow sub-interfaces (like PanePsGetter) in their own
// packages when they only need a slice of this surface.
type TmuxService interface {
	// Host returns the HostID baked into every node key this service emits.
	Host() HostID

	// Server returns the cached ServerInfo (socket path, pid, alive). Fast — no shellout.
	Server() ServerInfo

	// PaneByID looks up a pane by (host, paneID). ok=false when not found.
	PaneByID(host, paneID string) (Pane, bool)

	// SessionByName looks up a session by (host, name). ok=false when not found.
	SessionByName(host, name string) (Session, bool)

	// WindowByKey looks up a window by (host, session, index). ok=false when not found.
	WindowByKey(host, session string, index int) (Window, bool)

	// ClientByName looks up a client by (host, clientName). ok=false when not found.
	ClientByName(host, clientName string) (Client, bool)

	// AllSessions returns a snapshot copy of all cached sessions.
	// Allocates — use sparingly. Prefer PaneByID / SessionByName for field resolvers.
	AllSessions() []Session

	// AllPanes returns a snapshot copy of all cached panes.
	AllPanes() []Pane

	// AllClients returns a snapshot copy of all cached clients.
	AllClients() []Client

	// AllWindows returns a snapshot copy of all cached windows.
	AllWindows() []Window

	// PanesByCwd returns every pane on host whose foreground-process cwd equals
	// cwd exactly or has cwd+"/" as a prefix. psGetter resolves pids to cwds.
	// Implements the ADR-022 PanesByCwd axis (O1).
	PanesByCwd(host, cwd string, ps PanePsGetter) []Pane

	// PanesByCommand returns every pane on host whose foreground command basename
	// contains basenameContains (case-insensitive). Uses psGetter for real basename.
	// Implements the ADR-022 PanesByCommand axis (O1).
	PanesByCommand(host, basenameContains string, ps PanePsGetter) []Pane

	// PanesBySession returns every pane in a given tmux session.
	PanesBySession(host, sessionName string) []Pane

	// CapturePane captures pane output via `tmux capture-pane`. Not cached;
	// callers pay the per-call cost (schema documents this).
	CapturePane(ctx context.Context, key PaneKey, start, end int, full, stripAnsi bool) (string, error)

	// CapturePaneTail captures the last `lines` rows from pane output.
	CapturePaneTail(ctx context.Context, key PaneKey, lines int, stripAnsi bool) (string, error)

	// Subscribe returns a channel that receives an event each time the session
	// cache changes. The channel is closed when ctx is done. Used by
	// Subscription.tmuxSessionsChanged (R12 — returns <-chan, not bare chan).
	Subscribe(ctx context.Context) <-chan SessionChangeEvent

	// PokeRefresh forces the poll loop to refresh outside its tick.
	PokeRefresh()

	// Start performs the initial fetch and starts the background poll loop.
	Start(ctx context.Context) error
}

// SessionChangeEvent signals that the session cache has changed. Subscribers
// receive this and then call AllSessions() to read the new state (R16: emit
// after cache write, so AllSessions() always returns fresh data after receipt).
type SessionChangeEvent struct {
	// Reason is a human-readable poll/watch reason for logging.
	Reason string
	// At is when the change was observed.
	At time.Time
}

// PanePsGetter is the narrow ps surface this domain needs to resolve
// foreground-process cwd and command basename for pane axis lookups (R4).
//
// The ps domain's Provider satisfies this via its service interface;
// tests implement it inline.
type PanePsGetter interface {
	// CwdForPid returns the working directory of the process, or "" when unavailable.
	CwdForPid(host string, pid int) string
	// CommandForPid returns the command basename (e.g. "claude"), or "" when unavailable.
	CommandForPid(host string, pid int) string
}

// ClaudeInstanceRef is the minimal cross-domain payload the tmux domain needs
// to resolve TmuxPane.claudeInstance (S15b — the field is declared in this
// domain's schema partial; the derivation is in claude-instance).
//
// Returning a ref rather than a full ClaudeInstance keeps the tmux domain
// free of the claude-instance type graph (R5 anti-corruption layer).
type ClaudeInstanceRef struct {
	// ID is the stable GraphQL node id of the ClaudeInstance.
	ID string
	// SessionUUID is the Claude session UUID (from the jsonl tail).
	SessionUUID string
	// State is the instance state string (working|idle|input|stalled).
	State string
}

// ClaudeInstanceGetter is the narrow claude-instance surface this domain
// needs to resolve TmuxPane.claudeInstance (R4 ISP — consumer defines the interface).
type ClaudeInstanceGetter interface {
	// InstanceForPane derives a ClaudeInstance ref from a pane's process.
	// Returns (ref, true) when the pane is running a Claude REPL, (_, false) otherwise.
	InstanceForPane(ctx context.Context, host, paneID string, pid int) (*ClaudeInstanceRef, bool)
}
