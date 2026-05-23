// Package claudeinstance implements the claude-instance domain: the live
// Claude REPL as a JOIN of jsonl tail freshness + matching tmux pane process.
//
// ClaudeInstance is NOT a primary resource. It exists only when three
// conditions are simultaneously true:
//   - claude-jsonls: a session JSONL file with a live heartbeat
//   - tmux: a pane whose current command is "claude"
//   - ps: the pane's foreground process pid + cwd
//
// This domain is read-only (no mutations, no subscriptions). It satisfies
// Query.claudeInstances.
//
// Cross-domain contracts (R4/R5): this package defines narrow interfaces for
// the three services it consumes. Production wiring injects concrete
// implementations that satisfy these interfaces; tests inject stubs.
package claudeinstance

import (
	"context"
	"fmt"
	"sort"
	"time"
)

// ─── Consumer-defined interfaces (R4 ISP) ────────────────────────────────────
// These interfaces live here, in the CONSUMER, not in the provider packages.
// Each is as narrow as the join logic requires.

// ConversationSummary is the minimal projection of a claude-jsonls
// Conversation this domain needs to resolve ClaudeInstance.conversation
// and match sessionUuid.
type ConversationSummary struct {
	// SessionUUID is the identifier that links a JSONL file to a Claude
	// process. Matches the ClaudeInstance.sessionUuid field.
	SessionUUID string
	// Cwd is the working directory the conversation was started in. Used to
	// match pane.cwd → conversation.
	Cwd string
	// ID is the opaque conversation id from the claude-jsonls domain. Carried
	// through to ClaudeInstance.conversation resolution.
	ID string
}

// JsonlsService is the narrow projection of the claude-jsonls domain this
// module needs. The concrete *claudejsonls.Provider satisfies this interface;
// tests inject a stub.
type JsonlsService interface {
	// ConversationsByCwd returns a map of cwd → ConversationSummary for all
	// known conversations. The map is keyed by cwd so this domain can perform
	// an O(1) lookup during join.
	ConversationsByCwd(ctx context.Context) (map[string]ConversationSummary, error)
}

// TmuxPaneSummary is the minimal projection of a TmuxPane this domain
// needs to perform the join.
type TmuxPaneSummary struct {
	// PaneID is the tmux pane identifier (e.g. "%1").
	PaneID string
	// CurrentCommand is the foreground command (should be "claude").
	CurrentCommand string
	// CurrentPid is the foreground process pid. Zero when unknown.
	CurrentPid int
	// SessionName is the owning tmux session name.
	SessionName string
	// WindowName is the owning tmux window name.
	WindowName string
	// LastActivityAt is an RFC3339 timestamp from the tmux session, used as
	// a fallback for ClaudeInstance.lastActivityAt when no jsonl timestamp
	// is available.
	LastActivityAt string
}

// TmuxPaneReader is the narrow tmux surface this domain needs — pane
// enumeration by foreground command. The concrete *tmux.Provider satisfies
// this interface.
type TmuxPaneReader interface {
	// PanesByCommand returns all panes whose current command matches the
	// given string. host scopes the lookup to a specific host (e.g. "local").
	PanesByCommand(ctx context.Context, host, command string) ([]*TmuxPaneSummary, error)
	// Host returns the host identifier this tmux provider is scoped to.
	Host() string
}

// PsReader is the narrow ps surface this domain needs — cwd lookup by pid.
// The concrete *ps.Provider satisfies this interface.
type PsReader interface {
	// LoadCwd resolves the current working directory for the given pid.
	// Returns ("", err) when the pid is unknown or cwd cannot be resolved.
	LoadCwd(ctx context.Context, pid int) (string, error)
}

// AccountReader is the narrow claude-account surface this domain needs.
// Produces the account attached to each ClaudeInstance (same account for
// all instances on a host).
type AccountReader interface {
	// ActiveAccount returns the currently-authenticated ClaudeAccount. Returns
	// nil when no account is configured or the provider is unavailable.
	ActiveAccount(ctx context.Context) (*Account, error)
}

