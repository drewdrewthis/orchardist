// passthrough.go — Query.git(worktreeId, args): JSON pass-through (S16b).
//
// The pass-through allows clients to run arbitrary git commands in a known
// worktree when the typed core does not yet cover a call shape. It is an
// escape hatch, not a bypass.
//
// L4 guards (S16b, T7):
//  1. Top-level query only — enforced at schema composition (gqlgen rejects
//     nested list-resolver passthrough at code-generation time; see T7 test).
//  2. Per-call timeout: 30 seconds.
//  3. Domain concurrency cap: 4 concurrent pass-through calls.
//  4. NOT cached, NOT DataLoader-batched, NOT subscribable.
//
// Clients wanting live updates MUST use the typed core (worktreeChanged subscription).
package git

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
	"time"
)

const (
	// passthroughTimeout is the per-call wall-clock limit (S16b guard 2).
	passthroughTimeout = 30 * time.Second

	// passthroughConcurrencyLimit is the maximum concurrent pass-through
	// calls allowed (S16b guard 3).
	passthroughConcurrencyLimit = 4
)

// PassthroughResolver owns the Query.git resolver with L4 guards.
type PassthroughResolver struct {
	svc    Service
	logger *slog.Logger

	// sem gates concurrent pass-through calls (S16b guard 3).
	sem chan struct{}
}

// NewPassthroughResolver creates a resolver with the concurrency semaphore
// initialised to passthroughConcurrencyLimit.
func NewPassthroughResolver(svc Service, logger *slog.Logger) *PassthroughResolver {
	if logger == nil {
		logger = slog.Default()
	}
	sem := make(chan struct{}, passthroughConcurrencyLimit)
	for i := 0; i < passthroughConcurrencyLimit; i++ {
		sem <- struct{}{}
	}
	return &PassthroughResolver{svc: svc, logger: logger, sem: sem}
}

// PassthroughResult is the JSON-serialisable output of Query.git.
// stdout and stderr are included so clients can observe all output.
type PassthroughResult struct {
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	ExitCode int    `json:"exitCode"`
}

// Git resolves Query.git(worktreeId: ID!, args: [String!]!): JSON.
//
// Guards:
//  (1) Top-level only — schema composition enforces this statically.
//  (2) 30-second timeout — applied via ctx.
//  (3) Concurrency cap 4 — semaphore blocks when cap is reached.
//  (4) Not cached — every call exec()s fresh.
func (r *PassthroughResolver) Git(ctx context.Context, worktreeID string, args []string) (json.RawMessage, error) {
	// Guard (2): check context before any work.
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// Guard: validate inputs (M4-style — the pass-through is a query, not
	// a mutation, but input validation applies equally).
	if worktreeID == "" {
		return nil, fmt.Errorf("git passthrough: worktreeId is required")
	}
	if len(args) == 0 {
		return nil, fmt.Errorf("git passthrough: args must not be empty")
	}

	// Guard: resolve the worktree path so we know where to run the command.
	wt, err := r.svc.GetWorktree(ctx, WorktreeID(worktreeID))
	if err != nil {
		return nil, fmt.Errorf("git passthrough: worktree %q not found: %w", worktreeID, err)
	}

	// Guard 3: acquire concurrency slot.
	select {
	case <-r.sem:
		defer func() { r.sem <- struct{}{} }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	// Guard 2: apply 30-second timeout.
	callCtx, cancel := context.WithTimeout(ctx, passthroughTimeout)
	defer cancel()

	// Build the git command: `git -C <path> <args...>`.
	cmdArgs := append([]string{"-C", wt.Path}, args...)
	cmd := exec.CommandContext(callCtx, "git", cmdArgs...) //nolint:gosec // args from authenticated client
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	exitCode := 0
	if runErr := cmd.Run(); runErr != nil {
		if ee, ok := runErr.(*exec.ExitError); ok {
			exitCode = ee.ExitCode()
		} else {
			exitCode = 1
		}
	}

	result := PassthroughResult{
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		ExitCode: exitCode,
	}

	r.logger.Debug("git passthrough",
		"worktreeId", worktreeID, "args", args,
		"exitCode", exitCode)

	raw, jsonErr := json.Marshal(result)
	if jsonErr != nil {
		return nil, fmt.Errorf("git passthrough: marshal result: %w", jsonErr)
	}
	return raw, nil
}
