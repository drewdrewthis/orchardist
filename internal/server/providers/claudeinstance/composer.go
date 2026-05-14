package claudeinstance

import (
	"context"
	"fmt"
	"log/slog"
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
	hostID     string
	panes      PaneFinder
	procs      ProcessFinder
	accounts   AccountFinder
	liveness   LivenessChecker
	jsonl      JsonlReader
	snapshot   SnapshotReader
	shadow     *ShadowClassifier
	clock      func() time.Time
	staleAfter time.Duration
}

// NewComposer constructs a Composer with the production
// LivenessChecker, JsonlReader, SnapshotReader, ShadowClassifier, and
// wall clock. Pass nil for any sibling that has not yet shipped a real
// provider — v1 leaves those edges null and the composer behaves as if
// no match was found.
func NewComposer(hostID string, panes PaneFinder, procs ProcessFinder, accounts AccountFinder) *Composer {
	c := NewComposerWith(hostID, panes, procs, accounts, OSLivenessChecker{}, NewFsJsonlReader(""), time.Now, HeartbeatStaleAfter)
	c.snapshot = NewFsSnapshotReader("")
	c.shadow = NewShadowClassifier("", slog.Default())
	return c
}

// NewComposerWith is the test-friendly constructor — accepts injected
// liveness checker, jsonl reader, clock, and stale-after duration.
// Production wires OSLivenessChecker, FsJsonlReader, time.Now, and
// HeartbeatStaleAfter; tests can shrink the window so staleness assertions
// converge in milliseconds and pass a fixture jsonl reader.
//
// The jsonl reader may be nil — the composer falls back to the
// heartbeat's last_activity field (and from there to the pane's
// session-level lastActivityAt) when no reader is wired.
//
// Shadow mode is disabled by default. Use WithShadow to enable it in
// production; tests that do not test shadow behavior leave it nil.
func NewComposerWith(
	hostID string,
	panes PaneFinder,
	procs ProcessFinder,
	accounts AccountFinder,
	liveness LivenessChecker,
	jsonl JsonlReader,
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
		jsonl:      jsonl,
		clock:      clock,
		staleAfter: staleAfter,
	}
}

// WithSnapshot attaches a SnapshotReader to the composer, enabling Phase 2
// jsonl-derived state derivation. Returns the same *Composer for chaining.
// When nil, the composer falls back to the hook-derived state (no-jsonl path).
func (c *Composer) WithSnapshot(s SnapshotReader) *Composer {
	c.snapshot = s
	return c
}

