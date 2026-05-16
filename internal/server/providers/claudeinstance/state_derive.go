package claudeinstance

import (
	"context"
	"time"

	"github.com/drewdrewthis/git-orchard-rs/internal/server/graphql"
)

// DeriveState encapsulates the inputs needed to derive a ClaudeInstance's
// state from a jsonl snapshot. Used by the pane-first resolver path (ADR-022
// Phase 4) where no Heartbeat is available.
type DeriveState struct {
	// Cwd is the working directory of the Claude process. Required for
	// locating the jsonl file.
	Cwd string
	// SessionUUID is the Claude CLI session id. Required for locating the
	// jsonl file.
	SessionUUID string
	// Pid is the foreground pid of the Claude process. When > 0 it is
	// checked for liveness before reading jsonl.
	Pid int
	// StaleAfter is the freshness window. Zero defaults to HeartbeatStaleAfter.
	StaleAfter time.Duration
	// Snapshot is the reader for jsonl records. When nil, the caller gets
	// InstanceStateIdle for live pids and InstanceStateNoClaude for dead ones.
	Snapshot SnapshotReader
	// Liveness lets callers inject a custom pid-check (e.g. for tests).
	// When nil, OSLivenessChecker is used.
	Liveness LivenessChecker
	// Clock is used for freshness evaluation. When nil, time.Now is used.
	Clock func() time.Time
}

// DeriveInstanceState derives the InstanceState and JsonlStateSnapshot for a
// pane-first ClaudeInstance (ADR-022 Phase 4). It is a package-level
// extraction of Composer.deriveStateFromJsonl, usable without a Composer
// or Heartbeat.
//
// Equivalent logic: if pid is dead → NoClaude; if no jsonl → Idle;
// otherwise ClassifyState from the jsonl records.
func DeriveInstanceState(ctx context.Context, d DeriveState) (graphql.InstanceState, JsonlStateSnapshot) {
	liveness := d.Liveness
	if liveness == nil {
		liveness = OSLivenessChecker{}
	}
	clock := d.Clock
	if clock == nil {
		clock = time.Now
	}
	staleAfter := d.StaleAfter
	if staleAfter <= 0 {
		staleAfter = HeartbeatStaleAfter
	}

	// If we have a pid, check liveness.
	if d.Pid > 0 && !liveness.IsAlive(d.Pid) {
		return graphql.InstanceStateNoClaude, JsonlStateSnapshot{}
	}

	// No jsonl available — return idle for live/unknown pids.
	if d.Snapshot == nil || d.Cwd == "" || d.SessionUUID == "" {
		return graphql.InstanceStateIdle, JsonlStateSnapshot{}
	}

	records, ok := d.Snapshot.ReadSnapshot(ctx, d.Cwd, d.SessionUUID)
	if !ok {
		return graphql.InstanceStateIdle, JsonlStateSnapshot{}
	}

	snap := ClassifyState(records, clock())
	return snap.State, snap
}
