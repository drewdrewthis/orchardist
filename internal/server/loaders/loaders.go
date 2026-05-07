// Package loaders builds a request-scoped bag of DataLoader instances
// per ADR-011 §6 (resolver composition rules). One loader per
// (provider, request) — fresh on every GraphQL operation, never shared
// across requests, never owned by the global Resolver.
//
// The middleware in this package attaches a *Loaders pointer to the
// request context; resolvers retrieve it via FromContext and call the
// per-edge helpers that keep the dataloader/v7 generics out of the
// resolver code.
//
// Why per-request: DataLoader's whole job is "coalesce duplicate keys
// inside one logical batch." Sharing a loader across requests would
// either mix unrelated batches and serve stale reads, or require
// per-cache-tag invalidation we don't want to maintain.
package loaders

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/graph-gophers/dataloader/v7"

	graphql1 "github.com/drewdrewthis/git-orchard-rs/internal/server/graphql"
	configprovider "github.com/drewdrewthis/git-orchard-rs/internal/server/providers/config"
	ghprovider "github.com/drewdrewthis/git-orchard-rs/internal/server/providers/gh"
	gitprovider "github.com/drewdrewthis/git-orchard-rs/internal/server/providers/git"
	hostprovider "github.com/drewdrewthis/git-orchard-rs/internal/server/providers/host"
	psprovider "github.com/drewdrewthis/git-orchard-rs/internal/server/providers/ps"
)

// GHPullRequestLister is the narrow gh surface the PullRequestsForRepo
// loader needs. *gh.Provider satisfies this automatically. Defined as
// an interface so tests can inject a stub without standing up HTTP.
type GHPullRequestLister interface {
	ListPullRequests(ctx context.Context, owner, name string, state ghprovider.PullRequestState) ([]ghprovider.PullRequest, error)
}

// ProvidersBundle is the read-side surface the loaders need from the
// resolver root. A struct (not an interface) because the loaders only
// ever read pointers off it and tests can swap individual fields
// without writing N stub methods.
type ProvidersBundle struct {
	Host     *hostprovider.Provider
	Git      *gitprovider.Provider
	Ps       *psprovider.Provider
	Projects configprovider.Lister
	// GH is the narrow gh surface the PullRequestsForRepo loader needs.
	// *gh.Provider satisfies GHPullRequestLister automatically; tests
	// can inject a stub without standing up HTTP.
	GH GHPullRequestLister
}

// loaderKey is the private context key for the per-request loaders.
type loaderKey struct{}

// Loaders is the per-request bundle. One Loaders value lives for the
// lifetime of one GraphQL operation (query, mutation, or subscription
// emission). Every dataloader instance holds its own batched promise
// state.
type Loaders struct {
	Host               *dataloader.Loader[string, *graphql1.Host]
	WorktreeForCwd     *dataloader.Loader[string, *graphql1.Worktree]
	Process            *dataloader.Loader[ProcessKey, *graphql1.Process]
	PullRequestsForRepo *dataloader.Loader[RepoKey, []*graphql1.PullRequest]

	// metrics — provider call counts, used by the n+1 detector test.
	hostBatches     *batchCounter
	worktreeBatches *batchCounter
	processBatches  *batchCounter
	prBatches       *batchCounter
}

// RepoKey is the composite key for the PullRequestsForRepo loader.
// It identifies a GitHub repository by owner and name.
type RepoKey struct {
	Owner string
	Name  string
}

// String renders a RepoKey as "owner/name" — the canonical repo slug
// used in GraphQL IDs and human-readable log output.
func (k RepoKey) String() string {
	return fmt.Sprintf("%s/%s", k.Owner, k.Name)
}

// ProcessKey is the composite key for the [TmuxPane].process edge.
type ProcessKey struct {
	HostID string
	Pid    int
}

// String renders a ProcessKey as the canonical Process node id —
// useful for logging and for keeping the loader cache key
// human-readable.
func (k ProcessKey) String() string {
	return psprovider.ProcessID{Host: k.HostID, PID: k.Pid}.String()
}

