// pane_claude.go — pane-first ClaudeInstances implementation (ADR-022 Phase 4).
//
// Query.claudeInstances is a view over Pane nodes filtered by command "claude".
// This file contains the projection logic that converts []*graphql.TmuxPane
// into []*graphql.ClaudeInstance without going through the heartbeat subsystem.
package resolvers

import (
	"context"
	"fmt"
	"sort"
	"time"

	graphql1 "github.com/drewdrewthis/git-orchard-rs/internal/server/graphql"
	"github.com/drewdrewthis/git-orchard-rs/internal/server/loaders"
	claudeinstance "github.com/drewdrewthis/git-orchard-rs/internal/server/providers/claudeinstance"
	psprovider "github.com/drewdrewthis/git-orchard-rs/internal/server/providers/ps"
)

// projectPanesToClaudeInstances converts a slice of tmux panes (all presumed
// to be running claude) into ClaudeInstance graph nodes. For each pane it:
//
//  1. Resolves the Process via the ps provider / loader.
//  2. Finds the matching Conversation by cwd (via claudeprojects).
//  3. Derives state from the jsonl snapshot.
//  4. Attaches the active ClaudeAccount.
//
// Returns [] (never nil).
func (r *queryResolver) projectPanesToClaudeInstances(ctx context.Context, panes []*graphql1.TmuxPane) []*graphql1.ClaudeInstance {
	if len(panes) == 0 {
		return []*graphql1.ClaudeInstance{}
	}

	host := "local"
	if r.Tmux != nil {
		host = string(r.Tmux.Host())
	}

	// Resolve the active account once — same account for every instance.
	var account *graphql1.ClaudeAccount
	if r.ClaudeAccount != nil {
		accts, err := r.ClaudeAccount.List(ctx)
		if err == nil && len(accts) > 0 {
			account = r.ClaudeAccount.ToGraphQL(accts[0])
		}
	}

	// Build a production SnapshotReader for jsonl state derivation.
	snapshotReader := claudeinstance.NewFsSnapshotReader("")

	out := make([]*graphql1.ClaudeInstance, 0, len(panes))
	for _, pane := range panes {
		if pane == nil {
			continue
		}
		inst := r.buildClaudeInstanceFromPane(ctx, pane, host, account, snapshotReader)
		out = append(out, inst)
	}

	// Sort by id for deterministic output — mirrors Provider.List sort.
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// buildClaudeInstanceFromPane constructs one ClaudeInstance from a TmuxPane.
func (r *queryResolver) buildClaudeInstanceFromPane(
	ctx context.Context,
	pane *graphql1.TmuxPane,
	host string,
	account *graphql1.ClaudeAccount,
	snapshotReader claudeinstance.SnapshotReader,
) *graphql1.ClaudeInstance {
	var pid int
	if pane.CurrentPid != nil {
		pid = int(*pane.CurrentPid)
	}

	id := buildClaudeIDFromPane(host, pid, pane)
	inst := &graphql1.ClaudeInstance{
		ID:      id,
		Pane:    pane,
		Account: account,
	}

	// Resolve Process via loader when available, otherwise direct ps call.
	if pid > 0 {
		if l := loaders.FromContext(ctx); l != nil {
			if proc, err := l.Process.Load(ctx, loaders.ProcessKey{HostID: host, Pid: pid})(); err == nil && proc != nil {
				inst.Process = proc
			}
		} else if r.PS != nil {
			if proc, _, err := r.PS.Get(ctx, psprovider.ProcessID{Host: host, PID: pid}); err == nil {
				inst.Process = projectProcessFromPsProcess(&proc, host)
			}
		}
	}

	// Resolve cwd from ps — required to locate the conversation.
	var cwd string
	if r.PS != nil && pid > 0 {
		if resolved, err := r.PS.LoadCwd(ctx, pid); err == nil {
			cwd = resolved
		}
	}

	// Find the deepest matching conversation by cwd for state derivation.
	var sessionUUID string
	if cwd != "" && r.ClaudeProjects != nil {
		if convs, err := r.ClaudeProjects.List(ctx); err == nil {
			for _, conv := range convs {
				if conv.Cwd != nil && *conv.Cwd == cwd {
					sessionUUID = conv.ID.SessionUUID
					break
				}
			}
		}
	}

	// Derive state from jsonl snapshot.
	state, snap := claudeinstance.DeriveInstanceState(ctx, claudeinstance.DeriveState{
		Cwd:         cwd,
		SessionUUID: sessionUUID,
		Pid:         pid,
		Snapshot:    snapshotReader,
	})
	inst.State = state
	inst.InflightToolCount = int64(snap.InflightToolCount)
	if snap.Model != "" {
		v := snap.Model
		inst.Model = &v
	}
	if !snap.LastActivityAt.IsZero() {
		quantized := snap.LastActivityAt.UTC().Truncate(time.Second)
		v := quantized.Format(time.RFC3339)
		inst.LastActivityAt = &v
	}
	if sessionUUID != "" {
		v := sessionUUID
		inst.SessionUUID = &v
	}

	// Fallback lastActivityAt from the pane's session (mirrors Composer).
	if inst.LastActivityAt == nil &&
		pane.Window != nil && pane.Window.Session != nil &&
		pane.Window.Session.LastActivityAt != nil {
		v := *pane.Window.Session.LastActivityAt
		inst.LastActivityAt = &v
	}

	return inst
}

// buildClaudeIDFromPane constructs the stable ClaudeInstance node id from a
// pane. Mirrors claudeinstance.buildID: pid-keyed when pid > 0, pane-keyed
// otherwise.
func buildClaudeIDFromPane(host string, pid int, pane *graphql1.TmuxPane) string {
	if pid > 0 {
		return fmt.Sprintf("ClaudeInstance:%s:%d", host, pid)
	}
	return fmt.Sprintf("ClaudeInstance:%s:pane-%s", host, pane.PaneID)
}

// projectProcessFromPsProcess projects a psprovider.Process onto
// *graphql1.Process. Mirrors loader_bridge.go:projectTmuxPane's pattern
// and the loaders.projectProcess function.
func projectProcessFromPsProcess(p *psprovider.Process, hostID string) *graphql1.Process {
	startedAt := p.StartedRaw
	if !p.StartedAt.IsZero() {
		startedAt = p.StartedAt.Format(time.RFC3339)
	}
	out := &graphql1.Process{
		ID:         p.ID.String(),
		Host:       &graphql1.Host{ID: hostID},
		Pid:        int64(p.ID.PID),
		Ppid:       int64(p.PPID),
		Command:    p.Command,
		StartedAt:  startedAt,
		CPUPercent: p.CPUPercent,
		MemBytes:   p.MemBytes,
	}
	if p.TTY != "" {
		tty := p.TTY
		out.Tty = &tty
	}
	return out
}
