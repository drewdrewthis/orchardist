// Package gh implements the GitHub provider per ADR-011 §5.1, §12.
//
// Hybrid auth: at boot the daemon shells out to `gh auth token` to
// pluck the user's PAT/OAuth token from the gh CLI's keyring. Every
// subsequent call goes direct to api.github.com (or the GHES API base
// configured in gh's CLI) over plain net/http with that bearer token.
//
// The package is split into independently-testable layers:
//
//   - auth.go     — boot-time `gh auth token` shellout + caching
//   - client.go   — net/http client with Bearer header, base-URL
//     override (GH_API_BASE_URL), rate-limit awareness
//   - adapter.go  — Adapter[K, V] over the client for each node type
//   - provider.go — Provider[K, V] with 2-minute read TTL
//   - webhook.go  — POST /webhook/github stub (ADR-011 §12 post-v1)
//
// All types in this file are the data the provider hands back to
// resolvers. They are hand-rolled (not gqlgen-generated) so the
// provider package stays free of generated-code imports — the resolver
// layer projects them into graphql.* types.
//
// **PR vs Issue**: the GitHub REST API treats pull requests as a
// special kind of issue. The Issues endpoint returns both unless we
// filter; we always filter on the consumer side so this package's
// `Issue` type is real issues only. Pull requests live in their own
// dedicated type with PR-specific edges (reviews, base/head ref).
package gh

import "fmt"

// PullRequestState mirrors the GraphQL `PullRequestState` enum.
// Defined here (not imported from the generated graphql package) so
// the gh provider stays independent of generated code — the resolver
// layer maps these to graphql.PullRequestState in one place.
type PullRequestState string

const (
	PullRequestStateOpen   PullRequestState = "OPEN"
	PullRequestStateClosed PullRequestState = "CLOSED"
	PullRequestStateMerged PullRequestState = "MERGED"
	PullRequestStateAll    PullRequestState = "ALL"
)

// IssueState mirrors the GraphQL `IssueState` enum.
type IssueState string

const (
	IssueStateOpen   IssueState = "OPEN"
	IssueStateClosed IssueState = "CLOSED"
	IssueStateAll    IssueState = "ALL"
)

// PullRequestKey identifies a pull request by repo + number. Used as
// the cache key in the per-PR Provider; encoded into the GraphQL ID
// via PullRequest.ID().
type PullRequestKey struct {
	Owner  string
	Name   string
	Number int
}

// String renders the key as `owner/repo#number`.
func (k PullRequestKey) String() string {
	return fmt.Sprintf("%s/%s#%d", k.Owner, k.Name, k.Number)
}

// IssueKey identifies a GitHub issue. Same shape as PullRequestKey but
// kept distinct so the type system prevents accidental cross-pollination.
type IssueKey struct {
	Owner  string
	Name   string
	Number int
}

func (k IssueKey) String() string {
	return fmt.Sprintf("%s/%s#%d", k.Owner, k.Name, k.Number)
}

// WorkflowRunKey identifies a GitHub Actions workflow run.
type WorkflowRunKey struct {
	Owner string
	Name  string
	RunID int64
}

func (k WorkflowRunKey) String() string {
	return fmt.Sprintf("%s/%s#%d", k.Owner, k.Name, k.RunID)
}

// MergeableState mirrors GitHub's MergeableState enum.
// UNKNOWN means GitHub is still computing the mergeability.
type MergeableState string

const (
	MergeableStateMergeable   MergeableState = "MERGEABLE"
	MergeableStateConflicting MergeableState = "CONFLICTING"
	MergeableStateUnknown     MergeableState = "UNKNOWN"
)

// ReviewDecision mirrors GitHub's PullRequestReviewDecision enum.
// Nil when no review activity has occurred yet.
type ReviewDecision string

const (
	ReviewDecisionApproved         ReviewDecision = "APPROVED"
	ReviewDecisionChangesRequested ReviewDecision = "CHANGES_REQUESTED"
	ReviewDecisionReviewRequired   ReviewDecision = "REVIEW_REQUIRED"
	ReviewDecisionCommented        ReviewDecision = "COMMENTED"
	ReviewDecisionDismissed        ReviewDecision = "DISMISSED"
)

