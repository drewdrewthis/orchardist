package gh

import "fmt"

// PullRequestState mirrors the GraphQL PullRequestState enum.
// Defined here (not in the generated graphql package) so the provider
// stays independent of generated code — the resolver layer projects.
type PullRequestState string

const (
	PullRequestStateOpen   PullRequestState = "OPEN"
	PullRequestStateClosed PullRequestState = "CLOSED"
	PullRequestStateMerged PullRequestState = "MERGED"
	PullRequestStateAll    PullRequestState = "ALL"
)

// IssueState mirrors the GraphQL IssueState enum.
type IssueState string

const (
	IssueStateOpen   IssueState = "OPEN"
	IssueStateClosed IssueState = "CLOSED"
	IssueStateAll    IssueState = "ALL"
)

// PullRequestKey identifies a pull request by repo + number.
// Cache key in the provider; encoded into the GraphQL ID via PullRequest.ID().
type PullRequestKey struct {
	Owner  string
	Name   string
	Number int
}

func (k PullRequestKey) String() string {
	return fmt.Sprintf("%s/%s#%d", k.Owner, k.Name, k.Number)
}

// IssueKey identifies a GitHub issue. Same shape as PullRequestKey but
// kept distinct to prevent accidental cross-pollination.
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
// UNKNOWN means GitHub is still computing mergeability.
type MergeableState string

const (
	MergeableStateMergeable   MergeableState = "MERGEABLE"
	MergeableStateConflicting MergeableState = "CONFLICTING"
	MergeableStateUnknown     MergeableState = "UNKNOWN"
)

// ReviewDecision mirrors GitHub's PullRequestReviewDecision enum.
// Nil pointer when no review activity has occurred yet.
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

// PullRequest is the provider-facing pull request type.
//
// The enrichment fields (Mergeable, MergeStateStatus, ReviewDecision,
// StatusCheckRollup, Labels) are populated lazily by EnrichPullRequest /
// BatchEnrichPullRequests and are zero-valued until that call succeeds.
//
// O12: enrichment is served stale-while-revalidating; UNKNOWN mergeable
// is never cached so transient "computing" state doesn't harden (#367).
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

	// Enrichment fields — zero-valued until EnrichPullRequest succeeds.
	Mergeable         MergeableState
	MergeStateStatus  string
	ReviewDecision    *ReviewDecision // nil when GitHub returns null
	StatusCheckRollup CiStatus
	Labels            []Label // user labels only; phase labels excluded
}

// ID is the GraphQL-stable node id `PullRequest:<owner>/<repo>#<number>`.
func (p PullRequest) ID() string {
	return fmt.Sprintf("PullRequest:%s/%s#%d", p.RepoOwner, p.RepoName, p.Number)
}

// Label mirrors a GitHub label attached to an issue or pull request.
// Color and Description fall back to empty strings (not nil).
type Label struct {
	Name        string
	Color       string
	Description string
}

// PullRequestReview mirrors GitHub's review payload.
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
//
// Labels are populated by the REST endpoints (list + single) so callers
// never need a separate enrichment step. Phase labels are stripped.
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
	Labels      []Label
}

// ID is the GraphQL-stable id `Issue:<owner>/<repo>#<number>`.
func (i Issue) ID() string {
	return fmt.Sprintf("Issue:%s/%s#%d", i.RepoOwner, i.RepoName, i.Number)
}

// IssueRef carries cross-issue identity plus title — enough for typical
// UI projections without a follow-up GetIssue per node.
type IssueRef struct {
	Owner  string
	Name   string
	Number int
	Title  string
}

// ID renders the same GraphQL id Issue.ID() does.
func (r IssueRef) ID() string {
	return fmt.Sprintf("Issue:%s/%s#%d", r.Owner, r.Name, r.Number)
}

// IssueDependencies aggregates the four dependency edges GitHub exposes
// on an issue. Lazily populated by EnrichIssueDependencies; resolvers
// project each edge into blockedByIssues, blockingIssues, subIssues,
// parentIssue (#563).
type IssueDependencies struct {
	BlockedBy []IssueRef
	Blocking  []IssueRef
	SubIssues []IssueRef
	Parent    *IssueRef
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

// Repository is the public shape the provider exposes for the get-repo
// endpoint. Surfaces just the bits a daemon consumer cares about.
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
