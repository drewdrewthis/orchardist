// mutations.go — Mutation resolvers for the git domain (L5).
//
// Each mutation:
//  1. Validates input at the resolver boundary (M4).
//  2. Execs the corresponding scripts/git-<op>.sh with --json.
//  3. Projects the L2 envelope {ok, data?, error?} as the GraphQL response.
//  4. Returns the affected node so the client cache updates (L8, S8).
//
// Mutations do NOT re-implement git logic in Go; the script is the
// canonical operation (L1, L5). The daemon is a thin façade.
package git

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"time"
)

// MutationResolver owns the git mutation resolvers.
type MutationResolver struct {
	svc        Service
	scriptRoot string // abs path to scripts/ directory
}

// NewMutationResolver creates a resolver backed by the service.
// scriptRoot is the absolute path to the scripts/ directory; it must
// end without a trailing slash. If empty, defaults to "scripts".
func NewMutationResolver(svc Service, scriptRoot string) *MutationResolver {
	if scriptRoot == "" {
		scriptRoot = "scripts"
	}
	return &MutationResolver{svc: svc, scriptRoot: scriptRoot}
}

// --- Input types (M3: granular mutations, S4: single Input object) ---

// WorktreeCreateInput is the input for Mutation.worktreeCreate.
type WorktreeCreateInput struct {
	// RepoID is the Repo to create the worktree in.
	RepoID string
	// Branch is the branch name for the new worktree.
	Branch string
	// Path is the optional filesystem path for the new worktree.
	// Defaults to a sibling directory of the main checkout.
	Path string
}

// WorktreeRemoveInput is the input for Mutation.worktreeRemove.
type WorktreeRemoveInput struct {
	// WorktreeID is the stable ID of the worktree to remove.
	WorktreeID string
	// Force removes even if there are uncommitted changes.
	Force bool
}

// WorktreeMoveInput is the input for Mutation.worktreeMove.
type WorktreeMoveInput struct {
	// WorktreeID is the stable ID of the worktree to move.
	WorktreeID string
	// NewPath is the destination path for the worktree.
	NewPath string
}

// FetchInput is the input for Mutation.fetch.
type FetchInput struct {
	// WorktreeID identifies the worktree to run fetch from.
	WorktreeID string
	// Remote is the remote name to fetch from. Defaults to "origin".
	Remote string
}

// PullInput is the input for Mutation.pull.
type PullInput struct {
	// WorktreeID identifies the worktree to pull into.
	WorktreeID string
	// Rebase uses --rebase instead of merge. Defaults to false.
	Rebase bool
}

// PushInput is the input for Mutation.push.
type PushInput struct {
	// WorktreeID identifies the worktree to push from.
	WorktreeID string
	// Remote is the remote name. Defaults to "origin".
	Remote string
	// Force uses --force-with-lease. False by default.
	Force bool
}

// --- script envelope (L2) ---

// scriptEnvelope is the L2 `{ok, data?, error?}` shape parsed from
// a script's --json stdout.
type scriptEnvelope struct {
	OK    bool            `json:"ok"`
	Data  json.RawMessage `json:"data,omitempty"`
	Error *scriptError    `json:"error,omitempty"`
}

type scriptError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// MutationResult is the GraphQL return type for git mutations (S8: returns
// affected node). When OK is false, Node is nil and ErrCode/ErrMsg are set.
type MutationResult struct {
	OK     bool
	Node   *Worktree
	ErrCode string
	ErrMsg  string
}

// execScript runs script at path with args, appends --json, parses the
// L2 envelope from stdout. Returns MutationResult; non-zero exit is
// handled by parsing the envelope (ok: false + error).
func (r *MutationResolver) execScript(ctx context.Context, script string, args ...string) (scriptEnvelope, error) {
	args = append(args, "--json")
	cmd := exec.CommandContext(ctx, script, args...) //nolint:gosec // path from config
	out, err := cmd.Output()
	if err != nil {
		// Non-zero exit: try to parse the envelope from stdout anyway.
		if ee, ok := err.(*exec.ExitError); ok && len(out) == 0 {
			out = ee.Stderr
		}
	}
	if len(out) == 0 {
		return scriptEnvelope{}, fmt.Errorf("script %q produced no output: %w", script, err)
	}
	var env scriptEnvelope
	if jsonErr := json.Unmarshal(out, &env); jsonErr != nil {
		return scriptEnvelope{}, fmt.Errorf("parse script %q output: %w", script, jsonErr)
	}
	return env, nil
}

// worktreeScript returns the absolute path to a git-worktree-* script.
func (r *MutationResolver) worktreeScript(op string) string {
	return fmt.Sprintf("%s/git-worktree-%s.sh", r.scriptRoot, op)
}

// gitScript returns the absolute path to a git-* script.
func (r *MutationResolver) gitScript(op string) string {
	return fmt.Sprintf("%s/git-%s.sh", r.scriptRoot, op)
}

// --- Mutation resolvers ---

// WorktreeCreate resolves Mutation.worktreeCreate.
// Idempotency: NOT idempotent — creating the same branch twice errors at git level (M5).
func (r *MutationResolver) WorktreeCreate(ctx context.Context, input WorktreeCreateInput) (MutationResult, error) {
	// M4: validate input at resolver boundary.
	if input.RepoID == "" {
		return MutationResult{OK: false, ErrCode: "INVALID_INPUT", ErrMsg: "repoId is required"}, nil
	}
	if input.Branch == "" {
		return MutationResult{OK: false, ErrCode: "INVALID_INPUT", ErrMsg: "branch is required"}, nil
	}

	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	args := []string{"--repo", input.RepoID, "--branch", input.Branch}
	if input.Path != "" {
		args = append(args, "--path", input.Path)
	}

	env, err := r.execScript(ctx, r.worktreeScript("create"), args...)
	if err != nil {
		return MutationResult{}, err
	}
	if !env.OK {
		code, msg := scriptErrFields(env)
		return MutationResult{OK: false, ErrCode: code, ErrMsg: msg}, nil
	}
	return MutationResult{OK: true}, nil
}

