// resolver_issue.go — resolvers for the Issue GraphQL type.
//
// R6: ONE file per GraphQL type.
// R3: dependency-edge fields (blockedByIssues, blockingIssues, subIssues,
// parentIssue) go through EnrichIssueDependencies; no Snapshot().
// S16a: typed core — Query.issues, Query.issue, Issue.* field resolvers.
package gh

import (
	"context"
	"fmt"
)

// IssueResolver provides the resolver bodies for the Issue type and
// for Query fields that return Issues.
type IssueResolver struct {
	Svc    Service
	Loader *IssueLoader
}

// NewIssueResolver constructs an IssueResolver.
func NewIssueResolver(svc Service) *IssueResolver {
	return &IssueResolver{
		Svc:    svc,
		Loader: NewIssueLoader(svc),
	}
}

// --- Query field resolvers ---

// QueryIssues implements Query.issues(repo, state).
func (r *IssueResolver) QueryIssues(ctx context.Context, repo string, state IssueState) ([]Issue, error) {
	if r.Svc == nil {
		return nil, ErrGHNotConfigured
	}
	owner, name, err := SplitRepo(repo)
	if err != nil {
		return nil, err
	}
	return r.Svc.ListIssues(ctx, owner, name, state)
}

// QueryIssue implements Query.issue(repo, number).
// Returns nil (GraphQL null) when not found.
func (r *IssueResolver) QueryIssue(ctx context.Context, repo string, number int) (*Issue, error) {
	if r.Svc == nil {
		return nil, ErrGHNotConfigured
	}
	owner, name, err := SplitRepo(repo)
	if err != nil {
		return nil, err
	}
	issue, err := r.Svc.GetIssue(ctx, IssueKey{Owner: owner, Name: name, Number: number})
	if err != nil {
		if IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	return &issue, nil
}

// --- Issue field resolvers (R3: through loader or enrichment) ---

// ResolveLabels fetches labels for an Issue.
// Labels are populated by the REST list/single endpoints, so this is
// a simple re-fetch via the IssueLoader (caching via provider).
func (r *IssueResolver) ResolveLabels(ctx context.Context, issue Issue) ([]Label, error) {
	if r.Loader == nil || r.Svc == nil {
		return issue.Labels, nil
	}
	refreshed, err := r.Loader.Load(ctx, IssueKey{Owner: issue.RepoOwner, Name: issue.RepoName, Number: issue.Number})
	if err != nil {
		// Fall back to what we already have on the issue struct.
		return issue.Labels, nil
	}
	return refreshed.Labels, nil
}

// ResolveDependencies fetches all four dependency edges for an Issue.
// Returns nil on soft-fail cases (gh not configured, malformed id).
func (r *IssueResolver) ResolveDependencies(ctx context.Context, issue Issue) (*IssueDependencies, error) {
	if r.Svc == nil {
		return nil, nil
	}
	key, ok := issueKeyFromID(issue.ID())
	if !ok {
		return nil, nil
	}
	deps, err := r.Svc.EnrichIssueDependencies(ctx, key)
	if err != nil {
		return nil, err
	}
	return &deps, nil
}

// ResolveComments fetches comments on an issue.
func (r *IssueResolver) ResolveComments(ctx context.Context, issue Issue) ([]IssueComment, error) {
	if r.Svc == nil {
		return nil, ErrGHNotConfigured
	}
	return r.Svc.ListIssueComments(ctx, issue.RepoOwner, issue.RepoName, issue.Number)
}

// issueKeyFromID parses "Issue:owner/repo#42".
func issueKeyFromID(id string) (IssueKey, bool) {
	const prefix = "Issue:"
	if len(id) <= len(prefix) {
		return IssueKey{}, false
	}
	tail := id[len(prefix):]
	hashIdx := -1
	for i := len(tail) - 1; i >= 0; i-- {
		if tail[i] == '#' {
			hashIdx = i
			break
		}
	}
	if hashIdx <= 0 {
		return IssueKey{}, false
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
		return IssueKey{}, false
	}
	var n int
	if _, err := fmt.Sscanf(numStr, "%d", &n); err != nil {
		return IssueKey{}, false
	}
	return IssueKey{
		Owner:  repoStr[:slashIdx],
		Name:   repoStr[slashIdx+1:],
		Number: n,
	}, true
}
