// Package gh is the GitHub domain module for the orchard daemon.
//
// Architecture (R1, R2):
//
//	service.go    — Service interface, the ONLY import surface for consumers (R2).
//	provider.go   — In-process cache + stale-while-revalidate (O12). Internal.
//	adapter.go    — HTTP I/O (REST + GraphQL), auth shellout, pagination. Internal.
//	resolver_*.go — One file per owned GraphQL type (R6). Thin Load()+projection.
//	loaders.go    — DataLoaders per ADR-022 axes (R3, O1).
//	mutations.go  — L5: each mutation execs scripts/<op> --json.
//	subscriptions.go — R16: emit AFTER cache write.
//
// O12 contract: enrichment fields (mergeable, statusCheckRollup, …) are
// served stale-while-revalidating when GitHub is rate-limited; the
// staleness window is documented per field in provider.go.
//
// S16 contract: typed core (queries) + pass-through escape hatch
// (Query.gh) with L4 guards enforced in resolver_passthrough.go.
package gh

import (
	"context"
	"log/slog"
	"time"
)

// Service is the R2 contract — the only API surface consumers may import.
// Resolvers, loaders, and other domains (e.g. git's Worktree.pr back-edge)
// depend on this interface, never on *Provider directly.
//
// Per R4 (ISP), consumers define a NARROWER interface in their own module
// that embeds only the methods they actually call.
//
// R11: NewService returns *service (concrete), not Service (interface).
// Callers that need the interface declare it themselves.
type Service interface {
	// --- PullRequest operations ---

	ListPullRequests(ctx context.Context, owner, name string, state PullRequestState) ([]PullRequest, error)
	GetPullRequest(ctx context.Context, key PullRequestKey) (PullRequest, error)
	// EnrichPullRequest fetches GraphQL-only enrichment fields (mergeable,
	// mergeStateStatus, reviewDecision, statusCheckRollup, labels) and
	// merges them into the per-key cache.
	//
	// O12: serves stale enrichment when GitHub is rate-limited or when
	// the network call fails transiently. Staleness contract: up to 1h
	// stale on rate-limit; up to enrichmentTTL (5m) on happy path.
	// UNKNOWN mergeable is never cached — always re-fetches (#367).
	EnrichPullRequest(ctx context.Context, key PullRequestKey) (PullRequest, error)
	// BatchEnrichPullRequests collapses enrichment for multiple PRs into
	// one GitHub GraphQL round-trip per unique (owner, name) pair.
	BatchEnrichPullRequests(ctx context.Context, keys []PullRequestKey) (map[PullRequestKey]PullRequest, error)
	ListPullRequestReviews(ctx context.Context, owner, name string, number int) ([]PullRequestReview, error)
	ListPullRequestComments(ctx context.Context, owner, name string, number int) ([]IssueComment, error)

	// --- Issue operations ---

	ListIssues(ctx context.Context, owner, name string, state IssueState) ([]Issue, error)
	GetIssue(ctx context.Context, key IssueKey) (Issue, error)
	ListIssueComments(ctx context.Context, owner, name string, number int) ([]IssueComment, error)
	// EnrichIssueDependencies fetches the four GitHub dependency edges
	// (blockedBy, blocking, subIssues, parent). TTL: issueDepsTTL (60s).
	EnrichIssueDependencies(ctx context.Context, key IssueKey) (IssueDependencies, error)

	// --- WorkflowRun operations ---

	ListWorkflowRuns(ctx context.Context, owner, name string) ([]WorkflowRun, error)
	GetWorkflowRun(ctx context.Context, key WorkflowRunKey) (WorkflowRun, error)

	// --- Repository ---

	GetRepository(ctx context.Context, owner, name string) (Repository, error)

	// --- Pass-through (S16b) ---

	// GraphQL forwards an arbitrary GitHub GraphQL document using the
	// daemon's credentials. Not cached. Caller must not nest inside a
	// list or subscription resolver (S16b guard enforced at the resolver
	// layer in resolver_passthrough.go).
	GraphQL(ctx context.Context, query string, variables map[string]any) (map[string]any, error)

	// --- Subscription ---

	// Subscribe returns a channel of InvalidationEvents. The caller must
	// cancel ctx to unsubscribe; the channel is closed when ctx is done.
	// R12: returns receive-only channel.
	Subscribe(ctx context.Context) <-chan InvalidationEvent

	// --- Lifecycle ---

	// Start primes the auth bootstrap. Non-fatal on failure; the provider
	// surfaces auth errors per-field.
	Start(ctx context.Context) error
}