// WithShadow attaches a ShadowClassifier to the composer, enabling Phase 1
// shadow-mode comparison logging. Returns the same *Composer for chaining.
func (c *Composer) WithShadow(s *ShadowClassifier) *Composer {
	c.shadow = s
	return c
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
//
// State is derived exclusively from the jsonl snapshot (Phase 2+). The
// hook-written state field is no longer read. When no jsonl is found the
// session has no observable transcript state; we emit idle rather than
// trusting the hook's potentially-fabricated value. Dead pid still forces
// no_claude regardless.
func (c *Composer) composeOne(ctx context.Context, hb Heartbeat) *graphql.ClaudeInstance {
	pane := c.findPane(ctx, hb)
	pid := c.resolvePid(hb, pane)
	proc := c.findProcess(ctx, pid)
	account := c.findAccount(ctx)

	// Derive state from jsonl. Falls back to idle (not hook value) when no
	// jsonl exists so we never surface fabricated hook input events.
	state, snap := c.deriveStateFromJsonl(ctx, hb, pid, pane)

	id := buildID(c.hostID, pid, hb.TmuxSession)
	inst := &graphql.ClaudeInstance{
		ID:                id,
		Pane:              pane,
		Process:           proc,
		Account:           account,
		State:             state,
		RcEnabled:         hb.RcEnabled,
		InflightToolCount: int64(snap.InflightToolCount),
	}
	if snap.Model != "" {
		v := snap.Model
		inst.Model = &v
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
	// Resolve LastActivityAt in priority order:
	//
	//  1. Jsonl classifier snapshot — LastActivityAt is the max timestamp
	//     of the last N records; authoritative because claude appends on
	//     every assistant/user/system step. Quantized to 1-second resolution
	//     to prevent subscription thrash during streaming.
	//  2. Jsonl reader fallback — tail of the session's jsonl file.
	//  3. Pane session fallback — coarse tmux session lastActivityAt.
	if !snap.LastActivityAt.IsZero() {
		// Quantize to 1s resolution to prevent subscription thrash when
		// the assistant is streaming (each token causes a new fsnotify event
		// and a new lastActivityAt, but a 1-second granularity is sufficient
		// for the dashboard's "needs attention" lens).
		quantized := snap.LastActivityAt.UTC().Truncate(time.Second)
		v := quantized.Format(time.RFC3339)
		inst.LastActivityAt = &v
	}
	if inst.LastActivityAt == nil && c.jsonl != nil && hb.Cwd != "" && hb.SessionID != "" {
		if t, ok := c.jsonl.LastActivityAt(ctx, hb.Cwd, hb.SessionID); ok {
			quantized := t.UTC().Truncate(time.Second)
			v := quantized.Format(time.RFC3339)
			inst.LastActivityAt = &v
		}
	}
	if inst.LastActivityAt == nil &&
		pane != nil && pane.Window != nil && pane.Window.Session != nil &&
		pane.Window.Session.LastActivityAt != nil {
		v := *pane.Window.Session.LastActivityAt
		inst.LastActivityAt = &v
	}
	return inst
}

// deriveStateFromJsonl reads the jsonl snapshot for this heartbeat and
// returns the classified state plus the full snapshot. When no jsonl is
// found the state falls back to idle for confirmed-alive pids (not the
// hook's value). When pid is confirmed dead, no_claude is forced.
// When pid is unknown (no pid from any source), staleness determines:
// stale → no_claude (conservative), fresh → idle.
//
// Returns (no_claude, zero-snap) when pid is confirmed dead OR
//
//	when pid is unknown AND heartbeat is stale.
//
// Returns (idle, zero-snap) when no jsonl found and pid is alive/unknown-fresh.
// Returns (snap.State, snap) when jsonl is available and pid is alive/unknown-fresh.
func (c *Composer) deriveStateFromJsonl(ctx context.Context, hb Heartbeat, pid int, pane *graphql.TmuxPane) (graphql.InstanceState, JsonlStateSnapshot) {
	alive := c.aliveSignal(pid, pane)
	now := c.clock()

	// Dead pid always wins — the session is genuinely gone.
	if alive == alivenessDead {
		return graphql.InstanceStateNoClaude, JsonlStateSnapshot{}
	}

	// When pid is completely unknown (no pid in heartbeat, no pane), fall
	// back to heartbeat freshness as the only remaining liveness signal.
	// Stale + unknown → conservative no_claude; fresh + unknown → continue.
	if alive == alivenessUnknown {
		stamp := hb.LastHeartbeatAt
		if stamp.IsZero() {
			stamp = hb.Timestamp
		}
		fresh := !stamp.IsZero() && now.Sub(stamp) <= c.staleAfter
		if !fresh {
			return graphql.InstanceStateNoClaude, JsonlStateSnapshot{}
		}
	}

	// No jsonl available (no cwd, no session id, or file not found):
	// fall back to idle rather than trusting the hook's fabricated value.
	if c.snapshot == nil || hb.Cwd == "" || hb.SessionID == "" {
		return graphql.InstanceStateIdle, JsonlStateSnapshot{}
	}
	records, ok := c.snapshot.ReadSnapshot(ctx, hb.Cwd, hb.SessionID)
	if !ok {
		return graphql.InstanceStateIdle, JsonlStateSnapshot{}
	}

	snap := ClassifyState(records, c.clock())
	return snap.State, snap
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

// aliveness is a three-valued signal — pid liveness can be authoritative
// (alive or dead) or absent (no pid recorded anywhere).
type aliveness int

const (
	alivenessUnknown aliveness = iota
	alivenessAlive
	alivenessDead
)

// aliveSignal answers "is there a live Claude for this heartbeat?" using
// every pid we have. The pane's CurrentPid is consulted in addition to
// the resolved pid because the pane may be hosting a respawned claude
// whose pid differs from the heartbeat (#421). Either pid being alive
// flips the signal to alivenessAlive.
//
// The function returns alivenessUnknown only when no pid is available at
// all — neither heartbeat nor pane recorded one. The deriveState caller
// then falls back to heartbeat-freshness alone.
func (c *Composer) aliveSignal(pid int, pane *graphql.TmuxPane) aliveness {
	checked := false
	if pid > 0 {
		checked = true
		if c.liveness.IsAlive(pid) {
			return alivenessAlive
		}
	}
	if pane != nil && pane.CurrentPid != nil && *pane.CurrentPid > 0 {
		panePid := int(*pane.CurrentPid)
		if panePid != pid {
			checked = true
			if c.liveness.IsAlive(panePid) {
				return alivenessAlive
			}
		}
	}
	if checked {
		return alivenessDead
	}
	return alivenessUnknown
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