// Account is the minimal claude-account projection this domain stores on
// a ClaudeInstance. The claude-account resolver owns the full ClaudeAccount
// node; this type is the compact form needed for the join.
type Account struct {
	ID    string
	Email string
}

// SnapshotReader reads decoded records from a Claude session jsonl. Defined
// here (in the consumer) so tests can inject a stub without any filesystem
// dependency.
type SnapshotReader interface {
	// ReadSnapshot returns decoded non-sidechain records for the session
	// identified by cwd + sessionUUID. Returns (nil, false) when the file
	// does not exist or cannot be read.
	ReadSnapshot(ctx context.Context, cwd, sessionUUID string) ([]Record, bool)
}

// LivenessChecker reports whether a pid is still alive on the host. Tests
// inject a stub map; production uses OSLivenessChecker.
type LivenessChecker interface {
	IsAlive(pid int) bool
}

// ─── Service (R2) ─────────────────────────────────────────────────────────────

// Service is the only API consumers of this domain may import. All resolver
// and loader code depends on this interface, never on Provider directly.
type Service interface {
	// List returns all currently-live ClaudeInstance nodes. Returns an empty
	// slice (never nil) when no instances can be derived.
	List(ctx context.Context) ([]*Instance, error)
}

// ─── Domain types ─────────────────────────────────────────────────────────────

// HeartbeatStaleAfter is the freshness window used by DeriveInstanceState
// when no explicit StaleAfter is supplied.
const HeartbeatStaleAfter = 30 * time.Second

// Instance is the in-domain representation of a live ClaudeInstance node.
// Resolvers project this onto the GraphQL *graphql.ClaudeInstance model.
type Instance struct {
	// ID is the stable node id: "ClaudeInstance:<host>:<pid>" when pid > 0,
	// "ClaudeInstance:<host>:pane-<paneID>" otherwise.
	ID string
	// Host is the host identifier this instance lives on.
	Host string
	// Pid is the foreground claude process pid. Zero when unknown.
	Pid int
	// PaneID is the tmux pane id. Set when the pane is known.
	PaneID string
	// SessionUUID is the Claude CLI session identifier from the JSONL.
	SessionUUID string
	// State is the derived lifecycle state.
	State InstanceState
	// InflightToolCount is the number of open tool_use calls in the current turn.
	InflightToolCount int
	// Model is the Claude model string from the JSONL (e.g. "claude-opus-4-7").
	Model string
	// LastActivityAt is the RFC3339 timestamp of the most recent JSONL line.
	LastActivityAt time.Time
	// Account is the active ClaudeAccount for this session. May be nil.
	Account *Account
	// ConversationID is the claude-jsonls conversation id, for back-edge resolution.
	ConversationID string
}

// InstanceState enumerates the lifecycle states of a ClaudeInstance.
type InstanceState int

const (
	StateWorking  InstanceState = iota
	StateIdle                   // finished turn, waiting for prompt
	StateInput                  // paused, waiting for user input (AskUserQuestion)
	StateStalled                // alive but not answering heartbeats
	StateDead                   // pid gone
	StateNoClaude               // no claude session ever observed
)

// ─── Service implementation ───────────────────────────────────────────────────

// Inputs holds the three cross-domain interfaces this domain consumes.
// Injected at construction time; tests substitute stubs.
type Inputs struct {
	// Jsonls provides conversation cwd→uuid lookup.
	Jsonls JsonlsService
	// Panes provides claude-pane enumeration.
	Panes TmuxPaneReader
	// Ps provides cwd lookup by pid.
	Ps PsReader
	// Account provides the active claude account.
	Account AccountReader
	// Snapshot reads jsonl records for state derivation.
	Snapshot SnapshotReader
	// Liveness checks whether a pid is alive. Nil → OSLivenessChecker.
	Liveness LivenessChecker
	// Clock returns current time. Nil → time.Now.
	Clock func() time.Time
}

// Provider is the Service implementation. Satisfies Service.
type Provider struct {
	inputs Inputs
}

// New constructs a Provider. All Inputs fields are optional and guarded
// internally; nil fields disable the corresponding join arm and return
// safe defaults.
func New(inputs Inputs) *Provider {
	return &Provider{inputs: inputs}
}