// NewLoaders builds a fresh bundle of loaders bound to the given
// providers. The 1ms wait window matches the gqlgen handler tick —
// short enough that a query asking for many edges still feels
// synchronous, long enough that resolver fan-outs collapse into one
// batch.
func NewLoaders(providers *ProvidersBundle) *Loaders {
	hostBatches := &batchCounter{}
	worktreeBatches := &batchCounter{}
	processBatches := &batchCounter{}
	prBatches := &batchCounter{}

	hostBatch := func(_ context.Context, ids []string) []*dataloader.Result[*graphql1.Host] {
		hostBatches.inc()
		return loadHosts(providers, ids)
	}
	worktreeBatch := func(_ context.Context, cwds []string) []*dataloader.Result[*graphql1.Worktree] {
		worktreeBatches.inc()
		return loadWorktreesForCwds(providers, cwds)
	}
	processBatch := func(_ context.Context, keys []ProcessKey) []*dataloader.Result[*graphql1.Process] {
		processBatches.inc()
		return loadProcesses(providers, keys)
	}
	prBatch := func(ctx context.Context, keys []RepoKey) []*dataloader.Result[[]*graphql1.PullRequest] {
		prBatches.inc()
		return loadPullRequestsForRepo(ctx, providers, keys)
	}

	hostOpts := []dataloader.Option[string, *graphql1.Host]{
		dataloader.WithWait[string, *graphql1.Host](1 * time.Millisecond),
		dataloader.WithCache[string, *graphql1.Host](&dataloader.NoCache[string, *graphql1.Host]{}),
	}
	worktreeOpts := []dataloader.Option[string, *graphql1.Worktree]{
		dataloader.WithWait[string, *graphql1.Worktree](1 * time.Millisecond),
		dataloader.WithCache[string, *graphql1.Worktree](&dataloader.NoCache[string, *graphql1.Worktree]{}),
	}
	processOpts := []dataloader.Option[ProcessKey, *graphql1.Process]{
		dataloader.WithWait[ProcessKey, *graphql1.Process](1 * time.Millisecond),
		dataloader.WithCache[ProcessKey, *graphql1.Process](&dataloader.NoCache[ProcessKey, *graphql1.Process]{}),
	}
	prOpts := []dataloader.Option[RepoKey, []*graphql1.PullRequest]{
		dataloader.WithWait[RepoKey, []*graphql1.PullRequest](1 * time.Millisecond),
		dataloader.WithCache[RepoKey, []*graphql1.PullRequest](&dataloader.NoCache[RepoKey, []*graphql1.PullRequest]{}),
	}

	return &Loaders{
		Host:                dataloader.NewBatchedLoader(hostBatch, hostOpts...),
		WorktreeForCwd:      dataloader.NewBatchedLoader(worktreeBatch, worktreeOpts...),
		Process:             dataloader.NewBatchedLoader(processBatch, processOpts...),
		PullRequestsForRepo: dataloader.NewBatchedLoader(prBatch, prOpts...),
		hostBatches:         hostBatches,
		worktreeBatches:     worktreeBatches,
		processBatches:      processBatches,
		prBatches:           prBatches,
	}
}

// HostBatchCount returns the number of host-loader batch invocations
// since this Loaders was constructed.
func (l *Loaders) HostBatchCount() int { return l.hostBatches.value() }

// WorktreeBatchCount returns the number of worktree-loader batch
// invocations.
func (l *Loaders) WorktreeBatchCount() int { return l.worktreeBatches.value() }

// ProcessBatchCount returns the number of process-loader batch
// invocations.
func (l *Loaders) ProcessBatchCount() int { return l.processBatches.value() }

// PullRequestsForRepoBatchCount returns the number of PR-loader batch
// invocations since this Loaders was constructed. Used by n+1 tests.
func (l *Loaders) PullRequestsForRepoBatchCount() int { return l.prBatches.value() }

