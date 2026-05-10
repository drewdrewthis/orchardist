// worktree_claude.go holds the helpers backing Worktree.claudeInstances
// (#540): server-side join of Claude REPL process cwd against worktree
// path so clients don't repeat the cwd→path matching work.
//
// Mirrors worktree_tmux.go in shape: the resolver methods themselves
// live in schema.resolvers.go (gqlgen owns that file); the helpers stay
// here.

package resolvers

import (
	"context"
	"sort"

	graphql1 "github.com/drewdrewthis/git-orchard-rs/internal/server/graphql"
)

// matchClaudeInstancesForWorktree enumerates every ClaudeInstance the
// daemon knows about and keeps only those whose resolved process cwd
// equals obj.Path exactly OR starts with obj.Path+"/". Same matching
// rule as matchPanesForWorktree (worktree_tmux.go) — the worktree path
// is the canonical anchor.
//
// Resolves cwd via the ps provider (LoadCwd by pid) when the
// ClaudeInstance carries a non-zero process pid. Instances whose pid is
// unknown OR whose cwd cannot be resolved are silently skipped — same
// contract as the tmux side.
//
// Returns the matching instances in deterministic id-ascending order.
// Never returns nil.
func matchClaudeInstancesForWorktree(ctx context.Context, r *worktreeResolver, obj *graphql1.Worktree) ([]*graphql1.ClaudeInstance, error) {
	if r.ClaudeInstanceProvider == nil {
		return []*graphql1.ClaudeInstance{}, nil
	}

	all, err := r.ClaudeInstanceProvider.List(ctx)
	if err != nil {
		return nil, err
	}

	out := make([]*graphql1.ClaudeInstance, 0, len(all))
	for _, inst := range all {
		if inst == nil {
			continue
		}
		// Federation attribution: instance must live on the same host
		// as the worktree. Worktree.Host is set by toGraphQLWorktree
		// (currently "local"; ws-F populates per-peer).
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
// ps lookup by pid. Returns "" when no cwd can be derived (no process,
// no pid, or ps provider unwired).
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
//
// "Deepest" matters because nested worktrees share a prefix — given
// `/repo` and `/repo/.worktrees/feat`, a cwd of `/repo/.worktrees/feat/src`
// must match the inner worktree, not the outer one.
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
