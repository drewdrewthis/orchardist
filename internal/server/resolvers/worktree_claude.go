// worktree_claude.go holds the helpers backing Worktree.claudeInstances
// (#540): server-side join of Claude REPL process cwd against worktree
// path so clients don't repeat the cwd→path matching work.
//
// ADR-022 Phase 5: uses the pane-first path (Query.claudeInstances via
// projectPanesToClaudeInstances) instead of the heartbeat provider.
// Mirrors worktree_tmux.go in shape.

package resolvers

import (
	"context"
	"sort"

	graphql1 "github.com/drewdrewthis/git-orchard-rs/internal/server/graphql"
)

// matchClaudeInstancesForWorktree returns every ClaudeInstance whose
// resolved process cwd lies under obj.Path. Uses the pane-first
// Query.claudeInstances path (ADR-022 Phase 5).
//
// Returns [] (never nil).
func matchClaudeInstancesForWorktree(ctx context.Context, r *worktreeResolver, obj *graphql1.Worktree) ([]*graphql1.ClaudeInstance, error) {
	// Re-use the resolver-level ClaudeInstances which is already pane-first.
	all, err := r.Query().ClaudeInstances(ctx)
	if err != nil {
		return []*graphql1.ClaudeInstance{}, nil
	}

	out := make([]*graphql1.ClaudeInstance, 0, len(all))
	for _, inst := range all {
		if inst == nil {
			continue
		}
		// Federation attribution: instance must live on the same host
		// as the worktree.
		if inst.Process != nil && inst.Process.Host != nil &&
			inst.Process.Host.ID != obj.Host {
			continue
		}

		cwd := loadInstanceCwd(ctx, r, inst)
		if cwd == "" {
			continue
		}
		if !cwdMatchesWorktree(obj.Path, cwd) {
			continue
		}
		out = append(out, inst)
	}

	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// loadInstanceCwd returns the cwd for a ClaudeInstance, preferring the
// already-resolved process cwd when present and falling back to a fresh
// ps lookup by pid. Returns "" when no cwd can be derived.
func loadInstanceCwd(ctx context.Context, r *worktreeResolver, inst *graphql1.ClaudeInstance) string {
	if inst.Process == nil {
		return ""
	}
	if inst.Process.Cwd != nil && *inst.Process.Cwd != "" {
		return *inst.Process.Cwd
	}
	if r.PS == nil || inst.Process.Pid == 0 {
		return ""
	}
	cwd, err := r.PS.LoadCwd(ctx, int(inst.Process.Pid))
	if err != nil {
		return ""
	}
	return cwd
}

// findWorktreeForCwd walks every worktree the git provider knows about
// and returns the deepest match for cwd (longest path that contains
// cwd). Used by ClaudeInstance.worktree (the inverse of
// matchClaudeInstancesForWorktree). Returns (nil, nil) when no worktree
// contains cwd.
func findWorktreeForCwd(ctx context.Context, r *Resolver, cwd string) (*graphql1.Worktree, error) {
	if r.Git == nil || cwd == "" {
		return nil, nil
	}

	keys, err := r.Git.Keys(ctx)
	if err != nil {
		return nil, err
	}
	var best *graphql1.Worktree
	bestLen := -1
	for _, k := range keys {
		wt, _, err := r.Git.Get(ctx, k)
		if err != nil {
			continue
		}
		if !cwdMatchesWorktree(wt.Path, cwd) {
			continue
		}
		if len(wt.Path) > bestLen {
			best = toGraphQLWorktree(wt)
			bestLen = len(wt.Path)
		}
	}
	return best, nil
}
