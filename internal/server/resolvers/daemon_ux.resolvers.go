// Resolvers for the daemon-UX bundle (#469): SchemaSDL, WorkView,
// DaemonState, and the per-type subscriptions (tmuxSessionsChanged,
// pullRequestChanged, runChanged, worktreeChanged).
//
// Kept in its own file so schema.resolvers.go (gqlgen-managed) stays
// codegen-friendly. Methods here attach to the same queryResolver /
// subscriptionResolver receivers declared at the bottom of
// schema.resolvers.go.

package resolvers

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/drewdrewthis/git-orchard-rs/internal/server/adapter"
	graphql1 "github.com/drewdrewthis/git-orchard-rs/internal/server/graphql"
	gitprovider "github.com/drewdrewthis/git-orchard-rs/internal/server/providers/git"
	tmuxprovider "github.com/drewdrewthis/git-orchard-rs/internal/server/providers/tmux"
)

// metaProviderWorkView labels the Meta envelope returned alongside the
// composite WorkView. Stable so clients can switch on it.
const metaProviderWorkView = "workView"

// SchemaSDL is the resolver for Query.schemaSDL (#469 F10).
//
// Returns the daemon's embedded schema.graphql contents so agents can
// self-describe even when introspection is disabled or the source tree
// isn't on disk.
func (r *queryResolver) SchemaSdl(ctx context.Context) (string, error) {
	return SchemaSDL(), nil
}

// WorkView is the resolver for Query.workView (#469 F6).
//
// Walks the local projects → worktrees graph and joins each worktree
// to its open PR, linked issue, processes, and tmux sessions in a
// single round trip. Uses existing per-type resolvers so semantics
// match per-type queries.
func (r *queryResolver) WorkView(ctx context.Context) (*graphql1.WorkView, error) {
	q := r.Resolver.Query()

	projects, projectsErr := q.Projects(ctx)
	sessions, sessionsErr := q.TmuxSessions(ctx, nil)
	instances, instancesErr := q.ClaudeInstances(ctx)

	view := &graphql1.WorkView{
		Projects:        projects,
		TmuxSessions:    sessions,
		ClaudeInstances: instances,
		Meta: &graphql1.Meta{
			Provider:              metaProviderWorkView,
			LastSuccessfulFetchAt: nowRFC3339(),
		},
	}

	// Fold non-fatal sub-errors into the Meta envelope so callers can
	// distinguish "valid empty" from "data unavailable" (#469 F1)
	// without the whole composite failing.
	reasons := make([]string, 0, 3)
	if projectsErr != nil {
		reasons = append(reasons, "projects: "+projectsErr.Error())
	}
	if sessionsErr != nil {
		reasons = append(reasons, "tmuxSessions: "+sessionsErr.Error())
	}
	if instancesErr != nil {
		reasons = append(reasons, "claudeInstances: "+instancesErr.Error())
	}
	if len(reasons) > 0 {
		joined := strings.Join(reasons, "; ")
		view.Meta.FailureReason = &joined
		view.Meta.LastSuccessfulFetchAt = nil
	}

	if view.Projects == nil {
		view.Projects = []*graphql1.Project{}
	}
	if view.TmuxSessions == nil {
		view.TmuxSessions = []*graphql1.TmuxSession{}
	}
	if view.ClaudeInstances == nil {
		view.ClaudeInstances = []*graphql1.ClaudeInstance{}
	}

	return view, nil
}

// DaemonState is the resolver for Query.daemonState (#469 F9).
//
// Reports per-provider configuration plus daemon-wide startedAt /
// uptime. Per-provider counters (refreshCount/failureCount) are
// reserved for follow-up wiring — providers don't yet expose a stable
// counter surface, so v1 returns 0/nil and the field is documented as
// best-effort.
func (r *queryResolver) DaemonState(ctx context.Context) (*graphql1.DaemonState, error) {
	startedAt := r.StartedAt.UTC().Format(time.RFC3339)
	uptime := int64(time.Since(r.StartedAt).Round(time.Second).Seconds())

	providers := []*graphql1.ProviderHealth{
		{Name: "host", Configured: r.HostProvider != nil},
		{Name: "git", Configured: r.Git != nil},
		{Name: "ps", Configured: r.PS != nil},
		{Name: "tmux", Configured: r.Tmux != nil},
		{Name: "claudeProjects", Configured: r.ClaudeProjects != nil},
		{Name: "claudeAccount", Configured: r.ClaudeAccount != nil},
		{Name: "claudeInstance", Configured: r.ClaudeInstance != nil},
		{Name: "hostService", Configured: r.HostServiceProvider != nil},
		{Name: "contracts", Configured: r.ContractsProvider != nil},
		{Name: "gh", Configured: r.GH != nil},
		{Name: "peerProxy", Configured: r.PeerProxy != nil},
	}

	return &graphql1.DaemonState{
		StartedAt: startedAt,
		UptimeS:   uptime,
		Providers: providers,
	}, nil
}

