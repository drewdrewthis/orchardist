package gh

import (
	"errors"
	"fmt"
)

// ErrGHNotInstalled is returned when the `gh` CLI cannot be found on PATH.
// Per ADR-011 §6: the daemon surfaces this as a per-field GraphQL error
// and keeps running — non-gh fields stay live.
var ErrGHNotInstalled = errors.New("gh CLI is not installed; install with `brew install gh` or see https://cli.github.com/")

// ErrNotAuthenticated is returned when `gh auth token` exits non-zero,
// the token output is empty, or the GitHub API returns 401.
// Per ADR-011 §12: surfaces as a per-field error so the rest of the
// schema continues to resolve.
var ErrNotAuthenticated = errors.New("not authenticated against GitHub; run `gh auth login`")

// ErrRateLimitedT is returned when the GitHub API responds with
// X-RateLimit-Remaining: 0. ResetAt is the unix epoch when the window resets.
// S9: expected error surfaced as typed result, not in GraphQL errors[].
type ErrRateLimitedT struct {
	ResetAt int64 // unix seconds; 0 if unknown
}

func (e *ErrRateLimitedT) Error() string {
	if e.ResetAt > 0 {
		return fmt.Sprintf("github API rate limited; resets at %d", e.ResetAt)
	}
	return "github API rate limited"
}

// IsRateLimited reports whether err is an ErrRateLimitedT.
func IsRateLimited(err error) bool {
	var t *ErrRateLimitedT
	return errors.As(err, &t)
}

// httpError is the typed wrapper for non-2xx GitHub API responses.
// Status is captured so callers can discriminate (e.g. 404 → null response).
type httpError struct {
	Status   int
	Message  string
	Endpoint string
}

func (e *httpError) Error() string {
	return fmt.Sprintf("github %s returned %d: %s", e.Endpoint, e.Status, e.Message)
}

// IsNotFound reports whether err is a GitHub API 404. Used to return
// null for Query.issue / Query.pullRequest when the number doesn't exist.
func IsNotFound(err error) bool {
	var h *httpError
	if errors.As(err, &h) {
		return h.Status == 404
	}
	return false
}