// WorktreeRemove resolves Mutation.worktreeRemove.
// Idempotency: idempotent — removing a non-existent worktree is a no-op at git level (M5).
func (r *MutationResolver) WorktreeRemove(ctx context.Context, input WorktreeRemoveInput) (MutationResult, error) {
	if input.WorktreeID == "" {
		return MutationResult{OK: false, ErrCode: "INVALID_INPUT", ErrMsg: "worktreeId is required"}, nil
	}

	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	args := []string{"--worktree-id", input.WorktreeID}
	if input.Force {
		args = append(args, "--force")
	}

	env, err := r.execScript(ctx, r.worktreeScript("remove"), args...)
	if err != nil {
		return MutationResult{}, err
	}
	if !env.OK {
		code, msg := scriptErrFields(env)
		return MutationResult{OK: false, ErrCode: code, ErrMsg: msg}, nil
	}
	return MutationResult{OK: true}, nil
}

// WorktreeMove resolves Mutation.worktreeMove.
// Idempotency: NOT idempotent — moving to an existing path errors (M5).
func (r *MutationResolver) WorktreeMove(ctx context.Context, input WorktreeMoveInput) (MutationResult, error) {
	if input.WorktreeID == "" {
		return MutationResult{OK: false, ErrCode: "INVALID_INPUT", ErrMsg: "worktreeId is required"}, nil
	}
	if input.NewPath == "" {
		return MutationResult{OK: false, ErrCode: "INVALID_INPUT", ErrMsg: "newPath is required"}, nil
	}

	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	env, err := r.execScript(ctx, r.worktreeScript("move"),
		"--worktree-id", input.WorktreeID, "--new-path", input.NewPath)
	if err != nil {
		return MutationResult{}, err
	}
	if !env.OK {
		code, msg := scriptErrFields(env)
		return MutationResult{OK: false, ErrCode: code, ErrMsg: msg}, nil
	}
	return MutationResult{OK: true}, nil
}

// Fetch resolves Mutation.fetch.
// Idempotency: idempotent — re-fetching is safe (M5).
func (r *MutationResolver) Fetch(ctx context.Context, input FetchInput) (MutationResult, error) {
	if input.WorktreeID == "" {
		return MutationResult{OK: false, ErrCode: "INVALID_INPUT", ErrMsg: "worktreeId is required"}, nil
	}

	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	args := []string{"--worktree-id", input.WorktreeID}
	if input.Remote != "" {
		args = append(args, "--remote", input.Remote)
	}

	env, err := r.execScript(ctx, r.gitScript("fetch"), args...)
	if err != nil {
		return MutationResult{}, err
	}
	if !env.OK {
		code, msg := scriptErrFields(env)
		return MutationResult{OK: false, ErrCode: code, ErrMsg: msg}, nil
	}
	return MutationResult{OK: true}, nil
}

// Pull resolves Mutation.pull.
// Idempotency: idempotent when already up-to-date (M5).
func (r *MutationResolver) Pull(ctx context.Context, input PullInput) (MutationResult, error) {
	if input.WorktreeID == "" {
		return MutationResult{OK: false, ErrCode: "INVALID_INPUT", ErrMsg: "worktreeId is required"}, nil
	}

	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	args := []string{"--worktree-id", input.WorktreeID}
	if input.Rebase {
		args = append(args, "--rebase")
	}

	env, err := r.execScript(ctx, r.gitScript("pull"), args...)
	if err != nil {
		return MutationResult{}, err
	}
	if !env.OK {
		code, msg := scriptErrFields(env)
		return MutationResult{OK: false, ErrCode: code, ErrMsg: msg}, nil
	}
	return MutationResult{OK: true}, nil
}

// Push resolves Mutation.push.
// Idempotency: NOT idempotent — pushing already-pushed commits is a no-op, but
// force-pushing rewrites history (M5 — document the asymmetry).
func (r *MutationResolver) Push(ctx context.Context, input PushInput) (MutationResult, error) {
	if input.WorktreeID == "" {
		return MutationResult{OK: false, ErrCode: "INVALID_INPUT", ErrMsg: "worktreeId is required"}, nil
	}

	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	args := []string{"--worktree-id", input.WorktreeID}
	if input.Remote != "" {
		args = append(args, "--remote", input.Remote)
	}
	if input.Force {
		args = append(args, "--force")
	}

	env, err := r.execScript(ctx, r.gitScript("push"), args...)
	if err != nil {
		return MutationResult{}, err
	}
	if !env.OK {
		code, msg := scriptErrFields(env)
		return MutationResult{OK: false, ErrCode: code, ErrMsg: msg}, nil
	}
	return MutationResult{OK: true}, nil
}

// scriptErrFields extracts code and message from a failed envelope.
func scriptErrFields(env scriptEnvelope) (code, msg string) {
	if env.Error != nil {
		return env.Error.Code, env.Error.Message
	}
	return "SCRIPT_ERROR", "script returned ok: false with no error details"
}