// TmuxSessionsChanged is the resolver for Subscription.tmuxSessionsChanged
// (#469 F7). Emits a fresh snapshot of cached tmux sessions whenever the
// tmux provider invalidates.
func (r *subscriptionResolver) TmuxSessionsChanged(ctx context.Context) (<-chan []*graphql1.TmuxSession, error) {
	if r.Tmux == nil {
		return nil, fmt.Errorf("tmux provider not configured")
	}
	src := r.Tmux.Sessions().Subscribe(ctx)
	out := make(chan []*graphql1.TmuxSession, 1)
	go func() {
		defer close(out)
		for {
			select {
			case <-ctx.Done():
				return
			case _, ok := <-src:
				if !ok {
					return
				}
				snap := r.Tmux.Snapshot()
				sessions := make([]*graphql1.TmuxSession, 0, len(snap.Sessions))
				for _, s := range snap.Sessions {
					sessions = append(sessions, projectSession(s))
				}
				select {
				case out <- sessions:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return out, nil
}

// PullRequestChanged is the resolver for Subscription.pullRequestChanged
// (#469 F7). Emits the freshly-loaded PullRequest each time the gh
// provider invalidates an id matching repo+number.
func (r *subscriptionResolver) PullRequestChanged(ctx context.Context, repo string, number int64) (<-chan *graphql1.PullRequest, error) {
	if r.GH == nil {
		return nil, errGHNotConfigured
	}
	if _, _, ok := splitRepo(repo); !ok {
		return nil, fmt.Errorf("pullRequestChanged: malformed repo %q (want owner/name)", repo)
	}
	// gh provider keys PullRequest events as `PullRequest:<owner>/<repo>#<number>`.
	wantKey := fmt.Sprintf("PullRequest:%s#%d", repo, number)
	src := r.GH.Subscribe(ctx)
	out := make(chan *graphql1.PullRequest, 1)
	go func() {
		defer close(out)
		for {
			select {
			case <-ctx.Done():
				return
			case ev, ok := <-src:
				if !ok {
					return
				}
				if ev.Key != wantKey {
					continue
				}
				pr, err := r.Resolver.Query().PullRequest(ctx, repo, number)
				if err != nil || pr == nil {
					continue
				}
				select {
				case out <- pr:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return out, nil
}

// RunChanged is the resolver for Subscription.runChanged (#469 F7).
// Emits matching WorkflowRun snapshots when the gh provider invalidates.
func (r *subscriptionResolver) RunChanged(ctx context.Context, repo string, branch string) (<-chan *graphql1.WorkflowRun, error) {
	if r.GH == nil {
		return nil, errGHNotConfigured
	}
	if _, _, ok := splitRepo(repo); !ok {
		return nil, fmt.Errorf("runChanged: malformed repo %q (want owner/name)", repo)
	}
	src := r.GH.Subscribe(ctx)
	out := make(chan *graphql1.WorkflowRun, 1)
	go func() {
		defer close(out)
		for {
			select {
			case <-ctx.Done():
				return
			case ev, ok := <-src:
				if !ok {
					return
				}
				if !strings.HasPrefix(ev.Key, "WorkflowRun:"+repo+"#") {
					continue
				}
				runs, err := r.Resolver.Query().WorkflowRuns(ctx, repo)
				if err != nil {
					continue
				}
				for _, run := range runs {
					if run == nil || run.HeadBranch != branch {
						continue
					}
					if ev.Key != fmt.Sprintf("WorkflowRun:%s#%d", repo, run.RunID) {
						continue
					}
					select {
					case out <- run:
					case <-ctx.Done():
						return
					}
					break
				}
			}
		}
	}()
	return out, nil
}

// WorktreeChanged is the resolver for Subscription.worktreeChanged
// (#469 F7). Emits the project's full worktree list whenever the git
// provider invalidates any worktree belonging to the named project.
func (r *subscriptionResolver) WorktreeChanged(ctx context.Context, project string) (<-chan []*graphql1.Worktree, error) {
	if r.Git == nil {
		return nil, fmt.Errorf("git provider not configured")
	}
	src := r.Git.Subscribe(ctx)
	out := make(chan []*graphql1.Worktree, 1)
	go func() {
		defer close(out)
		for {
			select {
			case <-ctx.Done():
				return
			case ev, ok := <-src:
				if !ok {
					return
				}
				if !worktreeEventMatchesProject(ev, project) {
					continue
				}
				worktrees, err := r.Git.ListByProject(ctx, project)
				if err != nil {
					continue
				}
				out2 := make([]*graphql1.Worktree, 0, len(worktrees))
				for _, w := range worktrees {
					out2 = append(out2, toGraphQLWorktree(w))
				}
				select {
				case out <- out2:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return out, nil
}

// nowRFC3339 returns the current wall-clock time in RFC3339 form.
// Wrapped so tests can monkey-patch in the future if needed.
func nowRFC3339() *string {
	t := time.Now().UTC().Format(time.RFC3339)
	return &t
}

// splitRepo splits an "owner/name" repo coordinate. Returns ok=false on
// malformed input (missing slash, empty parts).
func splitRepo(repo string) (owner, name string, ok bool) {
	idx := strings.Index(repo, "/")
	if idx <= 0 || idx == len(repo)-1 {
		return "", "", false
	}
	return repo[:idx], repo[idx+1:], true
}

// worktreeEventMatchesProject is a best-effort filter that decides
// whether a git invalidation belongs to the requested project. The git
// provider keys events on `<projectID>:<worktreeName>`, so the project
// id is the prefix.
func worktreeEventMatchesProject(ev adapter.InvalidationEvent[gitprovider.WorktreeID], project string) bool {
	key := string(ev.Key)
	if key == "" || project == "" {
		return false
	}
	prefix := project + ":"
	return strings.HasPrefix(key, prefix) || key == project
}

// _ keeps the tmuxprovider import alive when the build path doesn't
// exercise the tmux subscription helper directly. The import is needed
// for the Sessions() / Snapshot() methods used above.
var _ = tmuxprovider.SessionKey{}
