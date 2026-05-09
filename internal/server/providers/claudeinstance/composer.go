package claudeinstance

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/drewdrewthis/git-orchard-rs/internal/server/graphql"
)

// PaneFinder resolves a tmux pane for a heartbeat. Implementations are
// dependency-injected by the daemon entry point so this provider has no
// import on the tmux provider's package — keeps SOLID-D honest.
//
// Find returns (nil, false) when no matching pane exists; the composer
// surfaces such instances with `pane: null` so the dashboard can still
// display a Claude that is heartbeating from outside any tmux pane.
type PaneFinder interface {
	// FindByPid returns the pane whose foreground pid matches claudePid.
	// claudePid==0 means the heartbeat did not record one; implementations
	// MUST return (nil, false) in that case rather than guessing.
	FindByPid(ctx context.Context, hostID string, claudePid int) (*graphql.TmuxPane, bool)

	// FindBySession returns the pane whose tmux session name matches.
	// Used as a fallback when the heartbeat does not yet record a pid.
	// Implementations may return any pane in the session that hosts a
	// claude process; v1 just returns the first one if multiple match.
	FindBySession(ctx context.Context, hostID, tmuxSession string) (*graphql.TmuxPane, bool)
}

// ProcessFinder resolves the OS process record for a Claude pid.
// Dependency-injected from the ps provider so composer compiles
// without ws-b-ps merging.
type ProcessFinder interface {
	FindByPid(ctx context.Context, hostID string, pid int) (*graphql.Process, bool)
}

// AccountFinder returns the active Claude CLI account for a host.
// v1 surfaces a single account per ADR-011 §5.1; the composer attaches
// it to every instance that has a fresh heartbeat.
type AccountFinder interface {
	// Active returns the local Claude account, or (nil, false) if claude
	// CLI is not installed/authed. v1 attaches this to every instance.
	Active(ctx context.Context, hostID string) (*graphql.ClaudeAccount, bool)
}

// LivenessChecker reports whether a pid is still alive on the host.
// Production uses os.FindProcess + signal-0; tests inject a stub map
// so they can deterministically toggle "process died".
type LivenessChecker interface {
	IsAlive(pid int) bool
}

// OSLivenessChecker uses the standard signal-0 trick to ask the kernel
// whether a pid is alive without sending a real signal.
type OSLivenessChecker struct{}

// IsAlive returns true when sending signal 0 to pid succeeds — the
// standard Unix idiom for "process exists and is reachable from this
// uid". Returns false for pid<=0.
func (OSLivenessChecker) IsAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}

// Composer joins heartbeats with sibling-provider data into a list of
// ClaudeInstances. All cross-provider data flows through the four
// dependency-injected interfaces above; the Composer itself imports
// nothing from tmux/ps/claudeaccount.
//
// Stateless on purpose — every Compose call goes back through its
// dependencies. Caching belongs to the Provider above us.
type Composer struct {
	hostID    string
	panes     PaneFinder
	procs     ProcessFinder
	accounts  AccountFinder
	liveness  LivenessChecker
	clock     func() time.Time
	staleAfter time.Duration
}

// NewComposer constructs a Composer with the production
// LivenessChecker and wall clock. Pass nil for any sibling that has not
// yet shipped a real provider — v1 leaves those edges null and the
// composer behaves as if no match was found.
func NewComposer(hostID string, panes PaneFinder, procs ProcessFinder, accounts AccountFinder) *Composer {
	return NewComposerWith(hostID, panes, procs, accounts, OSLivenessChecker{}, time.Now, HeartbeatStaleAfter)
}

// NewComposerWith is the test-friendly constructor — accepts injected
// liveness checker, clock, and stale-after duration. Production wires
// time.Now and HeartbeatStaleAfter; tests can shrink the window so
// staleness assertions converge in milliseconds.
func NewComposerWith(
	hostID string,
	panes PaneFinder,
	procs ProcessFinder,
	accounts AccountFinder,
	liveness LivenessChecker,
	clock func() time.Time,
	staleAfter time.Duration,
) *Composer {
	if liveness == nil {
		liveness = OSLivenessChecker{}
	}
	if clock == nil {
		clock = time.Now
	}
	if staleAfter <= 0 {
		staleAfter = HeartbeatStaleAfter
	}
	return &Composer{
		hostID:     hostID,
		panes:      panes,
		procs:      procs,
		accounts:   accounts,
		liveness:   liveness,
		clock:      clock,
		staleAfter: staleAfter,
	}
}

