package gh

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// DefaultAPIBaseURL is the github.com REST endpoint. GHES users with a
// custom base URL configured in the gh CLI need a separate path; v1
// reads the override from GH_API_BASE_URL (mostly used by tests, but
// also valid for GHES setups).
const DefaultAPIBaseURL = "https://api.github.com"

// EnvAPIBaseURL is the environment variable that overrides
// DefaultAPIBaseURL. Tests use this with httptest.NewTLSServer; GHES
// users could set it in their shell.
const EnvAPIBaseURL = "GH_API_BASE_URL"

// Default per-page limits keep response sizes small and predictable.
// GitHub's max is 100; we pick that for list endpoints because we
// generally want to surface the full slice in one round trip.
const defaultPerPage = 100

// Client is the GitHub HTTPS client. Stateless w.r.t. callers — every
// method takes a context and returns parsed data or an error. The
// bearer token is captured at construction time.
//
// Per ADR-011 §12: "ten endpoints, not a heavy library". This is
// hand-rolled net/http with json.Decoder; no go-github, no graphql-go,
// no oauth2.
type Client struct {
	BaseURL string
	Token   string

	// HTTP is the underlying HTTP client. Defaults to a clone of
	// http.DefaultClient with a 30-second timeout. Tests with
	// httptest.NewTLSServer inject one whose Transport accepts the
	// test server's self-signed cert.
	HTTP *http.Client

	// UserAgent is the User-Agent header sent on every request. GitHub
	// requires a non-empty UA; defaults to "orchard/v1" via
	// NewClient.
	UserAgent string
}

// NewClient constructs a Client with sane defaults. baseURL is
// trimmed of any trailing slash so URL composition stays straight.
func NewClient(baseURL, token string) *Client {
	if baseURL == "" {
		baseURL = DefaultAPIBaseURL
	}
	baseURL = strings.TrimRight(baseURL, "/")
	return &Client{
		BaseURL:   baseURL,
		Token:     token,
		HTTP:      &http.Client{Timeout: 30 * time.Second},
		UserAgent: "orchard/v1",
	}
}

// do performs an authenticated GET against the GitHub API and decodes
// the JSON body into out. It honours rate-limit headers and surfaces
// 401 as ErrNotAuthenticated.
//
// The path argument is a slash-prefixed REST path
// (e.g. `/repos/alice/repo/pulls`). Query arguments are passed via the
// `q` map; nil is fine.
func (c *Client) do(ctx context.Context, path string, q url.Values, out any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if c == nil {
		return errors.New("gh client not initialised")
	}

	full := c.BaseURL + path
	if len(q) > 0 {
		full += "?" + q.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, full, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if ua := c.UserAgent; ua != "" {
		req.Header.Set("User-Agent", ua)
	}

	resp, err := c.httpClient().Do(req)
	if err != nil {
		return fmt.Errorf("github GET %s: %w", path, err)
	}
	defer func() { _ = resp.Body.Close() }()

	// Rate limit awareness — when GitHub says we have 0 calls left for
	// the current window, surface ErrRateLimitedT so the resolver can
	// reflect that as a per-field error rather than a stack trace.
	if remaining := resp.Header.Get("X-RateLimit-Remaining"); remaining == "0" && resp.StatusCode == http.StatusForbidden {
		var resetAt int64
		if rs := resp.Header.Get("X-RateLimit-Reset"); rs != "" {
			if v, perr := strconv.ParseInt(rs, 10, 64); perr == nil {
				resetAt = v
			}
		}
		return &ErrRateLimitedT{ResetAt: resetAt}
	}

	if resp.StatusCode == http.StatusUnauthorized {
		return ErrNotAuthenticated
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return &httpError{
			Status:   resp.StatusCode,
			Message:  strings.TrimSpace(string(body)),
			Endpoint: path,
		}
	}

	if out == nil {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode %s body: %w", path, err)
	}
	return nil
}

func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return http.DefaultClient
}

// SplitRepo splits an "owner/name" string into its components. Returns
// an error on malformed input — extra slashes, empty halves, etc.
func SplitRepo(repo string) (owner, name string, err error) {
	parts := strings.SplitN(repo, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" || strings.Contains(parts[1], "/") {
		return "", "", fmt.Errorf("malformed repo %q: want owner/name", repo)
	}
	return parts[0], parts[1], nil
}

// listPullRequestsRaw is the wire-shape decoder for `/repos/{o}/{n}/pulls`.
// Kept private — callers go through the typed adapter methods.
type listPullRequestsRaw []struct {
	Number    int    `json:"number"`
	Title     string `json:"title"`
	Body      string `json:"body"`
	State     string `json:"state"` // "open" | "closed"
	Draft     bool   `json:"draft"`
	HTMLURL   string `json:"html_url"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
	MergedAt  string `json:"merged_at"`
	User      struct {
		Login string `json:"login"`
	} `json:"user"`
	Base struct {
		Ref string `json:"ref"`
	} `json:"base"`
	Head struct {
		Ref string `json:"ref"`
	} `json:"head"`
}

func (raws listPullRequestsRaw) toPullRequests(owner, name string) []PullRequest {
	out := make([]PullRequest, 0, len(raws))
	for _, r := range raws {
		state := PullRequestStateOpen
		if r.State == "closed" {
			if r.MergedAt != "" {
				state = PullRequestStateMerged
			} else {
				state = PullRequestStateClosed
			}
		}
		out = append(out, PullRequest{
			RepoOwner:   owner,
			RepoName:    name,
			Number:      r.Number,
			Title:       r.Title,
			Body:        r.Body,
			State:       state,
			Draft:       r.Draft,
			AuthorLogin: r.User.Login,
			BaseRef:     r.Base.Ref,
			HeadRef:     r.Head.Ref,
			URL:         r.HTMLURL,
			CreatedAt:   r.CreatedAt,
			UpdatedAt:   r.UpdatedAt,
		})
	}
	return out
}

// listIssuesRaw is the wire-shape decoder for `/repos/{o}/{n}/issues`.
type listIssuesRaw []struct {
	Number      int    `json:"number"`
	Title       string `json:"title"`
	Body        string `json:"body"`
	State       string `json:"state"` // "open" | "closed"
	HTMLURL     string `json:"html_url"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
	PullRequest *struct {
		URL string `json:"url"`
	} `json:"pull_request"` // present iff this "issue" is actually a PR
	User struct {
		Login string `json:"login"`
	} `json:"user"`
}