// Middleware wraps an http.Handler to attach a fresh *Loaders to the
// request context. Mount it once around the GraphQL handler.
func Middleware(providers *ProvidersBundle, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := WithLoaders(r.Context(), NewLoaders(providers))
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// WithLoaders attaches the Loaders to ctx. Useful for subscription
// emissions where the handler-level middleware doesn't apply (gqlgen's
// websocket transport spawns its own goroutine per emission).
func WithLoaders(ctx context.Context, l *Loaders) context.Context {
	return context.WithValue(ctx, loaderKey{}, l)
}

// FromContext pulls the Loaders out of ctx. Returns nil when no
// middleware has been wired — resolvers should fall back to the
// non-batched provider call in that case.
func FromContext(ctx context.Context) *Loaders {
	l, _ := ctx.Value(loaderKey{}).(*Loaders)
	return l
}

// loadHosts batches host id -> Host. v1 only knows the local host so
// the inner loop is trivial; the batch shape is what makes the test
// assertion possible.
func loadHosts(providers *ProvidersBundle, ids []string) []*dataloader.Result[*graphql1.Host] {
	out := make([]*dataloader.Result[*graphql1.Host], len(ids))
	hp := providers.Host
	if hp == nil {
		// No host provider — return a stub Host{ID:...} so the
		// resolver still gets *something* to project. Tests that wire
		// only the ps provider rely on this.
		for i, id := range ids {
			out[i] = &dataloader.Result[*graphql1.Host]{Data: &graphql1.Host{ID: id}}
		}
		return out
	}
	hosts, _, err := hp.GetMany(context.Background(), idsToHostKeys(ids))
	if err != nil {
		for i := range out {
			out[i] = &dataloader.Result[*graphql1.Host]{Error: err}
		}
		return out
	}
	for i, id := range ids {
		if h, ok := hosts[hostprovider.HostID(id)]; ok {
			out[i] = &dataloader.Result[*graphql1.Host]{Data: h}
			continue
		}
		// Foreign host id — fall back to a stub so node(id) lookups
		// keep the schema's nullable contract.
		out[i] = &dataloader.Result[*graphql1.Host]{Data: &graphql1.Host{ID: id}}
	}
	return out
}

func idsToHostKeys(ids []string) []hostprovider.HostID {
	out := make([]hostprovider.HostID, len(ids))
	for i, id := range ids {
		out[i] = hostprovider.HostID(id)
	}
	return out
}

// loadWorktreesForCwds is the dataloader batch fn — wraps the public
// WorktreesForCwds in the dataloader.Result envelope.
func loadWorktreesForCwds(providers *ProvidersBundle, cwds []string) []*dataloader.Result[*graphql1.Worktree] {
	values := WorktreesForCwds(providers, cwds)
	out := make([]*dataloader.Result[*graphql1.Worktree], len(cwds))
	for i, v := range values {
		out[i] = &dataloader.Result[*graphql1.Worktree]{Data: v}
	}
	return out
}

// WorktreesForCwds runs the cwd-prefix join in one pass: list every
// project, expand each project's worktrees, then for each requested
// cwd find the longest worktree path that is a prefix.
func WorktreesForCwds(providers *ProvidersBundle, cwds []string) []*graphql1.Worktree {
	out := make([]*graphql1.Worktree, len(cwds))
	gp := providers.Git
	pp := providers.Projects
	if gp == nil || pp == nil {
		return out
	}

	ctx := context.Background()
	rows, err := pp.List(ctx)
	if err != nil {
		return out
	}

	type record struct {
		worktree gitprovider.Worktree
	}
	var records []record
	for _, row := range rows {
		ws, listErr := gp.ListByProject(ctx, string(row.ID))
		if listErr != nil {
			continue
		}
		for _, w := range ws {
			records = append(records, record{worktree: w})
		}
	}

	for i, cwd := range cwds {
		var bestPath string
		var bestWT gitprovider.Worktree
		for _, rec := range records {
			if rec.worktree.Path == "" {
				continue
			}
			if !strings.HasPrefix(cwd, rec.worktree.Path) {
				continue
			}
			if len(rec.worktree.Path) > len(bestPath) {
				bestPath = rec.worktree.Path
				bestWT = rec.worktree
			}
		}
		if bestPath == "" {
			out[i] = nil
			continue
		}
		out[i] = &graphql1.Worktree{
			ID:     string(bestWT.ID),
			Path:   bestWT.Path,
			Branch: bestWT.Branch,
			Head:   bestWT.Head,
			Bare:   bestWT.Bare,
		}
	}
	return out
}

// loadProcesses batches (host, pid) keys -> Process via one
// providers.Ps.GetMany call.
func loadProcesses(providers *ProvidersBundle, keys []ProcessKey) []*dataloader.Result[*graphql1.Process] {
	out := make([]*dataloader.Result[*graphql1.Process], len(keys))
	psp := providers.Ps
	if psp == nil {
		for i := range out {
			out[i] = &dataloader.Result[*graphql1.Process]{Data: nil}
		}
		return out
	}
	pids := make([]psprovider.ProcessID, len(keys))
	for i, k := range keys {
		pids[i] = psprovider.ProcessID{Host: k.HostID, PID: k.Pid}
	}
	values, _, err := psp.GetMany(context.Background(), pids)
	if err != nil {
		for i := range out {
			out[i] = &dataloader.Result[*graphql1.Process]{Error: err}
		}
		return out
	}
	for i, k := range keys {
		key := psprovider.ProcessID{Host: k.HostID, PID: k.Pid}
		v, ok := values[key]
		if !ok {
			out[i] = &dataloader.Result[*graphql1.Process]{Data: nil}
			continue
		}
		out[i] = &dataloader.Result[*graphql1.Process]{Data: projectProcess(&v, k.HostID)}
	}
	return out
}

// loadPullRequestsForRepo is the DataLoader batch function for the
// PullRequestsForRepo loader. Each key is a RepoKey (owner/name pair);
// the batch collapses all concurrent resolver calls for the same repo
// into one underlying gh.Provider.ListPullRequests call.
//
// When NoCache is configured (as in production), the DataLoader may
// present duplicate keys in the same batch. This function deduplicates
// internally so the gh provider is called at most once per unique repo,
// regardless of how many Load calls arrived with the same key.
//
// Returns the full open-PR slice for each position in keys (including
// duplicate positions). The resolver layer does the headRef→branch match
// locally against this slice.
func loadPullRequestsForRepo(ctx context.Context, providers *ProvidersBundle, keys []RepoKey) []*dataloader.Result[[]*graphql1.PullRequest] {
	out := make([]*dataloader.Result[[]*graphql1.PullRequest], len(keys))
	ghp := providers.GH
	if ghp == nil {
		for i := range out {
			out[i] = &dataloader.Result[[]*graphql1.PullRequest]{Data: nil}
		}
		return out
	}

	// Deduplicate: fetch each unique repo exactly once, even when the
	// NoCache loader sends the same key multiple times in one batch.
	type repoResult struct {
		prs []*graphql1.PullRequest
		err error
	}
	cache := make(map[RepoKey]*repoResult, len(keys))
	for _, key := range keys {
		if _, seen := cache[key]; seen {
			continue
		}
		prs, err := ghp.ListPullRequests(ctx, key.Owner, key.Name, ghprovider.PullRequestStateOpen)
		if err != nil {
			cache[key] = &repoResult{err: err}
			continue
		}
		gqlPRs := make([]*graphql1.PullRequest, 0, len(prs))
		for _, p := range prs {
			pr := p // avoid loop-var capture
			gqlPRs = append(gqlPRs, projectPullRequest(pr))
		}
		cache[key] = &repoResult{prs: gqlPRs}
	}

	for i, key := range keys {
		r := cache[key]
		if r.err != nil {
			out[i] = &dataloader.Result[[]*graphql1.PullRequest]{Error: r.err}
		} else {
			out[i] = &dataloader.Result[[]*graphql1.PullRequest]{Data: r.prs}
		}
	}
	return out
}

// projectPullRequest projects a provider PullRequest into the GraphQL
// model. Duplicated from resolvers/gh.go (toGraphQLPullRequest) to
// keep the loaders package free of a circular import — the resolvers
// package imports loaders, so loaders cannot import resolvers.
func projectPullRequest(p ghprovider.PullRequest) *graphql1.PullRequest {
	return &graphql1.PullRequest{
		ID:          p.ID(),
		RepoOwner:   p.RepoOwner,
		RepoName:    p.RepoName,
		Number:      int64(p.Number),
		Title:       p.Title,
		Body:        p.Body,
		State:       mapPullRequestState(p.State),
		Draft:       p.Draft,
		AuthorLogin: p.AuthorLogin,
		BaseRef:     p.BaseRef,
		HeadRef:     p.HeadRef,
		URL:         p.URL,
		CreatedAt:   p.CreatedAt,
		UpdatedAt:   p.UpdatedAt,
	}
}

// mapPullRequestState maps the provider's PullRequestState enum to the
// generated GraphQL enum. Mirrors resolvers/gh.go mapPRStateBack without
// importing that package.
func mapPullRequestState(s ghprovider.PullRequestState) graphql1.PullRequestState {
	switch s {
	case ghprovider.PullRequestStateOpen:
		return graphql1.PullRequestStateOpen
	case ghprovider.PullRequestStateClosed:
		return graphql1.PullRequestStateClosed
	case ghprovider.PullRequestStateMerged:
		return graphql1.PullRequestStateMerged
	case ghprovider.PullRequestStateAll:
		return graphql1.PullRequestStateAll
	default:
		return graphql1.PullRequestStateOpen
	}
}

// projectProcess mirrors the resolver-layer projection so the loader
// returns a fully-formed Process value. Kept here so loaders is
// self-contained.
func projectProcess(p *psprovider.Process, hostID string) *graphql1.Process {
	tty := p.TTY
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
	if tty != "" {
		out.Tty = &tty
	}
	return out
}