// Compose folds the heartbeats into ClaudeInstances. The order follows
// the input slice (which the adapter sorts by tmux session for
// deterministic test output).
//
// Each heartbeat produces exactly one ClaudeInstance. When ClaudePid
// is unknown and the PaneFinder cannot match by tmux session either,
// the instance is still emitted with pane=null and process=null —
// orchardists want to see the heartbeat-only ghost so they know
// something is alive even when tmux state has not yet caught up.
func (c *Composer) Compose(ctx context.Context, heartbeats []Heartbeat) []*graphql.ClaudeInstance {
	out := make([]*graphql.ClaudeInstance, 0, len(heartbeats))
	for _, hb := range heartbeats {
		out = append(out, c.composeOne(ctx, hb))
	}
	return out
}

// composeOne builds a single ClaudeInstance from one Heartbeat. Pure
// in the sense that all I/O is delegated to the injected interfaces.
func (c *Composer) composeOne(ctx context.Context, hb Heartbeat) *graphql.ClaudeInstance {
	pane := c.findPane(ctx, hb)
	pid := c.resolvePid(hb, pane)
	proc := c.findProcess(ctx, pid)
	account := c.findAccount(ctx)
	state := c.deriveState(hb, pid)

	id := buildID(c.hostID, pid, hb.TmuxSession)
	inst := &graphql.ClaudeInstance{
		ID:        id,
		Pane:      pane,
		Process:   proc,
		Account:   account,
		State:     state,
		RcEnabled: hb.RcEnabled,
	}
	if hb.RcURL != "" {
		v := hb.RcURL
		inst.RcURL = &v
	}
	if hb.SessionID != "" {
		v := hb.SessionID
		inst.SessionUUID = &v
	}
	if !hb.Timestamp.IsZero() {
		v := hb.Timestamp.UTC().Format(time.RFC3339Nano)
		inst.StartedAt = &v
	}
	if !hb.LastActivity.IsZero() {
		// Primary source: heartbeat's last_activity field.
		// Preserve sub-second precision from the original value by using
		// RFC3339Nano. When the source had no nanoseconds, RFC3339Nano
		// still produces a valid RFC3339 string (trailing zeros are
		// stripped by Go's time formatter).
		v := hb.LastActivity.UTC().Format(time.RFC3339Nano)
		inst.LastActivityAt = &v
	} else {
		// Fallback (option a): read pane.Window.Session.LastActivityAt.
		// TmuxPane has no lastActivityAt field of its own; the session-level
		// timestamp is the same conceptual recency for the user's purposes
		// ("when was this claude instance last touched"). Option (b) — adding
		// TmuxPane.lastActivityAt as a new schema field — can be layered on
		// top later when pane-level granularity is needed, without breaking
		// this fallback.
		if pane != nil && pane.Window != nil && pane.Window.Session != nil &&
			pane.Window.Session.LastActivityAt != nil {
			v := *pane.Window.Session.LastActivityAt
			inst.LastActivityAt = &v
		}
	}
	return inst
}

// findPane delegates to PaneFinder using whichever match key the
// heartbeat provides. Returns nil silently when no finder is wired
// (sibling provider not yet shipped) or no match is found.
func (c *Composer) findPane(ctx context.Context, hb Heartbeat) *graphql.TmuxPane {
	if c.panes == nil {
		return nil
	}
	if hb.ClaudePid > 0 {
		if p, ok := c.panes.FindByPid(ctx, c.hostID, hb.ClaudePid); ok {
			return p
		}
	}
	if hb.TmuxSession != "" {
		if p, ok := c.panes.FindBySession(ctx, c.hostID, hb.TmuxSession); ok {
			return p
		}
	}
	return nil
}