// CiStatus is the aggregated CI status across all check runs and commit
// statuses on the PR head SHA.
type CiStatus string

const (
	CiStatusSuccess CiStatus = "SUCCESS"
	CiStatusFailure CiStatus = "FAILURE"
	CiStatusPending CiStatus = "PENDING"
	CiStatusUnknown CiStatus = "UNKNOWN"
)

// PullRequest is the provider-facing pull request. It mirrors the
// fields the GraphQL `PullRequest` type exposes, with one addition:
// the `State` is already projected into the schema's enum (OPEN,
// CLOSED, MERGED) so resolvers don't need to repeat the mapping.
//
// The enrichment fields (Mergeable, MergeStateStatus, ReviewDecision,
// StatusCheckRollup, Labels) are populated lazily by EnrichPullRequest
// and are zero-valued until that call succeeds.
type PullRequest struct {
	RepoOwner   string
	RepoName    string
	Number      int
	Title       string
	Body        string
	State       PullRequestState
	Draft       bool
	AuthorLogin string
	BaseRef     string
	HeadRef     string
	URL         string
	CreatedAt   string
	UpdatedAt   string

	// GraphQL-only enrichment fields. Zero-valued until EnrichPullRequest
	// has been called for this PR key.
	Mergeable         MergeableState
	MergeStateStatus  string          // raw GitHub mergeStateStatus string
	ReviewDecision    *ReviewDecision // nil when GitHub returns null
	StatusCheckRollup CiStatus
	Labels            []string // user labels only; phase labels excluded
}

// ID is the GraphQL-stable id `PullRequest:<owner>/<repo>#<number>`.
func (p PullRequest) ID() string {
	return fmt.Sprintf("PullRequest:%s/%s#%d", p.RepoOwner, p.RepoName, p.Number)
}

// PullRequestReview mirrors GitHub's review payload. NodeID is the
// GraphQL-stable id; the GitHub-issued numeric id is hidden behind it.
type PullRequestReview struct {
	GitHubID    int64
	AuthorLogin string
	State       string
	Body        string
	SubmittedAt string
}

// NodeID is the stable GraphQL id `PullRequestReview:<id>`.
func (r PullRequestReview) NodeID() string {
	return fmt.Sprintf("PullRequestReview:%d", r.GitHubID)
}

// Issue mirrors a GitHub issue (real issues only — PRs are filtered out
// upstream so resolver code can trust this list).
type Issue struct {
	RepoOwner   string
	RepoName    string
	Number      int
	Title       string
	Body        string
	State       IssueState
	AuthorLogin string
	URL         string
	CreatedAt   string
	UpdatedAt   string
}

// ID is the GraphQL-stable id `Issue:<owner>/<repo>#<number>`.
func (i Issue) ID() string {
	return fmt.Sprintf("Issue:%s/%s#%d", i.RepoOwner, i.RepoName, i.Number)
}

// IssueComment mirrors a GitHub issue / PR conversation comment.
type IssueComment struct {
	GitHubID    int64
	AuthorLogin string
	Body        string
	CreatedAt   string
	UpdatedAt   string
}

// NodeID is the stable GraphQL id `IssueComment:<id>`.
func (c IssueComment) NodeID() string {
	return fmt.Sprintf("IssueComment:%d", c.GitHubID)
}

// WorkflowRun mirrors a GitHub Actions workflow run.
type WorkflowRun struct {
	RepoOwner    string
	RepoName     string
	RunID        int64
	Name         string
	WorkflowPath string
	Status       string
	Conclusion   string
	HeadBranch   string
	HeadSHA      string
	URL          string
	CreatedAt    string
	UpdatedAt    string
}

// ID is the GraphQL-stable id `WorkflowRun:<owner>/<repo>#<run_id>`.
func (w WorkflowRun) ID() string {
	return fmt.Sprintf("WorkflowRun:%s/%s#%d", w.RepoOwner, w.RepoName, w.RunID)
}

// Repository is the public shape the provider exposes for the
// `get-repo` endpoint. Surfaces just the bits a daemon consumer cares
// about — full repo metadata is over-fetching.
type Repository struct {
	Owner         string
	Name          string
	Description   string
	Private       bool
	Fork          bool
	Archived      bool
	DefaultBranch string
	URL           string
	UpdatedAt     string
}