func (raws listIssuesRaw) toIssues(owner, name string) []Issue {
	out := make([]Issue, 0, len(raws))
	for _, r := range raws {
		// Filter out PRs — GitHub returns them in the issues endpoint,
		// but our schema separates them.
		if r.PullRequest != nil {
			continue
		}
		state := IssueStateOpen
		if r.State == "closed" {
			state = IssueStateClosed
		}
		out = append(out, Issue{
			RepoOwner:   owner,
			RepoName:    name,
			Number:      r.Number,
			Title:       r.Title,
			Body:        r.Body,
			State:       state,
			AuthorLogin: r.User.Login,
			URL:         r.HTMLURL,
			CreatedAt:   r.CreatedAt,
			UpdatedAt:   r.UpdatedAt,
		})
	}
	return out
}

// listWorkflowRunsRaw is the wire-shape for `/repos/{o}/{n}/actions/runs`.
type listWorkflowRunsRaw struct {
	WorkflowRuns []struct {
		ID         int64  `json:"id"`
		Name       string `json:"name"`
		Path       string `json:"path"`
		Status     string `json:"status"`
		Conclusion string `json:"conclusion"`
		HeadBranch string `json:"head_branch"`
		HeadSHA    string `json:"head_sha"`
		HTMLURL    string `json:"html_url"`
		CreatedAt  string `json:"created_at"`
		UpdatedAt  string `json:"updated_at"`
	} `json:"workflow_runs"`
}

func (raws listWorkflowRunsRaw) toRuns(owner, name string) []WorkflowRun {
	out := make([]WorkflowRun, 0, len(raws.WorkflowRuns))
	for _, r := range raws.WorkflowRuns {
		out = append(out, WorkflowRun{
			RepoOwner:    owner,
			RepoName:     name,
			RunID:        r.ID,
			Name:         r.Name,
			WorkflowPath: r.Path,
			Status:       r.Status,
			Conclusion:   r.Conclusion,
			HeadBranch:   r.HeadBranch,
			HeadSHA:      r.HeadSHA,
			URL:          r.HTMLURL,
			CreatedAt:    r.CreatedAt,
			UpdatedAt:    r.UpdatedAt,
		})
	}
	return out
}

// listReviewsRaw is the wire-shape for `/repos/{o}/{n}/pulls/{n}/reviews`.
type listReviewsRaw []struct {
	ID          int64  `json:"id"`
	State       string `json:"state"`
	Body        string `json:"body"`
	SubmittedAt string `json:"submitted_at"`
	User        struct {
		Login string `json:"login"`
	} `json:"user"`
}

func (raws listReviewsRaw) toReviews() []PullRequestReview {
	out := make([]PullRequestReview, 0, len(raws))
	for _, r := range raws {
		out = append(out, PullRequestReview{
			GitHubID:    r.ID,
			AuthorLogin: r.User.Login,
			State:       r.State,
			Body:        r.Body,
			SubmittedAt: r.SubmittedAt,
		})
	}
	return out
}

// listCommentsRaw is the wire-shape for issue comments.
type listCommentsRaw []struct {
	ID        int64  `json:"id"`
	Body      string `json:"body"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
	User      struct {
		Login string `json:"login"`
	} `json:"user"`
}

func (raws listCommentsRaw) toComments() []IssueComment {
	out := make([]IssueComment, 0, len(raws))
	for _, c := range raws {
		out = append(out, IssueComment{
			GitHubID:    c.ID,
			AuthorLogin: c.User.Login,
			Body:        c.Body,
			CreatedAt:   c.CreatedAt,
			UpdatedAt:   c.UpdatedAt,
		})
	}
	return out
}

// getRepoRaw is the wire-shape for `/repos/{o}/{n}`.
type getRepoRaw struct {
	Description   string `json:"description"`
	Private       bool   `json:"private"`
	Fork          bool   `json:"fork"`
	Archived      bool   `json:"archived"`
	DefaultBranch string `json:"default_branch"`
	HTMLURL       string `json:"html_url"`
	UpdatedAt     string `json:"updated_at"`
}

func (r getRepoRaw) toRepository(owner, name string) Repository {
	return Repository{
		Owner:         owner,
		Name:          name,
		Description:   r.Description,
		Private:       r.Private,
		Fork:          r.Fork,
		Archived:      r.Archived,
		DefaultBranch: r.DefaultBranch,
		URL:           r.HTMLURL,
		UpdatedAt:     r.UpdatedAt,
	}
}
