// Package resolvers — gh-backed resolver bodies.
//
// Workstream D-gh wires `Query.pullRequests`, `Query.issues`,
// `Query.workflowRuns`, plus `Project.pullRequests`/`Project.issues`
// and the per-PR/Issue review/comment edges, into the gh provider.
//
// Per-field GraphQL errors — when the daemon is not authenticated
// against GitHub the resolver returns the typed gh.ErrNotAuthenticated.
// gqlgen surfaces that as a per-field error in the GraphQL response,
// which is exactly the ADR-011 §6 / §12 contract: gh-derived fields
// fail individually while sibling fields (host, projects, ...) keep
// resolving.
package resolvers

import (
	"context"
	"fmt"

	graphql1 "github.com/drewdrewthis/git-orchard-rs/internal/server/graphql"
	"github.com/drewdrewthis/git-orchard-rs/internal/server/providers/gh"
)

// errGHNotConfigured is returned when the gh provider was never wired.
// This is an operator misconfiguration (missing WithGH option) and
// surfaces as a per-field GraphQL error so the rest of the schema
// continues to resolve.
var errGHNotConfigured = fmt.Errorf("gh provider not configured")

// queryPullRequestsResolver implements `Query.pullRequests(repo, state)`.
//
// Returns a slice of PullRequest, or a per-field GraphQL error when the
// daemon is not authenticated. The empty result is a valid GraphQL
// response — `[]` not `null` — so the resolver returns an allocated
// slice on the happy path.
func (r *queryResolver) queryPullRequestsResolver(ctx context.Context, repo string, state *graphql1.PullRequestState) ([]*graphql1.PullRequest, error) {
	if r.GH == nil {
		return nil, errGHNotConfigured
	}
	owner, name, err := gh.SplitRepo(repo)
	if err != nil {
		return nil, err
	}
	prs, err := r.GH.ListPullRequests(ctx, owner, name, mapPRState(state))
	if err != nil {
		return nil, err
	}
	out := make([]*graphql1.PullRequest, 0, len(prs))
	for _, p := range prs {
		out = append(out, toGraphQLPullRequest(p))
	}
	return out, nil
}

// queryIssuesResolver implements `Query.issues(repo, state)`.
func (r *queryResolver) queryIssuesResolver(ctx context.Context, repo string, state *graphql1.IssueState) ([]*graphql1.Issue, error) {
	if r.GH == nil {
		return nil, errGHNotConfigured
	}
	owner, name, err := gh.SplitRepo(repo)
	if err != nil {
		return nil, err
	}
	issues, err := r.GH.ListIssues(ctx, owner, name, mapIssueState(state))
	if err != nil {
		return nil, err
	}
	out := make([]*graphql1.Issue, 0, len(issues))
	for _, i := range issues {
		out = append(out, toGraphQLIssue(i))
	}
	return out, nil
}

// queryWorkflowRunsResolver implements `Query.workflowRuns(repo)`.
func (r *queryResolver) queryWorkflowRunsResolver(ctx context.Context, repo string) ([]*graphql1.WorkflowRun, error) {
	if r.GH == nil {
		return nil, errGHNotConfigured
	}
	owner, name, err := gh.SplitRepo(repo)
	if err != nil {
		return nil, err
	}
	runs, err := r.GH.ListWorkflowRuns(ctx, owner, name)
	if err != nil {
		return nil, err
	}
	out := make([]*graphql1.WorkflowRun, 0, len(runs))
	for _, w := range runs {
		out = append(out, toGraphQLWorkflowRun(w))
	}
	return out, nil
}

// queryGhResolver implements `Query.gh(query, variables)` — the
// pass-through into GitHub's GraphQL API. Issue #418.
//
// The `variables` argument arrives as `interface{}` because the JSON
// scalar can carry any shape. Two valid shapes: nil (no variables) or
// a `map[string]any` keyed by GraphQL variable name. Anything else is
// a caller bug; reject it as a per-field error rather than panicking.
//
// Auth-not-bootstrapped returns the typed `errGHNotConfigured` /
// `gh.ErrNotAuthenticated` so the resolver layer surfaces it as a
// per-field GraphQL error (the rest of the schema keeps resolving).
// GitHub-level GraphQL errors ride through inside the returned envelope
// — they are not Go errors here.
func (r *queryResolver) queryGhResolver(ctx context.Context, query string, variables interface{}) (interface{}, error) {
	if r.GH == nil {
		return nil, errGHNotConfigured
	}
	vars, err := coerceGhVariables(variables)
	if err != nil {
		return nil, err
	}
	return r.GH.GraphQL(ctx, query, vars)
}

