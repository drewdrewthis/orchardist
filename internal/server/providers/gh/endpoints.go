package gh

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"strconv"
)

// The ten endpoints (ADR-011 §12 requires "ten endpoints, not a heavy
// library"). Each method is a thin wrapper over Client.do that
// composes the path, decodes the wire shape, and projects to the
// public typed result.
//
// Endpoints:
//
//   1.  ListPulls           — GET /repos/{o}/{n}/pulls
//   2.  GetPull             — GET /repos/{o}/{n}/pulls/{number}
//   3.  ListIssues          — GET /repos/{o}/{n}/issues
//   4.  GetIssue            — GET /repos/{o}/{n}/issues/{number}
//   5.  ListWorkflowRuns    — GET /repos/{o}/{n}/actions/runs
//   6.  GetWorkflowRun      — GET /repos/{o}/{n}/actions/runs/{run_id}
//   7.  ListPullReviews     — GET /repos/{o}/{n}/pulls/{number}/reviews
//   8.  ListPullComments    — GET /repos/{o}/{n}/issues/{number}/comments  (PR conversation)
//   9.  ListIssueComments   — GET /repos/{o}/{n}/issues/{number}/comments  (real issue)
//   10. GetRepo             — GET /repos/{o}/{n}

// ListPulls fetches the list of pull requests for a repository.
// state must be one of OPEN, CLOSED, MERGED, ALL.
//
// The underlying request is paginated through GitHub's Link header up
// to MaxPages (1000 items at defaultPerPage = 100); repos with more
// PRs than that surface as a truncated slice rather than an
// open-ended fetch.
func (c *Client) ListPulls(ctx context.Context, owner, name string, state PullRequestState) ([]PullRequest, error) {
	q := url.Values{}
	q.Set("per_page", strconv.Itoa(defaultPerPage))
	switch state {
	case PullRequestStateOpen, "":
		q.Set("state", "open")
	case PullRequestStateClosed, PullRequestStateMerged:
		// GitHub doesn't have a separate "merged" filter — merged PRs
		// live under state=closed and are filtered client-side.
		q.Set("state", "closed")
	case PullRequestStateAll:
		q.Set("state", "all")
	default:
		return nil, fmt.Errorf("unknown PullRequestState %q", state)
	}
	q.Set("sort", "updated")
	q.Set("direction", "desc")

	var items []listPullRequestsRawItem
	if err := doListPaginated(ctx, c, fmt.Sprintf("/repos/%s/%s/pulls", owner, name), q, &items); err != nil {
		return nil, err
	}
	prs := listPullRequestsRaw(items).toPullRequests(owner, name)
	if state == PullRequestStateMerged {
		filtered := prs[:0]
		for _, p := range prs {
			if p.State == PullRequestStateMerged {
				filtered = append(filtered, p)
			}
		}
		return filtered, nil
	}
	if state == PullRequestStateClosed {
		filtered := prs[:0]
		for _, p := range prs {
			if p.State == PullRequestStateClosed {
				filtered = append(filtered, p)
			}
		}
		return filtered, nil
	}
	return prs, nil
}

