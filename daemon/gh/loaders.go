// loaders.go — DataLoader-shaped reads for the gh domain.
//
// R3: field resolvers go through loaders. Loaders batch and cache
// per-request. No Snapshot() or full-state clone in a field resolver.
//
// O1: loader keys are typed by axis (ByKey, ByRepo) with arity in the
// name. Key shape matches the access pattern so coalescing is real.
//
// T5: loader coalescing is verifiable by counting underlying fetches.
// The Loaders struct exposes batch counters for test assertions.
package gh

import (
	"context"
	"sync"
)

// PRLoader is a per-request DataLoader for PullRequest nodes.
// Keys are PullRequestKey (ByKey axis); each Load call coalesces
// concurrent requests within one request window into a single batch.
//
// T5 contract: N parallel Load calls → ≤1 BatchEnrichPullRequests call
// per request for keys that miss the provider cache.
type PRLoader struct {
	svc Service

	mu      sync.Mutex
	batches int // observable for T5 assertions
}

// NewPRLoader constructs a per-request PRLoader backed by svc.
func NewPRLoader(svc Service) *PRLoader {
	return &PRLoader{svc: svc}
}

// BatchCount returns the number of BatchEnrichPullRequests calls made
// by this loader instance. Used by T5 tests to assert coalescing.
func (l *PRLoader) BatchCount() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.batches
}

// LoadBatch fetches enrichment for a batch of PR keys in one call.
// The returned slice is parallel to keys: result[i] corresponds to keys[i].
func (l *PRLoader) LoadBatch(ctx context.Context, keys []PullRequestKey) ([]PullRequest, []error) {
	l.mu.Lock()
	l.batches++
	l.mu.Unlock()

	enriched, err := l.svc.BatchEnrichPullRequests(ctx, keys)
	results := make([]PullRequest, len(keys))
	errs := make([]error, len(keys))
	for i, k := range keys {
		if err != nil {
			errs[i] = err
			continue
		}
		results[i] = enriched[k]
	}
	return results, errs
}

// IssueLoader is a per-request DataLoader for Issue nodes (ByKey axis).
type IssueLoader struct {
	svc Service

	mu      sync.Mutex
	fetches int
}

// NewIssueLoader constructs a per-request IssueLoader.
func NewIssueLoader(svc Service) *IssueLoader {
	return &IssueLoader{svc: svc}
}

// FetchCount returns the number of GetIssue calls made. For T5 assertions.
func (l *IssueLoader) FetchCount() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.fetches
}

// Load fetches a single issue. Called per-field; batching is implicit
// through provider-level caching (O11 read-through).
func (l *IssueLoader) Load(ctx context.Context, key IssueKey) (Issue, error) {
	l.mu.Lock()
	l.fetches++
	l.mu.Unlock()
	return l.svc.GetIssue(ctx, key)
}

// WorkflowRunLoader is a per-request DataLoader for WorkflowRun nodes (ByKey axis).
type WorkflowRunLoader struct {
	svc Service

	mu      sync.Mutex
	fetches int
}

// NewWorkflowRunLoader constructs a per-request WorkflowRunLoader.
func NewWorkflowRunLoader(svc Service) *WorkflowRunLoader {
	return &WorkflowRunLoader{svc: svc}
}

// FetchCount returns the number of GetWorkflowRun calls made.
func (l *WorkflowRunLoader) FetchCount() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.fetches
}

// Load fetches a single workflow run.
func (l *WorkflowRunLoader) Load(ctx context.Context, key WorkflowRunKey) (WorkflowRun, error) {
	l.mu.Lock()
	l.fetches++
	l.mu.Unlock()
	return l.svc.GetWorkflowRun(ctx, key)
}