// resolvePid returns the best available pid for the Claude process, in
// priority order:
//
//  1. Heartbeat ClaudePid — authoritative when the hook script records it.
//  2. pane.CurrentPid — primary fallback; the tmux provider reads the
//     foreground pid of the pane directly, so this is accurate even when
//     the heartbeat predates the pid-recording shape.
//  3. pidFromPaneID — legacy stub (always returns 0/false today; reserved
//     for a future PaneFinder extension).
//  4. 0 — no pid available; the composer surfaces these as `no_claude`.
func (c *Composer) resolvePid(hb Heartbeat, pane *graphql.TmuxPane) int {
	if hb.ClaudePid > 0 {
		return hb.ClaudePid
	}
	if pane != nil && pane.CurrentPid != nil && *pane.CurrentPid > 0 {
		return int(*pane.CurrentPid)
	}
	if pane != nil && pane.ID != "" {
		if pid, ok := pidFromPaneID(pane.ID); ok {
			return pid
		}
	}
	return 0
}

// pidFromPaneID is a no-op fallback today: TmuxPane.id is `<host>:<paneId>`
// (e.g. `mac:%26`) and does not embed the foreground pid. A future
// PaneFinder can be extended to expose the pid; for now the heartbeat
// is the only authoritative pid source. Returns (0, false) so callers
// fall through to the no_claude path when no pid is available.
func pidFromPaneID(_ string) (int, bool) {
	return 0, false
}

func (c *Composer) findProcess(ctx context.Context, pid int) *graphql.Process {
	if c.procs == nil || pid <= 0 {
		return nil
	}
	if p, ok := c.procs.FindByPid(ctx, c.hostID, pid); ok {
		return p
	}
	return nil
}

func (c *Composer) findAccount(ctx context.Context) *graphql.ClaudeAccount {
	if c.accounts == nil {
		return nil
	}
	if a, ok := c.accounts.Active(ctx, c.hostID); ok {
		return a
	}
	return nil
}

// deriveState collapses the briefing's matrix into one InstanceState.
//
//	working   — heartbeat fresh AND state==working AND pid alive (or unknown)
//	idle      — heartbeat fresh AND state==idle    AND pid alive (or unknown)
//	input     — heartbeat fresh AND state==input   AND pid alive (or unknown)
//	no_claude — heartbeat stale OR pid known dead OR state unrecognised
//
// "pid unknown" (heartbeat predates the pid-recording shape) does NOT
// downgrade to no_claude — we trust the heartbeat in that case because
// the alternative is a permanent no_claude for every instance until the
// hook script is updated.
func (c *Composer) deriveState(hb Heartbeat, pid int) graphql.InstanceState {
	now := c.clock()
	stamp := hb.LastHeartbeatAt
	if stamp.IsZero() {
		stamp = hb.Timestamp
	}
	if stamp.IsZero() || now.Sub(stamp) > c.staleAfter {
		return graphql.InstanceStateNoClaude
	}
	if pid > 0 && !c.liveness.IsAlive(pid) {
		return graphql.InstanceStateNoClaude
	}
	switch strings.ToLower(strings.TrimSpace(hb.State)) {
	case "working":
		return graphql.InstanceStateWorking
	case "idle":
		return graphql.InstanceStateIdle
	case "input":
		return graphql.InstanceStateInput
	default:
		return graphql.InstanceStateNoClaude
	}
}

// buildID is the canonical id formatter — `ClaudeInstance:<host>:<pid>`
// when pid is known; falls back to `ClaudeInstance:<host>:session-<name>`
// when no pid is available so the id still satisfies Node.id's
// uniqueness requirement.
func buildID(hostID string, pid int, tmuxSession string) string {
	if pid > 0 {
		return fmt.Sprintf("ClaudeInstance:%s:%d", hostID, pid)
	}
	return fmt.Sprintf("ClaudeInstance:%s:session-%s", hostID, tmuxSession)
}

// parseInstanceID is the inverse of buildID. Returns (hostID, pid, true)
// for pid-keyed ids, or (hostID, 0, false) for session-keyed ids
// (callers should look up by tmuxSession in that case).
func parseInstanceID(id string) (string, int, bool) {
	const prefix = "ClaudeInstance:"
	if !strings.HasPrefix(id, prefix) {
		return "", 0, false
	}
	rest := id[len(prefix):]
	idx := strings.LastIndex(rest, ":")
	if idx < 0 {
		return rest, 0, false
	}
	host, tail := rest[:idx], rest[idx+1:]
	if pid, err := strconv.Atoi(tail); err == nil && pid > 0 {
		return host, pid, true
	}
	return host, 0, false
}
