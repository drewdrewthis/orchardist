// resolver_pull_request.go — resolvers for the PullRequest GraphQL type.
//
// R6: ONE file per GraphQL type. This file owns all PullRequest field
// resolvers plus the Query fields that return PullRequest.
//
// R3: enrichment fields (Mergeable, MergeStateStatus, ReviewDecision,
// StatusCheckRollup, Labels) go through the PRLoader which routes to
// BatchEnrichPullRequests. No Snapshot() here.
//
// S16a: typed core — Query.pullRequests, Query.openPullRequests,
// Query.pullRequest, PullRequest.* enrichment fields.
package gh

import (
	"context"
	"fmt"
)

// PullRequestResolver provides the resolver bodies for the PullRequest type
// and for Query fields that return PullRequests.
// Resolver implementations wire in the Service (R2) via the constructor;
// the gqlgen aggregate resolver embeds this via composition.
type PullRequestResolver struct {
	Svc    Service
	Loader *PRLoader
}

// NewPullRequestResolver constructs a PullRequestResolver.
func NewPullRequestResolver(svc Service) *PullRequestResolver {
	return &PullRequestResolver{
		Svc:    svc,
		Loader: NewPRLoader(svc),
	}
}

// --- Query field resolvers ---

// QueryPullRequests implements Query.pullRequests(repo, state).
// Returns a slice of PullRequest or a per-field GraphQL error on auth failure.
func (r *PullRequestResolver) QueryPullRequests(ctx context.Context, repo string, state PullRequestState) ([]PullRequest, error) {
	if r.Svc == nil {
		return nil, ErrGHNotConfigured
	}
	owner, name, err := SplitRepo(repo)
	if err != nil {
		return nil, err
	}
	return r.Svc.ListPullRequests(ctx, owner, name, state)
}

// QueryOpenPullRequests implements Query.openPullRequests(repo, author).
// Lists every open PR, optionally filtered by author login.
func (r *PullRequestResolver) QueryOpenPullRequests(ctx context.Context, repo string, author *string) ([]PullRequest, error) {
	if r.Svc == nil {
		return nil, ErrGHNotConfigured
	}
	owner, name, err := SplitRepo(repo)
	if err != nil {
		return nil, err
	}
	prs, err := r.Svc.ListPullRequests(ctx, owner, name, PullRequestStateOpen)
	if err != nil {
		return nil, err
	}
	if author == nil || *author == "" {
		return prs, nil
	}
	out := prs[:0]
	for _, p := range prs {
		if p.AuthorLogin == *author {
			out = append(out, p)
		}
	}
	return out, nil
}

// QueryPullRequest implements Query.pullRequest(repo, number).
// Returns nil (GraphQL null) when not found.
func (r *PullRequestResolver) QueryPullRequest(ctx context.Context, repo string, number int) (*PullRequest, error) {
	if r.Svc == nil {
		return nil, ErrGHNotConfigured
	}
	owner, name, err := SplitRepo(repo)
	if err != nil {
		return nil, err
	}
	pr, err := r.Svc.GetPullRequest(ctx, PullRequestKey{Owner: owner, Name: name, Number: number})
	if err != nil {
		if IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	return &pr, nil
}

// --- PullRequest enrichment field resolvers (R3: through loader) ---

// ResolveEnrichment fetches enrichment for a PullRequest via the batch loader.
// Called by Mergeable, MergeStateStatus, ReviewDecision, StatusCheckRollup,
// and Labels resolvers.
func (r *PullRequestResolver) ResolveEnrichment(ctx context.Context, pr PullRequest) (PullRequest, error) {
	key, ok := prKeyFromID(pr.ID())
	if !ok {
		return pr, fmt.Errorf("gh: cannot parse PullRequest id %q", pr.ID())
	}
	if r.Loader != nil {
		// Route through batch loader for per-request coalescing (R3, T5).
		results, errs := r.Loader.LoadBatch(ctx, []PullRequestKey{key})
		if len(errs) > 0 && errs[0] != nil {
			return pr, errs[0]
		}
		if len(results) > 0 {
			return results[0], nil
		}
	}
	return r.Svc.EnrichPullRequest(ctx, key)
}

// --- PR comment / review field resolvers ---

// ResolveReviews fetches reviews for a PullRequest (lazy, not cached by provider).
func (r *PullRequestResolver) ResolveReviews(ctx context.Context, pr PullRequest) ([]PullRequestReview, error) {
	if r.Svc == nil {
		return nil, ErrGHNotConfigured
	}
	return r.Svc.ListPullRequestReviews(ctx, pr.RepoOwner, pr.RepoName, pr.Number)
}

// ResolveComments fetches conversation comments for a PullRequest.
func (r *PullRequestResolver) ResolveComments(ctx context.Context, pr PullRequest) ([]IssueComment, error) {
	if r.Svc == nil {
		return nil, ErrGHNotConfigured
	}
	return r.Svc.ListPullRequestComments(ctx, pr.RepoOwner, pr.RepoName, pr.Number)
}

// ErrGHNotConfigured is returned when the gh service was never wired.
// Surfaces as a per-field GraphQL error; the rest of the schema keeps resolving.
var ErrGHNotConfigured = fmt.Errorf("gh provider not configured")

// prKeyFromID parses a PullRequest GraphQL id "PullRequest:owner/repo#N".
func prKeyFromID(id string) (PullRequestKey, bool) {
	return splitGHNodeIDtoPR(id)
}

// splitGHNodeIDtoPR is a package-internal helper shared by resolver files.
// Parses "PullRequest:owner/repo#42" → (owner, repo, 42, true).
func splitGHNodeIDtoPR(id string) (PullRequestKey, bool) {
	const prefix = "PullRequest:"
	if len(id) <= len(prefix) {
		return PullRequestKey{}, false
	}
	tail := id[len(prefix):]
	// tail = "owner/repo#42"
	hashIdx := -1
	for i := len(tail) - 1; i >= 0; i-- {
		if tail[i] == '#' {
			hashIdx = i
			break
		}
	}
	if hashIdx <= 0 {
		return PullRequestKey{}, false
	}
	repoStr := tail[:hashIdx]
	numStr := tail[hashIdx+1:]
	slashIdx := -1
	for i, c := range repoStr {
		if c == '/' {
			slashIdx = i
			break
		}
	}
	if slashIdx <= 0 {
		return PullRequestKey{}, false
	}
	var n int
	if _, err := fmt.Sscanf(numStr, "%d", &n); err != nil {
		return PullRequestKey{}, false
	}
	return PullRequestKey{
		Owner:  repoStr[:slashIdx],
		Name:   repoStr[slashIdx+1:],
		Number: n,
	}, true
}
