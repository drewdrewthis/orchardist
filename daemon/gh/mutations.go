// mutations.go — GraphQL mutation resolvers for the gh domain.
//
// L5: every mutation execs the matching `scripts/<op>` and projects its
// --json output as the response. Daemon-side code is input validation +
// script-exec wrapping only — no mutation logic reimplemented in Go.
//
// L2: each script emits {ok, data?, error?} with exit 0 on ok:true and
// non-zero on ok:false. Daemon parses stdout only; stderr is for humans.
//
// M4: mutations validate input at the resolver boundary before exec.
// M5: idempotency is documented per mutation.
// M6: origin/capability gating happens at the server level (checkGUIOrigin);
// these resolver methods trust they've already passed that gate.
package gh

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// MutationResolver provides resolver bodies for Mutation.* fields owned
// by the gh domain.
type MutationResolver struct {
	// ScriptsDir is the path prefix for scripts. Defaults to "scripts/"
	// relative to the working directory; injected in tests.
	ScriptsDir string
}

// NewMutationResolver constructs a MutationResolver.
func NewMutationResolver() *MutationResolver {
	return &MutationResolver{ScriptsDir: "scripts"}
}

// scriptResult is the L2 envelope shape returned by every script.
type scriptResult struct {
	Ok    bool             `json:"ok"`
	Data  *json.RawMessage `json:"data,omitempty"`
	Error *scriptError     `json:"error,omitempty"`
}

type scriptError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// execScript runs scripts/<name> --json with the given args and parses
// the L2 envelope from stdout. Returns the parsed result or an error.
// L4: NOT used on the read path — only called from mutation resolvers.
func (m *MutationResolver) execScript(ctx context.Context, name string, args ...string) (scriptResult, error) {
	path := m.ScriptsDir + "/" + name
	allArgs := append([]string{"--json"}, args...)
	cmd := exec.CommandContext(ctx, path, allArgs...)

	stdout, err := cmd.Output()
	if err != nil {
		// Non-zero exit is expected for ok:false scripts; parse what we have.
		if exitErr, ok := err.(*exec.ExitError); ok {
			_ = exitErr
			// stdout may still have the JSON envelope on non-zero exit.
			if len(stdout) > 0 {
				var res scriptResult
				if jsonErr := json.Unmarshal(stdout, &res); jsonErr == nil {
					return res, nil
				}
			}
		}
		return scriptResult{}, fmt.Errorf("exec %s: %w", name, err)
	}

	var res scriptResult
	if err := json.Unmarshal(stdout, &res); err != nil {
		return scriptResult{}, fmt.Errorf("parse %s output: %w", name, err)
	}
	return res, nil
}

// ReviewPullRequestInput is the input for Mutation.reviewPullRequest.
// M3: granular mutation — one field per intent.
type ReviewPullRequestInput struct {
	Repo   string // "owner/name"
	Number int
	Body   string
	Event  string // APPROVE | REQUEST_CHANGES | COMMENT
}

// ReviewPullRequestResult is returned by reviewPullRequest.
// M5: idempotency — NOT idempotent; submitting the same review twice
// creates a duplicate review on GitHub. Clients should not retry.
type ReviewPullRequestResult struct {
	ReviewID string
}

// ReviewPullRequest implements Mutation.reviewPullRequest.
// L5: execs scripts/gh-pr-review.sh --json.
// M4: validates repo format and event value before exec.
func (m *MutationResolver) ReviewPullRequest(ctx context.Context, input ReviewPullRequestInput) (*ReviewPullRequestResult, error) {
	// M4: input validation.
	if _, _, err := SplitRepo(input.Repo); err != nil {
		return nil, fmt.Errorf("reviewPullRequest: %w", err)
	}
	if input.Number <= 0 {
		return nil, fmt.Errorf("reviewPullRequest: number must be positive, got %d", input.Number)
	}
	validEvents := map[string]struct{}{"APPROVE": {}, "REQUEST_CHANGES": {}, "COMMENT": {}}
	if _, ok := validEvents[strings.ToUpper(input.Event)]; !ok {
		return nil, fmt.Errorf("reviewPullRequest: event must be APPROVE, REQUEST_CHANGES, or COMMENT; got %q", input.Event)
	}

	res, err := m.execScript(ctx, "gh-pr-review.sh",
		"--repo", input.Repo,
		"--number", fmt.Sprintf("%d", input.Number),
		"--event", input.Event,
		"--body", input.Body,
	)
	if err != nil {
		return nil, fmt.Errorf("reviewPullRequest exec: %w", err)
	}
	if !res.Ok {
		if res.Error != nil {
			return nil, fmt.Errorf("reviewPullRequest: %s: %s", res.Error.Code, res.Error.Message)
		}
		return nil, fmt.Errorf("reviewPullRequest: script returned ok:false with no error detail")
	}

	// Project the script data into the result type.
	var data struct {
		ReviewID string `json:"review_id"`
	}
	if res.Data != nil {
		if err := json.Unmarshal(*res.Data, &data); err != nil {
			return nil, fmt.Errorf("reviewPullRequest: parse data: %w", err)
		}
	}
	return &ReviewPullRequestResult{ReviewID: data.ReviewID}, nil
}

// LabelPullRequestInput is the input for Mutation.labelPullRequest.
type LabelPullRequestInput struct {
	Repo   string
	Number int
	Labels []string // label names to apply (additive)
}

// LabelPullRequestResult is returned by labelPullRequest.
// M5: idempotent — applying a label that's already present is a no-op.
type LabelPullRequestResult struct {
	AppliedLabels []string
}

