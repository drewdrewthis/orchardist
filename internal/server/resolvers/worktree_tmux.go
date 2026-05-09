// worktree_tmux.go implements Worktree.tmuxPanes and Worktree.tmuxSession (#511).
//
// The two fields derive a server-side join of pane.process.cwd against the
// worktree path — exact-equality or path-prefix (path + "/") — so no
// client-side heuristics are required to attach a terminal to a worktree.
//
// Both fields are in their own file to keep schema.resolvers.go lean
// (per project convention: SRP, one concern per file).

package resolvers

import (
	"context"
	"sort"
	"strings"

	graphql1 "github.com/drewdrewthis/git-orchard-rs/internal/server/graphql"
	tmux "github.com/drewdrewthis/git-orchard-rs/internal/server/providers/tmux"
)

// TmuxPanes resolves Worktree.tmuxPanes (#511): returns every tmux pane on
// the worktree's host whose foreground-process cwd equals obj.Path exactly
// OR has obj.Path+"/" as a prefix. Panes are returned sorted by paneId
// ascending (lex sort — "%2" < "%5" < "%9"). Returns [] (never nil) when
// no panes match or when either the tmux or ps provider is not wired.
func (r *worktreeResolver) TmuxPanes(ctx context.Context, obj *graphql1.Worktree) ([]*graphql1.TmuxPane, error) {
	if r.Tmux == nil {
		return []*graphql1.TmuxPane{}, nil
	}

	snap := r.Tmux.Snapshot()
	matching, err := matchPanesForWorktree(ctx, r, snap, obj)
	if err != nil {
		return []*graphql1.TmuxPane{}, nil
	}

	// Sort by paneId ascending so output is deterministic.
	sort.Slice(matching, func(i, j int) bool {
		return matching[i].PaneID < matching[j].PaneID
	})

	return matching, nil
}

// TmuxSession resolves Worktree.tmuxSession (#511): returns the
// most-recently-active TmuxSession among the matching panes.  When two
// sessions tie on lastActivityAt the one with the lexicographically-first
// name wins.  Returns nil when tmuxPanes is empty.
func (r *worktreeResolver) TmuxSession(ctx context.Context, obj *graphql1.Worktree) (*graphql1.TmuxSession, error) {
	if r.Tmux == nil {
		return nil, nil
	}

	snap := r.Tmux.Snapshot()
	matching, err := matchPanesForWorktree(ctx, r, snap, obj)
	if err != nil || len(matching) == 0 {
		return nil, nil
	}

	// Collect the unique sessions that the matching panes belong to.
	// Use SessionKey (host+name) for dedup; store the raw Session value
	// so we can order by LastActivityAt.
	seen := make(map[tmux.SessionKey]tmux.Session)
	for _, pane := range matching {
		// Reconstruct the raw Pane to access WindowKey.Session — we need
		// the provider's internal Session value (which carries LastActivityAt
		// as a time.Time). The matching []*graphql1.TmuxPane only has the
		// projected PaneID; we look the full Pane back up from the snapshot.
		paneKey := tmux.PaneKey{Host: tmux.HostID(obj.Host), PaneID: pane.PaneID}
		rawPane, ok := snap.Panes[paneKey]
		if !ok {
			continue
		}
		sessionKey := tmux.SessionKey{Host: rawPane.Key.Host, Name: rawPane.WindowKey.Session}
		if _, already := seen[sessionKey]; already {
			continue
		}
		// Look up the Session to get its LastActivityAt.
		if s, ok := snap.Sessions[sessionKey]; ok {
			seen[sessionKey] = s
		} else {
			// Session row missing from snapshot — still include it so we
			// don't silently drop a pane that genuinely sits in this session;
			// synthesise a zero-activity Session.
			seen[sessionKey] = tmux.Session{Key: sessionKey}
		}
	}

	if len(seen) == 0 {
		return nil, nil
	}

	// Flatten and sort: most-recently-active first; lex-lower name breaks ties.
	sessions := make([]tmux.Session, 0, len(seen))
	for _, s := range seen {
		sessions = append(sessions, s)
	}
	sort.Slice(sessions, func(i, j int) bool {
		a, b := sessions[i], sessions[j]
		// Zero LastActivityAt is treated as the lowest possible time, so
		// sessions with a real activity time always rank before those without.
		aZero := a.LastActivityAt.IsZero()
		bZero := b.LastActivityAt.IsZero()
		if aZero != bZero {
			// whichever has a real time goes first (i is "less" in sort order
			// = appears earlier = "wins").
			return !aZero // a has real time, b does not → a wins
		}
		if !a.LastActivityAt.Equal(b.LastActivityAt) {
			return a.LastActivityAt.After(b.LastActivityAt) // later = higher priority
		}
		// Deterministic tie-break: lex-lower name wins.
		return a.Key.Name < b.Key.Name
	})

	return projectSession(sessions[0]), nil
}

// matchPanesForWorktree enumerates every pane in snap that:
//  1. Lives on the same host as obj (attribution via pane.Key.Host).
//  2. Has a CurrentPid that the ps provider can resolve to a cwd.
//  3. Has a cwd that equals obj.Path exactly OR starts with obj.Path+"/".
//
// Panes whose cwd cannot be resolved are silently skipped. The returned
// slice is unsorted; callers sort by paneId.
func matchPanesForWorktree(ctx context.Context, r *worktreeResolver, snap tmux.RuntimeSnapshot, obj *graphql1.Worktree) ([]*graphql1.TmuxPane, error) {
	var matching []*graphql1.TmuxPane

	for _, pane := range snap.Panes {
		// Federation attribution: use pane.Key.Host (which tracks through
		// pane.window.session.host). Never synthesise from the local daemon.
		if string(pane.Key.Host) != obj.Host {
			continue
		}

		// Skip panes with no foreground pid.
		if pane.CurrentPid == 0 {
			continue
		}

		// Resolve cwd via the ps provider — same code path as
		// processResolver.Cwd (#463).
		if r.PS == nil {
			continue
		}
		cwd, err := r.PS.LoadCwd(ctx, pane.CurrentPid)
		if err != nil {
			// Silently skip panes whose cwd cannot be resolved.
			continue
		}
		if cwd == "" {
			continue
		}

		if !cwdMatchesWorktree(obj.Path, cwd) {
			continue
		}

		matching = append(matching, projectPane(pane))
	}

	if matching == nil {
		return []*graphql1.TmuxPane{}, nil
	}
	return matching, nil
}

// cwdMatchesWorktree returns true when cwd equals path exactly or is
// immediately under path (i.e. starts with path+"/").
//
// The trailing "/" guard prevents false positives: given path="/repo/feat-x",
// the cwd "/repo/feat-xtra" must NOT match even though it shares a prefix.
func cwdMatchesWorktree(path, cwd string) bool {
	return cwd == path || strings.HasPrefix(cwd, path+"/")
}
