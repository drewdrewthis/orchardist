// Package claudeinstance is the read provider for the ClaudeInstance
// node defined in ADR-011 §5.1.
//
// A ClaudeInstance is a Claude Code process running inside a tmux pane.
// Identity is `(host_id, claudePid)`. The provider composes four
// independent backends:
//
//  1. Heartbeat files written by the orchard hook script (state, session
//     uuid, optional claudePid / rcUrl / rcEnabled).
//  2. The tmux pane that hosts the claude pid (from ws-b-tmux's provider).
//  3. The OS process record for the pid (from ws-b-ps's provider).
//  4. The Claude CLI account that owns the session (from ws-b-claudeaccount).
//
// Per ADR-011 §6 (composition lives in resolvers, not in providers) and
// the briefing's SOLID checkpoint, this provider depends on small
// interfaces it defines locally — `PaneFinder`, `ProcessFinder`,
// `AccountFinder`, `LivenessChecker` — never on the concrete sibling
// provider types. The daemon entry point wires concrete sibling
// providers behind these interfaces at construction time.
package claudeinstance

import (
	"time"
)

// HeartbeatStaleAfter is the cutoff past which a heartbeat is treated as
// stale and the instance state collapses to `no_claude`. Briefing AC:
// "working if heartbeat's state==working AND lastHeartbeatAt < 30s".
const HeartbeatStaleAfter = 30 * time.Second

// PollInterval is the watcher's fallback tick rate when fsnotify cannot
// be used or has dropped events. Per briefing AC.
const PollInterval = 5 * time.Second

// InstanceID is the cache key for the provider — `(host_id, claudePid)`
// per ADR-011 §5.1. We store host_id as a string so the type stays
// comparable for use as a Go map key.
type InstanceID struct {
	HostID    string
	ClaudePid int
}

// Heartbeat is the in-memory shape of one heartbeat file from the
// orchard hook script. Only the fields that the composer actively reads
// are present; the hook may write additional fields (e.g. state,
// last_activity) that are no longer consumed by the daemon.
type Heartbeat struct {
	// TmuxSession is the tmux session name the heartbeat belongs to. The
	// orchard hook script always populates this.
	TmuxSession string
	// SessionID is the Claude session UUID (the `session_id` field).
	SessionID string
	// Timestamp is the RFC3339 timestamp the hook wrote when emitting
	// this file. We use this for staleness when LastHeartbeatAt is
	// absent.
	Timestamp time.Time
	// LastHeartbeatAt is the most recent heartbeat write time. When the
	// hook script emits this field separately, it is preferred over
	// Timestamp; otherwise the two are equal.
	LastHeartbeatAt time.Time
	// ClaudePid is the pid of the Claude process this heartbeat tracks.
	// Zero when the hook script has not yet been updated to write it; in
	// that case the composer derives the pid via PaneFinder.
	ClaudePid int
	// RcURL is the claude.ai Remote Control URL when remote-control is
	// enabled for this session. Empty when the heartbeat does not include
	// the field.
	RcURL string
	// RcEnabled mirrors the heartbeat's rcEnabled flag. False when the
	// field is absent — the schema's `rcEnabled` is non-nullable so we
	// default to false rather than nil.
	RcEnabled bool
	// Cwd is the working directory the Claude process was launched from,
	// as recorded by the hook script. Used together with SessionID to
	// resolve the session's transcript jsonl under
	// ~/.claude/projects/<encoded-cwd>/<session_uuid>.jsonl. Empty when the
	// heartbeat predates cwd recording.
	Cwd string
}

// HostID returns the host id this provider was constructed with. Useful
// to resolvers that need to round-trip the id back into Get calls.
func (p *Provider) HostID() string {
	return p.hostID
}