// LabelPullRequest implements Mutation.labelPullRequest.
// L5: execs scripts/gh-pr-label.sh --json.
func (m *MutationResolver) LabelPullRequest(ctx context.Context, input LabelPullRequestInput) (*LabelPullRequestResult, error) {
	if _, _, err := SplitRepo(input.Repo); err != nil {
		return nil, fmt.Errorf("labelPullRequest: %w", err)
	}
	if input.Number <= 0 {
		return nil, fmt.Errorf("labelPullRequest: number must be positive")
	}
	if len(input.Labels) == 0 {
		return nil, fmt.Errorf("labelPullRequest: at least one label required")
	}

	res, err := m.execScript(ctx, "gh-pr-label.sh",
		"--repo", input.Repo,
		"--number", fmt.Sprintf("%d", input.Number),
		"--labels", strings.Join(input.Labels, ","),
	)
	if err != nil {
		return nil, fmt.Errorf("labelPullRequest exec: %w", err)
	}
	if !res.Ok {
		if res.Error != nil {
			return nil, fmt.Errorf("labelPullRequest: %s: %s", res.Error.Code, res.Error.Message)
		}
		return nil, fmt.Errorf("labelPullRequest: script returned ok:false")
	}

	var data struct {
		AppliedLabels []string `json:"applied_labels"`
	}
	if res.Data != nil {
		_ = json.Unmarshal(*res.Data, &data)
	}
	return &LabelPullRequestResult{AppliedLabels: data.AppliedLabels}, nil
}

// CommentOnPullRequestInput is the input for Mutation.commentOnPullRequest.
type CommentOnPullRequestInput struct {
	Repo   string
	Number int
	Body   string
}

// CommentOnPullRequestResult is returned by commentOnPullRequest.
// M5: NOT idempotent — posting the same comment twice creates a duplicate.
type CommentOnPullRequestResult struct {
	CommentID string
	CreatedAt time.Time
}

// CommentOnPullRequest implements Mutation.commentOnPullRequest.
// L5: execs scripts/gh-pr-comment.sh --json.
func (m *MutationResolver) CommentOnPullRequest(ctx context.Context, input CommentOnPullRequestInput) (*CommentOnPullRequestResult, error) {
	if _, _, err := SplitRepo(input.Repo); err != nil {
		return nil, fmt.Errorf("commentOnPullRequest: %w", err)
	}
	if input.Number <= 0 {
		return nil, fmt.Errorf("commentOnPullRequest: number must be positive")
	}
	if strings.TrimSpace(input.Body) == "" {
		return nil, fmt.Errorf("commentOnPullRequest: body must not be empty")
	}

	res, err := m.execScript(ctx, "gh-pr-comment.sh",
		"--repo", input.Repo,
		"--number", fmt.Sprintf("%d", input.Number),
		"--body", input.Body,
	)
	if err != nil {
		return nil, fmt.Errorf("commentOnPullRequest exec: %w", err)
	}
	if !res.Ok {
		if res.Error != nil {
			return nil, fmt.Errorf("commentOnPullRequest: %s: %s", res.Error.Code, res.Error.Message)
		}
		return nil, fmt.Errorf("commentOnPullRequest: script returned ok:false")
	}

	var data struct {
		CommentID string `json:"comment_id"`
		CreatedAt string `json:"created_at"`
	}
	if res.Data != nil {
		_ = json.Unmarshal(*res.Data, &data)
	}
	var createdAt time.Time
	if data.CreatedAt != "" {
		createdAt, _ = time.Parse(time.RFC3339, data.CreatedAt)
	}
	return &CommentOnPullRequestResult{
		CommentID: data.CommentID,
		CreatedAt: createdAt,
	}, nil
}

// CreateIssueInput is the input for Mutation.createIssue.
type CreateIssueInput struct {
	Repo   string
	Title  string
	Body   string
	Labels []string
}

// CreateIssueResult is returned by createIssue.
// M5: NOT idempotent — posting the same issue title+body twice creates duplicates.
type CreateIssueResult struct {
	Number int
	URL    string
}

// CreateIssue implements Mutation.createIssue.
// L5: execs scripts/gh-issue-create.sh --json.
func (m *MutationResolver) CreateIssue(ctx context.Context, input CreateIssueInput) (*CreateIssueResult, error) {
	if _, _, err := SplitRepo(input.Repo); err != nil {
		return nil, fmt.Errorf("createIssue: %w", err)
	}
	if strings.TrimSpace(input.Title) == "" {
		return nil, fmt.Errorf("createIssue: title must not be empty")
	}

	args := []string{
		"--repo", input.Repo,
		"--title", input.Title,
		"--body", input.Body,
	}
	if len(input.Labels) > 0 {
		args = append(args, "--labels", strings.Join(input.Labels, ","))
	}

	res, err := m.execScript(ctx, "gh-issue-create.sh", args...)
	if err != nil {
		return nil, fmt.Errorf("createIssue exec: %w", err)
	}
	if !res.Ok {
		if res.Error != nil {
			return nil, fmt.Errorf("createIssue: %s: %s", res.Error.Code, res.Error.Message)
		}
		return nil, fmt.Errorf("createIssue: script returned ok:false")
	}

	var data struct {
		Number int    `json:"number"`
		URL    string `json:"url"`
	}
	if res.Data != nil {
		if err := json.Unmarshal(*res.Data, &data); err != nil {
			return nil, fmt.Errorf("createIssue: parse data: %w", err)
		}
	}
	return &CreateIssueResult{Number: data.Number, URL: data.URL}, nil
}