// coerceGhVariables narrows the JSON-scalar input to the shape the gh
// client wants. nil is allowed (no variables); a `map[string]any` is
// the happy path. We tolerate `nil` from an absent argument; anything
// else is a misuse of the field that should surface clearly rather
// than smuggle a typed-cast panic into the resolver.
func coerceGhVariables(v interface{}) (map[string]any, error) {
	if v == nil {
		return nil, nil
	}
	if m, ok := v.(map[string]any); ok {
		return m, nil
	}
	return nil, fmt.Errorf("gh: variables must be a JSON object or null, got %T", v)
}

// projectPullRequestsResolver implements `Project.pullRequests(state)`.
//
// Resolves the project's `origin` remote → `owner/repo` and delegates
// to the gh provider. Projects whose origin is not a GitHub URL get an
// empty list — that is not an error; the project simply has no GitHub
// surface. An ErrNotAuthenticated from the provider does propagate.
func (r *projectResolver) projectPullRequestsResolver(ctx context.Context, obj *graphql1.Project, state *graphql1.PullRequestState) ([]*graphql1.PullRequest, error) {
	if r.GH == nil {
		return nil, errGHNotConfigured
	}
	owner, name, ok := projectGitHubRepo(obj)
	if !ok {
		return []*graphql1.PullRequest{}, nil
	}
	prs, err := r.GH.ListPullRequests(ctx, owner, name, mapPRState(state))
	if err != nil {
		return nil, err
	}
	out := make([]*graphql1.PullRequest, 0, len(prs))
	for _, p := range prs {
		out = append(out, toGraphQLPullRequest(p))
	}
	return out, nil
}

// projectIssuesResolver implements `Project.issues(state)`.
func (r *projectResolver) projectIssuesResolver(ctx context.Context, obj *graphql1.Project, state *graphql1.IssueState) ([]*graphql1.Issue, error) {
	if r.GH == nil {
		return nil, errGHNotConfigured
	}
	owner, name, ok := projectGitHubRepo(obj)
	if !ok {
		return []*graphql1.Issue{}, nil
	}
	issues, err := r.GH.ListIssues(ctx, owner, name, mapIssueState(state))
	if err != nil {
		return nil, err
	}
	out := make([]*graphql1.Issue, 0, len(issues))
	for _, i := range issues {
		out = append(out, toGraphQLIssue(i))
	}
	return out, nil
}

// Per-PR/Issue review and comment edges are auto-bound to struct fields;
// the gh provider populates them eagerly inside ListPullRequests / ListIssues.
// If lazy fetching is desired in a future workstream, register Reviews
// and Comments as resolvers in gqlgen.yml and reintroduce per-field
// resolver methods here.

// mapPRState collapses a *graphql.PullRequestState argument to the
// concrete provider enum the gh package expects. Nil → OPEN per the
// schema default. The mapping is deliberately verbose so a future
// schema rename can be tracked through a compile error here.
func mapPRState(s *graphql1.PullRequestState) gh.PullRequestState {
	if s == nil {
		return gh.PullRequestStateOpen
	}
	switch *s {
	case graphql1.PullRequestStateOpen:
		return gh.PullRequestStateOpen
	case graphql1.PullRequestStateClosed:
		return gh.PullRequestStateClosed
	case graphql1.PullRequestStateMerged:
		return gh.PullRequestStateMerged
	case graphql1.PullRequestStateAll:
		return gh.PullRequestStateAll
	default:
		return gh.PullRequestStateOpen
	}
}

func mapIssueState(s *graphql1.IssueState) gh.IssueState {
	if s == nil {
		return gh.IssueStateOpen
	}
	switch *s {
	case graphql1.IssueStateOpen:
		return gh.IssueStateOpen
	case graphql1.IssueStateClosed:
		return gh.IssueStateClosed
	case graphql1.IssueStateAll:
		return gh.IssueStateAll
	default:
		return gh.IssueStateOpen
	}
}