// List implements Service. Derives the live ClaudeInstance set from the
// join of panes running "claude" + jsonl snapshots + ps cwd resolution.
func (p *Provider) List(ctx context.Context) ([]*Instance, error) {
	if p.inputs.Panes == nil {
		return []*Instance{}, nil
	}

	host := p.inputs.Panes.Host()
	panes, err := p.inputs.Panes.PanesByCommand(ctx, host, "claude")
	if err != nil {
		return nil, fmt.Errorf("claude-instance: list panes: %w", err)
	}
	if len(panes) == 0 {
		return []*Instance{}, nil
	}

	// Fetch account once — same for all instances on this host.
	var account *Account
	if p.inputs.Account != nil {
		account, _ = p.inputs.Account.ActiveAccount(ctx)
	}

	// Build cwd→conversationSummary index once (O(M)) to avoid N×M lookups.
	cwdToConv := make(map[string]ConversationSummary)
	if p.inputs.Jsonls != nil {
		if m, err := p.inputs.Jsonls.ConversationsByCwd(ctx); err == nil {
			cwdToConv = m
		}
	}

	clock := p.inputs.Clock
	if clock == nil {
		clock = time.Now
	}
	liveness := p.inputs.Liveness
	if liveness == nil {
		liveness = OSLivenessChecker{}
	}

	out := make([]*Instance, 0, len(panes))
	for _, pane := range panes {
		if pane == nil {
			continue
		}
		inst := p.buildFromPane(ctx, pane, host, account, cwdToConv, liveness, clock)
		if inst != nil {
			out = append(out, inst)
		}
	}

	// Deterministic sort by ID.
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// buildFromPane constructs one Instance from a TmuxPaneSummary. Returns nil
// when the pane cannot produce a valid instance (e.g. dead pid with no jsonl).
func (p *Provider) buildFromPane(
	ctx context.Context,
	pane *TmuxPaneSummary,
	host string,
	account *Account,
	cwdToConv map[string]ConversationSummary,
	liveness LivenessChecker,
	clock func() time.Time,
) *Instance {
	pid := pane.CurrentPid

	// Build the stable id.
	id := buildID(host, pid, pane.PaneID)

	// Resolve cwd from ps.
	var cwd string
	if p.inputs.Ps != nil && pid > 0 {
		if resolved, err := p.inputs.Ps.LoadCwd(ctx, pid); err == nil {
			cwd = resolved
		}
	}

	// Match conversation by cwd.
	var sessionUUID, conversationID string
	if cwd != "" {
		if conv, ok := cwdToConv[cwd]; ok {
			sessionUUID = conv.SessionUUID
			conversationID = conv.ID
		}
	}

	// Derive state from jsonl snapshot.
	state, snap := deriveState(ctx, DeriveState{
		Cwd:         cwd,
		SessionUUID: sessionUUID,
		Pid:         pid,
		Snapshot:    p.inputs.Snapshot,
		Liveness:    liveness,
		Clock:       clock,
	})

	inst := &Instance{
		ID:                id,
		Host:              host,
		Pid:               pid,
		PaneID:            pane.PaneID,
		SessionUUID:       sessionUUID,
		State:             state,
		InflightToolCount: snap.InflightToolCount,
		Model:             snap.Model,
		LastActivityAt:    snap.LastActivityAt,
		Account:           account,
		ConversationID:    conversationID,
	}

	// Fallback lastActivityAt from the pane's session.
	if inst.LastActivityAt.IsZero() && pane.LastActivityAt != "" {
		if t, err := time.Parse(time.RFC3339, pane.LastActivityAt); err == nil {
			inst.LastActivityAt = t
		}
	}

	return inst
}

// buildID produces a stable node id for a ClaudeInstance.
func buildID(host string, pid int, paneID string) string {
	if pid > 0 {
		return fmt.Sprintf("ClaudeInstance:%s:%d", host, pid)
	}
	return fmt.Sprintf("ClaudeInstance:%s:pane-%s", host, paneID)
}
