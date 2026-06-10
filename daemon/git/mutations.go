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
	"strings"
	"sync"
	"time"
)

// PRStateLookup is the narrow contract MutationResolver needs to resolve
// a branch's PR merged-state before the branch-delete step.
//
// Per R4 (ISP), consumers define this narrow interface in their own module
// and depend on it, not on the gh domain's full Service.
//
// Implementations: the resolver layer wraps *gh.Provider with a thin
// adapter that satisfies this interface (see resolver.go WithGitMutations).
// In tests, a stub struct with a statically-configured response suffices.
//
// Error contract: on any lookup failure (network, auth, no PR) return
// ("", err). The caller maps the empty state through PRMergedArgForState,
// which yields "unknown" (fail-closed per AC-G2).
type PRStateLookup interface {
	// PRStateByBranch returns the gh PR state string ("MERGED", "CLOSED",
	// "OPEN") for the most-relevant PR whose head branch matches `branch`
	// in the repo identified by `repoSlug` ("owner/repo" format).
	// Returns ("", nil) when no PR exists.
	// Returns ("", err) on any lookup error — the caller must treat this as
	// "unknown" and skip branch deletion (fail-closed, AC-G2).
	PRStateByBranch(ctx context.Context, repoSlug, branch string) (string, error)
}

// MutationResolver owns the git mutation resolvers.
//
// cleanupMu serializes worktreesCleanup calls so that two concurrent cleanup
// operations over an overlapping stale set do not race: the second call waits
// for the first to finish, then finds the worktrees already gone (idempotent
// no-op path). This is the AC-G5 concurrency model: serialize-and-tolerate.
type MutationResolver struct {
	scriptRoot string        // abs path to scripts/ directory
	cleanupMu  sync.Mutex    // serializes WorktreesCleanup (AC-G5)
	prLookup   PRStateLookup // optional; nil → PR merged-state always "unknown" (fail-closed)
}

// NewMutationResolver creates a resolver.
// scriptRoot is the absolute path to the scripts/ directory; it must
// end without a trailing slash. If empty, defaults to "scripts".
func NewMutationResolver(scriptRoot string) *MutationResolver {
	if scriptRoot == "" {
		scriptRoot = "scripts"
	}
	return &MutationResolver{scriptRoot: scriptRoot}
}

// WithPRStateLookup injects the gh merged-state lookup dependency (AC-G2).
// Must be called before any WorktreesCleanup call.
// When not called (nil lookup), every branch-delete step is skipped
// with reason "merged-state-unavailable" (fail-closed).
func (r *MutationResolver) WithPRStateLookup(p PRStateLookup) *MutationResolver {
	r.prLookup = p
	return r
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
	// ActiveSession is the tmux session name the user is currently active in (AC-G1).
	// When non-empty, the worktree whose session matches is excluded from ALL destruction
	// and reported as skipped with reason "hosts-active-session".
	// The daemon MUST NOT infer this from its own $TMUX env — always caller-supplied.
	ActiveSession string
	// ActiveCwd is the absolute path of the user's active working directory (AC-G1).
	// When non-empty, the worktree whose path matches is excluded from ALL destruction
	// and reported as skipped with reason "hosts-active-session".
	ActiveCwd string
}

// WorktreesCleanupInput is the input for Mutation.worktreesCleanup (AC8/AC9/AC-G5/AC10).
type WorktreesCleanupInput struct {
	// WorktreeIDs is the set of stable worktree IDs to clean up.
	// Must be non-empty; malformed IDs are rejected at the resolver boundary (M4, AC10).
	WorktreeIDs []string
	// Force removes even if there are uncommitted changes.
	Force bool
	// ActiveSession is the tmux session name the user is currently active in (AC-G1).
	ActiveSession string
	// ActiveCwd is the absolute path of the user's active working directory (AC-G1).
	ActiveCwd string
	// BaseBranch is the base branch for branch-delete safety checks. Defaults to "main".
	BaseBranch string
	// Protected is a comma-separated list of branch names never to delete.
	Protected string
	// SessionNames is a parallel array aligned by index with WorktreeIDs.
	// Each entry is the tmux session name to kill for that worktree (AC-G3).
	// An empty or nil entry means the worktree has no associated session;
	// the script skips the tmux-kill stage when --tmux-session is not supplied.
	SessionNames []string
}