// GetPull fetches one pull request by number.
func (c *Client) GetPull(ctx context.Context, owner, name string, number int) (PullRequest, error) {
	var raw struct {
		Number    int    `json:"number"`
		Title     string `json:"title"`
		Body      string `json:"body"`
		State     string `json:"state"`
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
	if err := c.do(ctx, fmt.Sprintf("/repos/%s/%s/pulls/%d", owner, name, number), nil, &raw); err != nil {
		return PullRequest{}, err
	}
	state := PullRequestStateOpen
	if raw.State == "closed" {
		if raw.MergedAt != "" {
			state = PullRequestStateMerged
		} else {
			state = PullRequestStateClosed
		}
	}
	return PullRequest{
		RepoOwner:   owner,
		RepoName:    name,
		Number:      raw.Number,
		Title:       raw.Title,
		Body:        raw.Body,
		State:       state,
		Draft:       raw.Draft,
		AuthorLogin: raw.User.Login,
		BaseRef:     raw.Base.Ref,
		HeadRef:     raw.Head.Ref,
		URL:         raw.HTMLURL,
		CreatedAt:   raw.CreatedAt,
		UpdatedAt:   raw.UpdatedAt,
	}, nil
}

// ListIssues fetches the list of issues for a repository. PRs are
// filtered out — see types.go. Pagination walks Link rel="next" up
// to MaxPages.
func (c *Client) ListIssues(ctx context.Context, owner, name string, state IssueState) ([]Issue, error) {
	q := url.Values{}
	q.Set("per_page", strconv.Itoa(defaultPerPage))
	switch state {
	case IssueStateOpen, "":
		q.Set("state", "open")
	case IssueStateClosed:
		q.Set("state", "closed")
	case IssueStateAll:
		q.Set("state", "all")
	default:
		return nil, fmt.Errorf("unknown IssueState %q", state)
	}

	var items []listIssuesRawItem
	if err := doListPaginated(ctx, c, fmt.Sprintf("/repos/%s/%s/issues", owner, name), q, &items); err != nil {
		return nil, err
	}
	return listIssuesRaw(items).toIssues(owner, name), nil
}

// GetIssue fetches one issue by number.
func (c *Client) GetIssue(ctx context.Context, owner, name string, number int) (Issue, error) {
	var raw struct {
		Number      int    `json:"number"`
		Title       string `json:"title"`
		Body        string `json:"body"`
		State       string `json:"state"`
		HTMLURL     string `json:"html_url"`
		CreatedAt   string `json:"created_at"`
		UpdatedAt   string `json:"updated_at"`
		PullRequest *struct {
			URL string `json:"url"`
		} `json:"pull_request"`
		User struct {
			Login string `json:"login"`
		} `json:"user"`
		Labels []restLabelRaw `json:"labels"`
	}
	if err := c.do(ctx, fmt.Sprintf("/repos/%s/%s/issues/%d", owner, name, number), nil, &raw); err != nil {
		return Issue{}, err
	}
	if raw.PullRequest != nil {
		return Issue{}, fmt.Errorf("issue %s/%s#%d is a pull request — use GetPull instead", owner, name, number)
	}
	state := IssueStateOpen
	if raw.State == "closed" {
		state = IssueStateClosed
	}
	return Issue{
		RepoOwner:   owner,
		RepoName:    name,
		Number:      raw.Number,
		Title:       raw.Title,
		Body:        raw.Body,
		State:       state,
		AuthorLogin: raw.User.Login,
		URL:         raw.HTMLURL,
		CreatedAt:   raw.CreatedAt,
		UpdatedAt:   raw.UpdatedAt,
		Labels:      restLabelsToLabels(raw.Labels),
	}, nil
}

// ListWorkflowRuns fetches the most recent workflow runs for a repo.
// Pagination walks Link rel="next" up to MaxPages. GitHub's response
// is enveloped (`{ "workflow_runs": [...] }`), so each page is decoded
// per-iteration and the inner slice concatenated.
func (c *Client) ListWorkflowRuns(ctx context.Context, owner, name string) ([]WorkflowRun, error) {
	q := url.Values{}
	q.Set("per_page", strconv.Itoa(defaultPerPage))
	var accum listWorkflowRunsRaw
	decode := func(body io.Reader) error {
		var page listWorkflowRunsRaw
		if err := json.NewDecoder(body).Decode(&page); err != nil {
			return fmt.Errorf("decode /repos/%s/%s/actions/runs body: %w", owner, name, err)
		}
		accum.WorkflowRuns = append(accum.WorkflowRuns, page.WorkflowRuns...)
		return nil
	}
	if err := doEnvelopePaginated(ctx, c, fmt.Sprintf("/repos/%s/%s/actions/runs", owner, name), q, decode); err != nil {
		return nil, err
	}
	return accum.toRuns(owner, name), nil
}

// GetWorkflowRun fetches one workflow run by id.
func (c *Client) GetWorkflowRun(ctx context.Context, owner, name string, runID int64) (WorkflowRun, error) {
	var raw struct {
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
	}
	if err := c.do(ctx, fmt.Sprintf("/repos/%s/%s/actions/runs/%d", owner, name, runID), nil, &raw); err != nil {
		return WorkflowRun{}, err
	}
	return WorkflowRun{
		RepoOwner:    owner,
		RepoName:     name,
		RunID:        raw.ID,
		Name:         raw.Name,
		WorkflowPath: raw.Path,
		Status:       raw.Status,
		Conclusion:   raw.Conclusion,
		HeadBranch:   raw.HeadBranch,
		HeadSHA:      raw.HeadSHA,
		URL:          raw.HTMLURL,
		CreatedAt:    raw.CreatedAt,
		UpdatedAt:    raw.UpdatedAt,
	}, nil
}

// ListPullReviews fetches reviews on one pull request. Pagination
// walks Link rel="next" up to MaxPages.
func (c *Client) ListPullReviews(ctx context.Context, owner, name string, number int) ([]PullRequestReview, error) {
	q := url.Values{}
	q.Set("per_page", strconv.Itoa(defaultPerPage))
	var items []listReviewsRawItem
	if err := doListPaginated(ctx, c, fmt.Sprintf("/repos/%s/%s/pulls/%d/reviews", owner, name, number), q, &items); err != nil {
		return nil, err
	}
	return listReviewsRaw(items).toReviews(), nil
}

// ListPullComments fetches conversation comments on a pull request.
// GitHub stores PR conversation under the issues/{n}/comments path
// (review comments are a separate endpoint we do not surface in v1).
func (c *Client) ListPullComments(ctx context.Context, owner, name string, number int) ([]IssueComment, error) {
	return c.listIssueLikeComments(ctx, owner, name, number)
}

// ListIssueComments fetches comments on a real issue.
func (c *Client) ListIssueComments(ctx context.Context, owner, name string, number int) ([]IssueComment, error) {
	return c.listIssueLikeComments(ctx, owner, name, number)
}

func (c *Client) listIssueLikeComments(ctx context.Context, owner, name string, number int) ([]IssueComment, error) {
	q := url.Values{}
	q.Set("per_page", strconv.Itoa(defaultPerPage))
	var items []listCommentsRawItem
	if err := doListPaginated(ctx, c, fmt.Sprintf("/repos/%s/%s/issues/%d/comments", owner, name, number), q, &items); err != nil {
		return nil, err
	}
	return listCommentsRaw(items).toComments(), nil
}

// GetRepo fetches the repository metadata.
func (c *Client) GetRepo(ctx context.Context, owner, name string) (Repository, error) {
	var raw getRepoRaw
	if err := c.do(ctx, fmt.Sprintf("/repos/%s/%s", owner, name), nil, &raw); err != nil {
		return Repository{}, err
	}
	return raw.toRepository(owner, name), nil
}
