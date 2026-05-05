package gh

import (
	"context"

	"github.com/drewdrewthis/git-orchard-rs/internal/server/adapter"
)

// PRAdapter wraps the gh Client into the Adapter[PullRequestKey, PullRequest]
// surface. Stateless — the provider above it owns cache + watcher.
//
// The adapter does not know about caching, TTLs, or invalidation. It
// is just the I/O layer between the provider and the GitHub API.
type PRAdapter struct {
	Client *Client
}

// NewPRAdapter constructs a PRAdapter.
func NewPRAdapter(c *Client) *PRAdapter { return &PRAdapter{Client: c} }

// Fetch implements adapter.Adapter.
func (a *PRAdapter) Fetch(ctx context.Context, key PullRequestKey) (PullRequest, error) {
	return a.Client.GetPull(ctx, key.Owner, key.Name, key.Number)
}

// FetchAll implements adapter.Adapter. Returns an empty map — the gh
// adapter cannot enumerate every PR across every repo without explicit
// scoping. Provider-level enumeration happens via ListPulls(repo, state)
// which is not represented in the Adapter[K,V] contract.
func (a *PRAdapter) FetchAll(_ context.Context) (map[PullRequestKey]PullRequest, error) {
	return map[PullRequestKey]PullRequest{}, nil
}

// Watch implements adapter.Adapter. Returns a closed channel — webhook
// pushes drive invalidation via Provider.Subscribe, not via this
// adapter's Watch. Keeps the contract satisfied without misrepresenting
// the data flow.
func (a *PRAdapter) Watch(ctx context.Context) (<-chan PullRequestKey, error) {
	ch := make(chan PullRequestKey)
	close(ch)
	return ch, nil
}

// Close implements adapter.Adapter.
func (a *PRAdapter) Close() error { return nil }

// IssueAdapter wraps the gh Client into Adapter[IssueKey, Issue].
type IssueAdapter struct {
	Client *Client
}

// NewIssueAdapter constructs an IssueAdapter.
func NewIssueAdapter(c *Client) *IssueAdapter { return &IssueAdapter{Client: c} }

// Fetch implements adapter.Adapter.
func (a *IssueAdapter) Fetch(ctx context.Context, key IssueKey) (Issue, error) {
	return a.Client.GetIssue(ctx, key.Owner, key.Name, key.Number)
}

// FetchAll implements adapter.Adapter. See PRAdapter.FetchAll.
func (a *IssueAdapter) FetchAll(_ context.Context) (map[IssueKey]Issue, error) {
	return map[IssueKey]Issue{}, nil
}

// Watch implements adapter.Adapter. See PRAdapter.Watch.
func (a *IssueAdapter) Watch(_ context.Context) (<-chan IssueKey, error) {
	ch := make(chan IssueKey)
	close(ch)
	return ch, nil
}

// Close implements adapter.Adapter.
func (a *IssueAdapter) Close() error { return nil }

// WorkflowRunAdapter wraps the gh Client into
// Adapter[WorkflowRunKey, WorkflowRun].
type WorkflowRunAdapter struct {
	Client *Client
}

// NewWorkflowRunAdapter constructs a WorkflowRunAdapter.
func NewWorkflowRunAdapter(c *Client) *WorkflowRunAdapter { return &WorkflowRunAdapter{Client: c} }

// Fetch implements adapter.Adapter.
func (a *WorkflowRunAdapter) Fetch(ctx context.Context, key WorkflowRunKey) (WorkflowRun, error) {
	return a.Client.GetWorkflowRun(ctx, key.Owner, key.Name, key.RunID)
}

// FetchAll implements adapter.Adapter. See PRAdapter.FetchAll.
func (a *WorkflowRunAdapter) FetchAll(_ context.Context) (map[WorkflowRunKey]WorkflowRun, error) {
	return map[WorkflowRunKey]WorkflowRun{}, nil
}

// Watch implements adapter.Adapter. See PRAdapter.Watch.
func (a *WorkflowRunAdapter) Watch(_ context.Context) (<-chan WorkflowRunKey, error) {
	ch := make(chan WorkflowRunKey)
	close(ch)
	return ch, nil
}

// Close implements adapter.Adapter.
func (a *WorkflowRunAdapter) Close() error { return nil }

// Compile-time checks: each adapter satisfies the generic
// adapter.Adapter[K, V] interface for its node type.
var _ adapter.Adapter[PullRequestKey, PullRequest] = (*PRAdapter)(nil)
var _ adapter.Adapter[IssueKey, Issue] = (*IssueAdapter)(nil)
var _ adapter.Adapter[WorkflowRunKey, WorkflowRun] = (*WorkflowRunAdapter)(nil)