// WorktreeCleanupEntry is a per-worktree result inside WorktreesCleanupResult (AC8).
type WorktreeCleanupEntry struct {
	// WorktreeID is the stable ID of the worktree this entry describes.
	WorktreeID string
	// OK is true when cleanup succeeded or was a clean no-op.
	OK bool
	// Stage is the failing stage when OK is false (e.g. "worktree-remove").
	Stage string
	// Message is a human-readable message for failures or skips.
	Message string
	// AlreadyRemoved is true when the worktree was already gone before this call
	// (idempotent re-run or race loser — AC9, AC-G5).
	AlreadyRemoved bool
	// Warnings carries non-fatal per-stage warnings (branch-skip, tmux-kill).
	Warnings []string
}

// WorktreesCleanupResult is returned by WorktreesCleanup.
type WorktreesCleanupResult struct {
	// OK is true when the batch call itself succeeded (valid input, no systemic error).
	// Individual entry OK fields carry per-worktree status.
	OK      bool
	Entries []WorktreeCleanupEntry
	ErrCode string
	ErrMsg  string
}

// --- gh-state → --pr-merged argument seam (AC-G2 / item G) ---

// PRMergedArgForState maps a gh provider PullRequest state string to the
// --pr-merged argument value consumed by scripts/git/branch-delete.sh.
//
// States:
//
//	"MERGED"  → "merged"
//	"CLOSED"  → "not-merged"  (closed-without-merge → branch NOT merged via gh)
//	"OPEN"    → "not-merged"
//	err/""    → "unknown"     (fail-closed per AC-G2)
//
// This is the tested seam for Step 7 / integration: the gh service calls the
// daemon's gh provider to get PullRequest.State, then passes it here to derive
// the correct --pr-merged flag. When the gh call errors, pass "" to get "unknown"
// (fail-closed: branch deletion is skipped rather than risking data loss).
func PRMergedArgForState(ghPRState string) string {
	switch ghPRState {
	case "MERGED":
		return "merged"
	case "CLOSED", "OPEN":
		return "not-merged"
	default:
		// Empty string, "unknown", or any gh error → fail-closed (AC-G2).
		return "unknown"
	}
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
	OK      bool
	Node    *Worktree
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

// worktreeScript returns the absolute path to a git/worktree-* script.
// Scripts live under <scriptRoot>/git/worktree-<op>.sh, NOT as flat siblings
// <scriptRoot>/git-worktree-<op>.sh (issue #693).
func (r *MutationResolver) worktreeScript(op string) string {
	return fmt.Sprintf("%s/git/worktree-%s.sh", r.scriptRoot, op)
}

// gitScript returns the absolute path to a git/<op> script.
// Scripts live under <scriptRoot>/git/<op>.sh, NOT as flat siblings
// <scriptRoot>/git-<op>.sh (issue #693).
func (r *MutationResolver) gitScript(op string) string {
	return fmt.Sprintf("%s/git/%s.sh", r.scriptRoot, op)
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
//
// AC-G1: ActiveSession and ActiveCwd are passed through to the script as
// --active-session / --active-cwd so the script can exclude the live
// worktree from ALL destruction stages. The daemon NEVER reads $TMUX or
// calls current_session_name() — identity comes only from the caller.
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
	// AC-G1: thread active-session identity through to the script.
	// The script gates ALL destruction stages on this; no daemon-side $TMUX read.
	if input.ActiveSession != "" {
		args = append(args, "--active-session", input.ActiveSession)
	}
	if input.ActiveCwd != "" {
		args = append(args, "--active-cwd", input.ActiveCwd)
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

// WorktreesCleanup resolves Mutation.worktreesCleanup.
//
// Idempotency: idempotent — already-removed worktrees produce ok:true with
// alreadyRemoved:true entries; re-running on a clean fleet is a no-op (AC9, M5).
//
// Partial failure: each worktree is attempted even if a sibling fails (AC8).
//
// Concurrency: serialized via cleanupMu. Two concurrent calls over an overlapping
// set do not hard-race: the second waits, finds worktrees gone, and returns
// alreadyRemoved:true entries for doubly-targeted worktrees (AC-G5).
//
// Input validation (M4, AC10): empty list or any malformed ID is rejected at
// the resolver boundary; script is never exec'd for invalid input.
func (r *MutationResolver) WorktreesCleanup(ctx context.Context, input WorktreesCleanupInput) (WorktreesCleanupResult, error) {
	// M4 / AC10: validate at resolver boundary.
	if len(input.WorktreeIDs) == 0 {
		return WorktreesCleanupResult{
			OK:      false,
			ErrCode: "INVALID_INPUT",
			ErrMsg:  "worktreeIds must be a non-empty list",
		}, nil
	}
	for _, id := range input.WorktreeIDs {
		if id == "" {
			return WorktreesCleanupResult{
				OK:      false,
				ErrCode: "INVALID_INPUT",
				ErrMsg:  "worktreeIds contains an empty entry",
			}, nil
		}
		// Require <project>:<name> format.
		colonIdx := -1
		for i, c := range id {
			if c == ':' {
				colonIdx = i
				break
			}
		}
		if colonIdx <= 0 || colonIdx == len(id)-1 {
			return WorktreesCleanupResult{
				OK:      false,
				ErrCode: "INVALID_INPUT",
				ErrMsg:  fmt.Sprintf("malformed worktreeId %q: must be <project>:<name>", id),
			}, nil
		}
	}

	// AC-G5: serialize cleanup ops so races don't hard-error.
	r.cleanupMu.Lock()
	defer r.cleanupMu.Unlock()

	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	scriptPath := r.worktreeScript("remove")
	entries := make([]WorktreeCleanupEntry, 0, len(input.WorktreeIDs))

	for i, id := range input.WorktreeIDs {
		var sessionName string
		if i < len(input.SessionNames) {
			sessionName = input.SessionNames[i]
		}
		entry := r.cleanupOne(ctx, scriptPath, id, sessionName, input)
		entries = append(entries, entry)
	}

	return WorktreesCleanupResult{OK: true, Entries: entries}, nil
}

// cleanupOne runs worktree-remove.sh for a single worktree and returns a
// WorktreeCleanupEntry. It is always called while cleanupMu is held (AC-G5).
//
// AC-G2: the gh merged-state is looked up from r.prLookup (daemon-owned,
// not client-supplied). Any lookup error maps to "unknown" (fail-closed:
// branch-delete is skipped, but worktree+dir removal proceeds normally).
//
// AC-G3: sessionName is the tmux session name associated with this worktree.
// When non-empty, --tmux-session <name> is passed to the script so the
// tmux-kill stage actually fires. The kill is non-fatal: a failure records
// a warning and removal continues.
func (r *MutationResolver) cleanupOne(ctx context.Context, scriptPath, id, sessionName string, input WorktreesCleanupInput) WorktreeCleanupEntry {
	args := []string{"--worktree-id", id}
	if input.Force {
		args = append(args, "--force")
	}
	if input.ActiveSession != "" {
		args = append(args, "--active-session", input.ActiveSession)
	}
	if input.ActiveCwd != "" {
		args = append(args, "--active-cwd", input.ActiveCwd)
	}
	// AC-G3: pass the per-worktree tmux session name when supplied.
	// The script skips the tmux-kill stage when --tmux-session is absent.
	if sessionName != "" {
		args = append(args, "--tmux-session", sessionName)
	}

	// AC-G2: resolve PR merged-state from the daemon's own gh service.
	// The client never supplies this — always daemon-owned (AC-G2 decision).
	// Lookup failure → "" → PRMergedArgForState("") → "unknown" (fail-closed).
	prMerged := r.resolvePRMerged(ctx, id)
	if prMerged != "" {
		args = append(args, "--pr-merged", prMerged)
	}

	if input.BaseBranch != "" {
		args = append(args, "--base", input.BaseBranch)
	}
	if input.Protected != "" {
		args = append(args, "--protected", input.Protected)
	}

	env, err := r.execScript(ctx, scriptPath, args...)
	if err != nil {
		// Script produced no output or could not be exec'd — hard failure.
		return WorktreeCleanupEntry{
			WorktreeID: id,
			OK:         false,
			Stage:      "worktree-remove",
			Message:    err.Error(),
		}
	}

	if !env.OK {
		code, msg := scriptErrFields(env)
		return WorktreeCleanupEntry{
			WorktreeID: id,
			OK:         false,
			Stage:      "worktree-remove",
			Message:    fmt.Sprintf("%s: %s", code, msg),
		}
	}

	// ok:true — parse the data for alreadyRemoved and warnings.
	entry := WorktreeCleanupEntry{
		WorktreeID: id,
		OK:         true,
		Warnings:   []string{},
	}
	if len(env.Data) > 0 {
		entry = enrichEntryFromData(entry, env.Data)
	}
	return entry
}

// resolvePRMerged looks up the PR merged-state for the worktree identified by
// id via r.prLookup and maps it to the --pr-merged script argument.
//
// AC-G2 contract:
//   - r.prLookup nil            → "" → PRMergedArgForState("") → "unknown"
//   - lookup error              → "" → "unknown"
//   - no PR found               → "" → "unknown"
//   - PR found with state S     → PRMergedArgForState(S)
//
// The caller passes the result directly to --pr-merged. An "unknown" result
// causes the script to skip branch-delete (fail-closed).
func (r *MutationResolver) resolvePRMerged(ctx context.Context, id string) string {
	if r.prLookup == nil {
		return PRMergedArgForState("")
	}

	// Parse <repoSlug>:<branch> from the worktree ID.
	// The resolver boundary already validated the format (has a colon with
	// non-empty parts on both sides), so a missing colon here is defensive.
	colonIdx := strings.Index(id, ":")
	if colonIdx <= 0 || colonIdx == len(id)-1 {
		return PRMergedArgForState("") // malformed — fail-closed
	}
	repoSlug := id[:colonIdx]
	branch := id[colonIdx+1:]

	state, err := r.prLookup.PRStateByBranch(ctx, repoSlug, branch)
	if err != nil {
		return PRMergedArgForState("") // lookup error → unknown (AC-G2)
	}
	return PRMergedArgForState(state)
}

// enrichEntryFromData parses the script's ok:true data payload and populates
// alreadyRemoved and warnings into the entry.
func enrichEntryFromData(entry WorktreeCleanupEntry, raw json.RawMessage) WorktreeCleanupEntry {
	var data map[string]interface{}
	if err := json.Unmarshal(raw, &data); err != nil {
		return entry
	}

	// Detect alreadyRemoved: the script returns ok:true with no branchDelete/docker/
	// tmuxKill fields when the worktree was not found (idempotent no-op path). We also
	// treat the case where the worktree was listed as non-existent (script returns
	// ok:true with only worktreeId) as alreadyRemoved.
	// Note: tmuxKill is also a "real removal happened" signal — a response with a
	// tmuxKill field means the script DID reach the removal stages, so it is not
	// an already-removed no-op.
	_, hasBranch := data["branchDelete"]
	_, hasDocker := data["dockerTeardown"]
	_, hasSkipped := data["skipped"]
	_, hasTmuxKill := data["tmuxKill"]
	if !hasBranch && !hasDocker && !hasSkipped && !hasTmuxKill {
		// Script returned ok:true with just worktreeId — worktree was not found.
		entry.AlreadyRemoved = true
	}

	// Collect non-fatal warnings from branchDelete and dockerTeardown sub-objects.
	if bd, ok := data["branchDelete"].(map[string]interface{}); ok && bd != nil {
		if skipReason, ok := bd["skipReason"].(string); ok && skipReason != "" {
			if warning, ok := bd["warning"].(string); ok && warning != "" {
				entry.Warnings = append(entry.Warnings, warning)
			} else {
				entry.Warnings = append(entry.Warnings, "branch-skip: "+skipReason)
			}
		}
	}
	// AC-G3: surface the tmux-kill warning when the kill stage failed.
	// The script emits tmuxKill as:
	//   success: {"stage":"tmux-kill","killed":true}
	//   not-found: {"stage":"tmux-kill","killed":false,"reason":"session-not-found"}
	//   failure: {"stage":"tmux-kill","warning":"tmux kill-session failed: <msg>"}
	// Only the failure shape carries a "warning" field; surface it here.
	if tk, ok := data["tmuxKill"].(map[string]interface{}); ok && tk != nil {
		if warning, ok := tk["warning"].(string); ok && warning != "" {
			entry.Warnings = append(entry.Warnings, warning)
		}
	}

	if dt, ok := data["dockerTeardown"].(map[string]interface{}); ok && dt != nil {
		if action, ok := dt["action"].(string); ok && action == "error" {
			if reason, ok := dt["reason"].(string); ok && reason != "" {
				entry.Warnings = append(entry.Warnings, "docker-teardown error: "+reason)
			}
		}
	}

	return entry
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