// InvalidationEvent is a cache-invalidation signal pushed by the webhook
// handler or by provider-internal rate-limit recovery. Key is the
// GraphQL node id (e.g. "PullRequest:owner/repo#42").
type InvalidationEvent struct {
	Key    string
	Reason string
	At     time.Time
}

// service wraps *Provider and satisfies Service. It is the bridge
// between the public interface and the internal provider implementation.
type service struct {
	p *Provider
}

// NewService constructs the service from a Provider. The returned
// *service satisfies Service but is returned as a concrete type per R11.
// Callers that need the Service interface assign it:
//
//	var svc gh.Service = gh.NewService(p)
func NewService(logger *slog.Logger, baseURL string) *service {
	p := New(logger, baseURL)
	return &service{p: p}
}

// NewServiceWith is the test-friendly constructor.
func NewServiceWith(logger *slog.Logger, baseURL string, auth AuthSource, clock func() time.Time) *service {
	p := NewWith(logger, baseURL, auth, clock)
	return &service{p: p}
}

func (s *service) Start(ctx context.Context) error { return s.p.Start(ctx) }

func (s *service) ListPullRequests(ctx context.Context, owner, name string, state PullRequestState) ([]PullRequest, error) {
	return s.p.ListPullRequests(ctx, owner, name, state)
}
func (s *service) GetPullRequest(ctx context.Context, key PullRequestKey) (PullRequest, error) {
	return s.p.GetPullRequest(ctx, key)
}
func (s *service) EnrichPullRequest(ctx context.Context, key PullRequestKey) (PullRequest, error) {
	return s.p.EnrichPullRequest(ctx, key)
}
func (s *service) BatchEnrichPullRequests(ctx context.Context, keys []PullRequestKey) (map[PullRequestKey]PullRequest, error) {
	return s.p.BatchEnrichPullRequests(ctx, keys)
}
func (s *service) ListPullRequestReviews(ctx context.Context, owner, name string, number int) ([]PullRequestReview, error) {
	return s.p.ListPullRequestReviews(ctx, owner, name, number)
}
func (s *service) ListPullRequestComments(ctx context.Context, owner, name string, number int) ([]IssueComment, error) {
	return s.p.ListPullRequestComments(ctx, owner, name, number)
}

func (s *service) ListIssues(ctx context.Context, owner, name string, state IssueState) ([]Issue, error) {
	return s.p.ListIssues(ctx, owner, name, state)
}
func (s *service) GetIssue(ctx context.Context, key IssueKey) (Issue, error) {
	return s.p.GetIssue(ctx, key)
}
func (s *service) ListIssueComments(ctx context.Context, owner, name string, number int) ([]IssueComment, error) {
	return s.p.ListIssueComments(ctx, owner, name, number)
}
func (s *service) EnrichIssueDependencies(ctx context.Context, key IssueKey) (IssueDependencies, error) {
	return s.p.EnrichIssueDependencies(ctx, key)
}

func (s *service) ListWorkflowRuns(ctx context.Context, owner, name string) ([]WorkflowRun, error) {
	return s.p.ListWorkflowRuns(ctx, owner, name)
}
func (s *service) GetWorkflowRun(ctx context.Context, key WorkflowRunKey) (WorkflowRun, error) {
	return s.p.GetWorkflowRun(ctx, key)
}

func (s *service) GetRepository(ctx context.Context, owner, name string) (Repository, error) {
	return s.p.GetRepository(ctx, owner, name)
}

func (s *service) GraphQL(ctx context.Context, query string, variables map[string]any) (map[string]any, error) {
	return s.p.GraphQL(ctx, query, variables)
}

func (s *service) Subscribe(ctx context.Context) <-chan InvalidationEvent {
	return s.p.Subscribe(ctx)
}

// compile-time assertion: *service satisfies Service.
var _ Service = (*service)(nil)