// mapPRStateBack converts a provider state back to the GraphQL enum.
func mapPRStateBack(s gh.PullRequestState) graphql1.PullRequestState {
	switch s {
	case gh.PullRequestStateOpen:
		return graphql1.PullRequestStateOpen
	case gh.PullRequestStateClosed:
		return graphql1.PullRequestStateClosed
	case gh.PullRequestStateMerged:
		return graphql1.PullRequestStateMerged
	case gh.PullRequestStateAll:
		return graphql1.PullRequestStateAll
	default:
		return graphql1.PullRequestStateOpen
	}
}

func mapIssueStateBack(s gh.IssueState) graphql1.IssueState {
	switch s {
	case gh.IssueStateOpen:
		return graphql1.IssueStateOpen
	case gh.IssueStateClosed:
		return graphql1.IssueStateClosed
	case gh.IssueStateAll:
		return graphql1.IssueStateAll
	default:
		return graphql1.IssueStateOpen
	}
}

// projectGitHubRepo derives owner / repo for a Project by reading its
// `.git/config` file directly. Returns ok=false when the directory is
// not a git repo, has no origin remote, or the origin is not a GitHub
// URL — the resolver then surfaces an empty list rather than an error.
func projectGitHubRepo(obj *graphql1.Project) (owner, name string, ok bool) {
	if obj == nil {
		return "", "", false
	}
	url, err := gh.ReadOriginURL(obj.Directory)
	if err != nil {
		return "", "", false
	}
	o, n, ok := gh.ParseGitHubURL(url)
	if !ok {
		return "", "", false
	}
	return o, n, true
}

// toGraphQLPullRequest projects a provider PullRequest into the GraphQL
// model. Kept here, not in the gh package, because the GraphQL types
// live in the codegen package and the gh package must stay independent
// of generated code.
func toGraphQLPullRequest(p gh.PullRequest) *graphql1.PullRequest {
	return &graphql1.PullRequest{
		ID:          p.ID(),
		RepoOwner:   p.RepoOwner,
		RepoName:    p.RepoName,
		Number:      int64(p.Number),
		Title:       p.Title,
		Body:        p.Body,
		State:       mapPRStateBack(p.State),
		Draft:       p.Draft,
		AuthorLogin: p.AuthorLogin,
		BaseRef:     p.BaseRef,
		HeadRef:     p.HeadRef,
		URL:         p.URL,
		CreatedAt:   p.CreatedAt,
		UpdatedAt:   p.UpdatedAt,
	}
}

func toGraphQLIssue(i gh.Issue) *graphql1.Issue {
	return &graphql1.Issue{
		ID:          i.ID(),
		RepoOwner:   i.RepoOwner,
		RepoName:    i.RepoName,
		Number:      int64(i.Number),
		Title:       i.Title,
		Body:        i.Body,
		State:       mapIssueStateBack(i.State),
		AuthorLogin: i.AuthorLogin,
		URL:         i.URL,
		CreatedAt:   i.CreatedAt,
		UpdatedAt:   i.UpdatedAt,
	}
}

func toGraphQLWorkflowRun(w gh.WorkflowRun) *graphql1.WorkflowRun {
	return &graphql1.WorkflowRun{
		ID:           w.ID(),
		RepoOwner:    w.RepoOwner,
		RepoName:     w.RepoName,
		RunID:        w.RunID,
		Name:         w.Name,
		WorkflowPath: w.WorkflowPath,
		Status:       w.Status,
		Conclusion:   w.Conclusion,
		HeadBranch:   w.HeadBranch,
		HeadSha:      w.HeadSHA,
		URL:          w.URL,
		CreatedAt:    w.CreatedAt,
		UpdatedAt:    w.UpdatedAt,
	}
}

func toGraphQLReview(r gh.PullRequestReview) *graphql1.PullRequestReview {
	return &graphql1.PullRequestReview{
		ID:          r.NodeID(),
		AuthorLogin: r.AuthorLogin,
		State:       r.State,
		Body:        r.Body,
		SubmittedAt: r.SubmittedAt,
	}
}

func toGraphQLComment(c gh.IssueComment) *graphql1.IssueComment {
	return &graphql1.IssueComment{
		ID:          c.NodeID(),
		AuthorLogin: c.AuthorLogin,
		Body:        c.Body,
		CreatedAt:   c.CreatedAt,
		UpdatedAt:   c.UpdatedAt,
	}
}
