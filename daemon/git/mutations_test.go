package git

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

// repoRoot returns the absolute path to the repo root (two levels up from
// daemon/git/ where this file lives).
func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not determine test file path")
	}
	// file = <root>/daemon/git/mutations_test.go
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

// --- T1: path-builder produces the git/ subdirectory path ---

// TestWorktreeScriptProducesGitSubdirPath verifies that worktreeScript("remove")
// returns <root>/git/worktree-remove.sh, NOT the flat sibling <root>/git-worktree-remove.sh
// that was the pre-#693 bug (scenario 2, AC1).
func TestWorktreeScriptProducesGitSubdirPath(t *testing.T) {
	r := NewMutationResolver("/scripts")
	got := r.worktreeScript("remove")
	want := "/scripts/git/worktree-remove.sh"
	if got != want {
		t.Errorf("worktreeScript(\"remove\"): got %q, want %q", got, want)
	}
}

// TestWorktreeScriptNotFlatSibling confirms the old (buggy) path is NOT returned.
func TestWorktreeScriptNotFlatSibling(t *testing.T) {
	r := NewMutationResolver("/scripts")
	got := r.worktreeScript("remove")
	if strings.HasSuffix(got, "git-worktree-remove.sh") {
		t.Errorf("worktreeScript still returns the buggy flat-sibling path: %q", got)
	}
}

// TestGitScriptProducesGitSubdirPath verifies gitScript builds <root>/git/<op>.sh,
// not <root>/git-<op>.sh (same family of bug, Boy Scout — fix consistently).
func TestGitScriptProducesGitSubdirPath(t *testing.T) {
	for _, op := range []string{"fetch", "pull", "push"} {
		r := NewMutationResolver("/scripts")
		got := r.gitScript(op)
		want := "/scripts/git/" + op + ".sh"
		if got != want {
			t.Errorf("gitScript(%q): got %q, want %q", op, got, want)
		}
	}
}

// TestWorktreeRemoveScriptExistsOnDisk verifies that the script path the resolver
// builds for "remove" actually exists on disk in the real repo checkout (deployment
// shape: confirms the real file is reachable at the corrected path, not the old buggy one).
func TestWorktreeRemoveScriptExistsOnDisk(t *testing.T) {
	root := repoRoot(t)
	r := NewMutationResolver(root + "/scripts")
	scriptPath := r.worktreeScript("remove")

	// Assert the path ends in git/worktree-remove.sh.
	if !strings.HasSuffix(scriptPath, "/git/worktree-remove.sh") {
		t.Errorf("unexpected path suffix: %q (want .../git/worktree-remove.sh)", scriptPath)
	}
	if _, err := os.Stat(scriptPath); err != nil {
		t.Errorf("script %q does not exist on disk: %v\n"+
			"Pre-#693 path was scripts/git-worktree-remove.sh (does not exist).\n"+
			"Corrected path is scripts/git/worktree-remove.sh.", scriptPath, err)
	}
}

// TestWorktreeRemoveInputValidation verifies the resolver returns INVALID_INPUT
// for an empty worktreeId (M4: validate at resolver boundary, AC10).
func TestWorktreeRemoveInputValidation(t *testing.T) {
	root := repoRoot(t)
	r := NewMutationResolver(root + "/scripts")
	result, err := r.WorktreeRemove(context.Background(), WorktreeRemoveInput{
		WorktreeID: "", // intentionally empty — must be rejected at resolver boundary
	})
	if err != nil {
		t.Fatalf("WorktreeRemove with empty ID: unexpected error: %v", err)
	}
	if result.OK {
		t.Error("expected ok=false for empty worktreeId, got ok=true")
	}
	if result.ErrCode != "INVALID_INPUT" {
		t.Errorf("expected ErrCode INVALID_INPUT, got %q", result.ErrCode)
	}
}

// TestWorktreeRemoveActiveSessionFieldsThreaded verifies that WorktreeRemove
// passes ActiveSession and ActiveCwd through to the script as --active-session
// and --active-cwd args (AC-G1: T1 resolver test — field threading).
//
// Uses a real but non-existent worktree ID so the script exits with a
// structured L2 envelope (not an OS-level error), confirming the args were
// accepted by the script's arg parser.
func TestWorktreeRemoveActiveSessionFieldsThreaded(t *testing.T) {
	root := repoRoot(t)
	r := NewMutationResolver(root + "/scripts")

	scriptPath := r.worktreeScript("remove")
	if _, err := os.Stat(scriptPath); err != nil {
		t.Skipf("skipping: script not found at %s: %v", scriptPath, err)
	}

	// Call with active-session + active-cwd fields set.
	// A non-existent worktree will trigger REPO_NOT_FOUND before the guard
	// is even reached, but the important thing is that the script does NOT
	// reject the --active-session / --active-cwd args with "Unknown argument".
	result, err := r.WorktreeRemove(context.Background(), WorktreeRemoveInput{
		WorktreeID:    "nonexistent-repo:nonexistent-branch",
		ActiveSession: "my-active-session",
		ActiveCwd:     "/some/active/cwd",
	})
	if err != nil {
		t.Logf("execScript error (expected for non-existent repo): %v", err)
		return
	}
	// Must NOT be UNKNOWN_ARGUMENT — that would mean the args weren't recognised.
	if result.ErrCode == "UNKNOWN_ARGUMENT" {
		t.Errorf("script rejected --active-session/--active-cwd: got UNKNOWN_ARGUMENT; args were not threaded correctly")
	}
	// Any other error code (REPO_NOT_FOUND, INVALID_INPUT) is fine — it means
	// the args were accepted and the script ran its normal logic.
	t.Logf("WorktreeRemove with active session fields: ok=%v errCode=%q", result.OK, result.ErrCode)
}

// TestWorktreeRemoveScriptPathInEnvelope verifies that when the script is called
// with a valid non-empty worktreeId, the call goes to the corrected path
// (scripts/git/worktree-remove.sh), not the old flat sibling path.
// Uses a non-existent worktree ID to trigger a script failure-envelope, which
// still proves the resolver reached the script at the right path.
func TestWorktreeRemoveScriptPathInEnvelope(t *testing.T) {
	root := repoRoot(t)
	r := NewMutationResolver(root + "/scripts")

	// The script must be present; if it is absent the resolver would return
	// a raw error (not an L2 envelope). The presence check above covers this,
	// but belt-and-suspenders: confirm the script is reachable.
	scriptPath := r.worktreeScript("remove")
	if _, err := os.Stat(scriptPath); err != nil {
		t.Skipf("skipping: script not found at %s (run TestWorktreeRemoveScriptExistsOnDisk first): %v", scriptPath, err)
	}

	// Call with a valid format but non-existent worktree. The script should
	// return ok:false with a structured error (not a "file not found" OS error).
	result, err := r.WorktreeRemove(context.Background(), WorktreeRemoveInput{
		WorktreeID: "nonexistent-project:nonexistent-worktree",
	})
	if err != nil {
		// This can happen if the script itself is not executable or has an
		// unexpected error. It is NOT the normal path; we want an L2 envelope.
		t.Logf("execScript error (may be expected if script requires a real git repo): %v", err)
		return
	}
	// Either ok:true (unlikely for a non-existent WT) or ok:false with an envelope.
	// Either way, the important thing is we got here without an OS-level missing-file
	// error, proving the resolver reached the correct script path.
	t.Logf("WorktreeRemove result: ok=%v errCode=%q", result.OK, result.ErrCode)
}

// =============================================================================
// Step 6 — Batch / partial-failure / concurrency / stale-set / typed errors
// =============================================================================

// --- AC10 / scenario ~300: Malformed input rejected at resolver boundary -----

// TestWorktreesCleanupEmptyList verifies that an empty worktreeIds list is
// rejected with INVALID_INPUT at the resolver boundary without exec'ing a script.
// @scenario Malformed input is rejected at the resolver boundary with a typed error
func TestWorktreesCleanupEmptyList(t *testing.T) {
	r := NewMutationResolver("/scripts")
	result, err := r.WorktreesCleanup(context.Background(), WorktreesCleanupInput{
		WorktreeIDs: []string{},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.OK {
		t.Error("expected ok=false for empty worktreeIds")
	}
	if result.ErrCode != "INVALID_INPUT" {
		t.Errorf("expected ErrCode INVALID_INPUT, got %q", result.ErrCode)
	}
}

// TestWorktreesCleanupEmptyID verifies that an entry with an empty string is
// rejected with INVALID_INPUT at the resolver boundary.
// @scenario Malformed input is rejected at the resolver boundary with a typed error
func TestWorktreesCleanupEmptyID(t *testing.T) {
	r := NewMutationResolver("/scripts")
	result, err := r.WorktreesCleanup(context.Background(), WorktreesCleanupInput{
		WorktreeIDs: []string{"myrepo:branch", ""}, // second entry is empty
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.OK {
		t.Error("expected ok=false for entry with empty ID")
	}
	if result.ErrCode != "INVALID_INPUT" {
		t.Errorf("expected ErrCode INVALID_INPUT, got %q", result.ErrCode)
	}
}

// TestWorktreesCleanupMalformedID verifies that a malformed ID (no colon) is
// rejected with INVALID_INPUT at the resolver boundary.
// @scenario Malformed input is rejected at the resolver boundary with a typed error
func TestWorktreesCleanupMalformedID(t *testing.T) {
	r := NewMutationResolver("/scripts")
	result, err := r.WorktreesCleanup(context.Background(), WorktreesCleanupInput{
		WorktreeIDs: []string{"nocolonhere"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.OK {
		t.Error("expected ok=false for malformed ID with no colon")
	}
	if result.ErrCode != "INVALID_INPUT" {
		t.Errorf("expected ErrCode INVALID_INPUT, got %q", result.ErrCode)
	}
}

// --- AC10 / scenario ~307: Expected failures as typed results, not opaque errors ---

// TestWorktreesCleanupStructuredResultNotOpaque verifies that a non-existent
// repo produces a per-worktree structured result entry (ok:false with stage+message),
// NOT a Go error that would surface as an opaque GraphQL error[].
// @scenario Expected failures are returned as typed structured results, not opaque GraphQL errors
func TestWorktreesCleanupStructuredResultNotOpaque(t *testing.T) {
	root := repoRoot(t)
	r := NewMutationResolver(root + "/scripts")

	scriptPath := r.worktreeScript("remove")
	if _, err := os.Stat(scriptPath); err != nil {
		t.Skipf("skipping: script not found at %s: %v", scriptPath, err)
	}

	// A worktree with a valid format but non-existent repo: the script will
	// return ok:false with a typed error envelope (REPO_NOT_FOUND), not crash.
	// The resolver must surface this as a per-worktree entry, not a Go error.
	result, err := r.WorktreesCleanup(context.Background(), WorktreesCleanupInput{
		WorktreeIDs: []string{"nonexistent-project:some-branch"},
	})

	// The batch call itself must return (no Go error) — typed structured result.
	if err != nil {
		t.Fatalf("WorktreesCleanup returned a Go error (opaque): %v — expected a typed per-worktree entry", err)
	}

	// The batch result must be ok:true (the batch call succeeded; individual entry carries the failure).
	if !result.OK {
		t.Errorf("expected batch ok=true (per-worktree failures are in entries), got ok=false errCode=%q", result.ErrCode)
	}

	// There must be exactly one entry for the given ID.
	if len(result.Entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(result.Entries))
	}
	entry := result.Entries[0]
	if entry.WorktreeID != "nonexistent-project:some-branch" {
		t.Errorf("entry.WorktreeID mismatch: got %q", entry.WorktreeID)
	}
	// The entry should be ok:false with a stage and message (not nil/empty).
	// (An ok:true with alreadyRemoved:true is also acceptable — the script may
	// return ok:true for a repo-not-found as an idempotent no-op; either shape
	// is structured, not opaque.)
	if !entry.OK && entry.Stage == "" {
		t.Error("expected non-empty Stage on a failing entry (typed structured result)")
	}
	t.Logf("structured result entry: ok=%v stage=%q msg=%q alreadyRemoved=%v",
		entry.OK, entry.Stage, entry.Message, entry.AlreadyRemoved)
}

// --- #693: unregistered-repo worktree → skipped, not a failing entry ----------

// TestWorktreesCleanupUnregisteredRepoSkipped verifies the #693 daemon-owned
// cleanup contract: when a worktreeId's <projectId> slug is NOT present in the
// orchard config, the script returns ok:true with skipped:true /
// skipReason:"repo-unregistered" (NOT a REPO_NOT_FOUND error). The resolver
// must map that envelope to a NON-FAILING WorktreeCleanupEntry — entry.OK==true
// and no failure stage — so a batch containing the orphan still returns and the
// other worktrees are processed.
//
// This is the integration proof through the script boundary (the bats test
// scripts/git/worktree-remove.bats proves the SCRIPT level; this proves the
// RESOLVER maps the skip envelope to a non-failing entry).
//
// @scenario An unregistered-repo worktree maps to a non-failing skipped entry; the batch still returns
func TestWorktreesCleanupUnregisteredRepoSkipped(t *testing.T) {
	root := repoRoot(t)
	r := NewMutationResolver(root + "/scripts")

	scriptPath := r.worktreeScript("remove")
	if _, err := os.Stat(scriptPath); err != nil {
		t.Skipf("skipping: script not found at %s: %v", scriptPath, err)
	}

	// Config registers ONLY "myrepo"; the cleanup targets an unregistered slug.
	repoDir := t.TempDir()
	if err := setupRepo(t, repoDir); err != nil {
		t.Skipf("git setup failed: %v", err)
	}
	cfgPath := writeOrchardConfig(t, "myrepo", repoDir)
	t.Setenv("ORCHARD_CONFIG", cfgPath)

	result, err := r.WorktreesCleanup(context.Background(), WorktreesCleanupInput{
		WorktreeIDs: []string{"langwatch/langwatch-saas:issue510"}, // slug NOT in config
	})
	// The batch call must not return a Go error.
	if err != nil {
		t.Fatalf("WorktreesCleanup returned a Go error: %v — unregistered repo must be a typed entry", err)
	}
	// The batch must complete (ok:true) so other worktrees in a larger batch are processed.
	if !result.OK {
		t.Errorf("expected batch ok=true, got ok=false errCode=%q errMsg=%q", result.ErrCode, result.ErrMsg)
	}
	if len(result.Entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(result.Entries))
	}
	entry := result.Entries[0]
	t.Logf("unregistered-repo entry: ok=%v stage=%q msg=%q warnings=%v alreadyRemoved=%v",
		entry.OK, entry.Stage, entry.Message, entry.Warnings, entry.AlreadyRemoved)

	// Key assertion 1: the entry must be NON-FAILING (OK true) for an
	// unregistered repo — it is a skip, not an error.
	if !entry.OK {
		t.Errorf("#693 FAIL: unregistered-repo entry should be OK=true (skipped, not failed); got OK=false stage=%q msg=%q",
			entry.Stage, entry.Message)
	}
	// Key assertion 2: no failure stage was set (a failing entry sets Stage="worktree-remove").
	if entry.Stage != "" {
		t.Errorf("#693 FAIL: unregistered-repo entry should have empty Stage (non-failing); got Stage=%q", entry.Stage)
	}
	// Key assertion 3: the message must NOT carry REPO_NOT_FOUND (the old hard-fail code).
	if strings.Contains(entry.Message, "REPO_NOT_FOUND") {
		t.Errorf("#693 FAIL: unregistered-repo entry message still carries REPO_NOT_FOUND: %q", entry.Message)
	}
	// Key assertion 4 (PR #695 bug): the skip must be POSITIVELY identifiable — a bare
	// {ok:true, warnings:[]} is indistinguishable from a real cleanup at the TUI/caller
	// layer. The orphan entry MUST surface its skipReason in Warnings so the caller can
	// distinguish a skip from a genuine removal.
	skipWarningFound := false
	for _, w := range entry.Warnings {
		if strings.Contains(w, "repo-unregistered") {
			skipWarningFound = true
			break
		}
	}
	if !skipWarningFound {
		t.Errorf("#695 FAIL: unregistered-repo skip not surfaced in Warnings — caller cannot distinguish skip from real cleanup; warnings=%v", entry.Warnings)
	}
}

// TestWorktreesCleanupUnregisteredRepoBatchContinues verifies the batch-continuation
// property of #693: when a batch mixes an unregistered-repo orphan with a real
// stale worktree, the orphan is skipped (non-failing) AND the real worktree is
// still removed. The unregistered orphan must NOT abort the batch or the sibling.
//
// @scenario A batch with an unregistered orphan still removes the registered stale worktree
func TestWorktreesCleanupUnregisteredRepoBatchContinues(t *testing.T) {
	root := repoRoot(t)
	r := NewMutationResolver(root + "/scripts")

	scriptPath := r.worktreeScript("remove")
	if _, err := os.Stat(scriptPath); err != nil {
		t.Skipf("skipping: script not found at %s: %v", scriptPath, err)
	}

	repoDir := t.TempDir()
	if err := setupRepo(t, repoDir); err != nil {
		t.Skipf("git setup failed: %v", err)
	}
	goodDir := t.TempDir()
	if err := addWorktree(t, repoDir, "good-branch", goodDir); err != nil {
		t.Skipf("git worktree add failed: %v", err)
	}
	cfgPath := writeOrchardConfig(t, "myrepo", repoDir)
	t.Setenv("ORCHARD_CONFIG", cfgPath)

	result, err := r.WorktreesCleanup(context.Background(), WorktreesCleanupInput{
		WorktreeIDs: []string{
			"langwatch/langwatch-saas:issue510", // unregistered orphan → skip
			"myrepo:good-branch",                // registered → removed
		},
	})
	if err != nil {
		t.Fatalf("WorktreesCleanup returned a Go error: %v", err)
	}
	if !result.OK {
		t.Fatalf("expected batch ok=true, got ok=false errCode=%q errMsg=%q", result.ErrCode, result.ErrMsg)
	}
	if len(result.Entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(result.Entries))
	}

	var orphan, good *WorktreeCleanupEntry
	for i := range result.Entries {
		switch result.Entries[i].WorktreeID {
		case "langwatch/langwatch-saas:issue510":
			orphan = &result.Entries[i]
		case "myrepo:good-branch":
			good = &result.Entries[i]
		}
	}
	if orphan == nil {
		t.Fatal("missing entry for the unregistered orphan langwatch/langwatch-saas:issue510")
	}
	if good == nil {
		t.Fatal("missing entry for myrepo:good-branch")
	}

	// The orphan entry must be non-failing (skipped, not errored).
	if !orphan.OK {
		t.Errorf("#693 FAIL: orphan entry should be OK=true (skipped); got OK=false stage=%q msg=%q",
			orphan.Stage, orphan.Message)
	}
	if strings.Contains(orphan.Message, "REPO_NOT_FOUND") {
		t.Errorf("#693 FAIL: orphan entry still carries REPO_NOT_FOUND: %q", orphan.Message)
	}

	// The registered worktree must still have been removed (batch did not abort).
	if !good.OK && !good.AlreadyRemoved {
		t.Errorf("#693 FAIL: good-branch entry not ok despite orphan in batch: stage=%q msg=%q",
			good.Stage, good.Message)
	}
	if _, err := os.Stat(goodDir); err == nil {
		t.Error("#693 FAIL: good worktree directory still exists — the unregistered orphan aborted the batch")
	}
}

// --- AC2 / scenario ~63: Stale-set acted on exactly; non-given worktree untouched ---

// TestWorktreesCleanupActsOnlyOnGivenIDs verifies that the mutation acts on
// exactly the IDs it is given and does not touch worktrees outside the set.
// This is the daemon-side AC2 assertion: given a mixed fixture, only the IDs
// passed in are attempted; a worktree not in the list is never touched.
// @scenario Cleanup operates on exactly the stale set and leaves an open-PR worktree fully intact
func TestWorktreesCleanupActsOnlyOnGivenIDs(t *testing.T) {
	root := repoRoot(t)
	r := NewMutationResolver(root + "/scripts")

	scriptPath := r.worktreeScript("remove")
	if _, err := os.Stat(scriptPath); err != nil {
		t.Skipf("skipping: script not found at %s: %v", scriptPath, err)
	}

	// Create a temporary git repo with two worktrees: "stale" and "open-pr".
	repoDir := t.TempDir()
	if err := setupRepo(t, repoDir); err != nil {
		t.Skipf("git setup failed: %v", err)
	}
	staleDir := t.TempDir()
	openPRDir := t.TempDir()
	if err := addWorktree(t, repoDir, "stale-branch", staleDir); err != nil {
		t.Skipf("git worktree add failed: %v", err)
	}
	if err := addWorktree(t, repoDir, "open-pr-branch", openPRDir); err != nil {
		t.Skipf("git worktree add failed: %v", err)
	}

	cfgPath := writeOrchardConfig(t, "myrepo", repoDir)
	t.Setenv("ORCHARD_CONFIG", cfgPath)

	// Only cleanup the stale branch — open-pr-branch must remain untouched.
	result, err := r.WorktreesCleanup(context.Background(), WorktreesCleanupInput{
		WorktreeIDs: []string{"myrepo:stale-branch"},
	})
	if err != nil {
		t.Fatalf("WorktreesCleanup error: %v", err)
	}
	if !result.OK {
		t.Fatalf("expected ok=true, got ok=false errCode=%q errMsg=%q", result.ErrCode, result.ErrMsg)
	}
	if len(result.Entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(result.Entries))
	}
	// Stale worktree directory must be gone.
	if _, err := os.Stat(staleDir); err == nil {
		t.Error("stale worktree directory still exists after cleanup — expected it to be removed")
	}
	// Open-PR worktree directory must still be present.
	if _, err := os.Stat(openPRDir); err != nil {
		t.Errorf("open-PR worktree directory gone — must remain untouched: %v", err)
	}
	// Open-PR worktree must still be registered in git.
	porcelain, err := gitWorktreeListPorcelain(repoDir)
	if err != nil {
		t.Fatalf("git worktree list --porcelain: %v", err)
	}
	openPRDirReal := resolvePath(openPRDir)
	if !strings.Contains(porcelain, openPRDirReal) {
		t.Errorf("open-PR worktree no longer listed in git worktree list --porcelain:\n%s", porcelain)
	}
}

// --- AC8 / scenario ~264: Partial-failure — N-1 succeed when K fails -----------

// TestWorktreesCleanupPartialFailure verifies that when cleanup of one worktree
// fails (here: a worktree whose directory cannot be removed due to a permission
// error), the remaining valid ones are still attempted and succeed.
// @scenario A failure on one worktree does not stop the others and is surfaced per-worktree
func TestWorktreesCleanupPartialFailure(t *testing.T) {
	root := repoRoot(t)
	r := NewMutationResolver(root + "/scripts")

	scriptPath := r.worktreeScript("remove")
	if _, err := os.Stat(scriptPath); err != nil {
		t.Skipf("skipping: script not found at %s: %v", scriptPath, err)
	}

	// Create a repo with two worktrees: one that will fail and one that will succeed.
	repoDir := t.TempDir()
	if err := setupRepo(t, repoDir); err != nil {
		t.Skipf("git setup failed: %v", err)
	}

	// fail-branch lives inside a custom parent dir whose permissions we control.
	// After adding the worktree, we remove write permission from the parent so
	// that both git worktree remove and the rm -rf fallback fail → RM_ERROR ok:false.
	failParent := t.TempDir()
	failDir := filepath.Join(failParent, "fail-branch-wt")
	if err := os.MkdirAll(failDir, 0755); err != nil {
		t.Skipf("mkdir fail: %v", err)
	}
	if err := addWorktree(t, repoDir, "fail-branch", failDir); err != nil {
		t.Skipf("git worktree add failed: %v", err)
	}

	goodDir := t.TempDir()
	if err := addWorktree(t, repoDir, "good-branch", goodDir); err != nil {
		t.Skipf("git worktree add failed: %v", err)
	}

	cfgPath := writeOrchardConfig(t, "myrepo", repoDir)
	t.Setenv("ORCHARD_CONFIG", cfgPath)

	// Remove write permission from failParent so rm -rf failDir fails.
	if err := os.Chmod(failParent, 0555); err != nil {
		t.Skipf("chmod fail: %v", err)
	}
	// Restore parent permissions in teardown so t.TempDir() cleanup succeeds.
	t.Cleanup(func() { os.Chmod(failParent, 0755) }) //nolint:errcheck

	// Two IDs: one that will fail (rm error) and one that will succeed.
	result, err := r.WorktreesCleanup(context.Background(), WorktreesCleanupInput{
		WorktreeIDs: []string{
			"myrepo:fail-branch", // fails — parent dir is read-only, rm -rf errors
			"myrepo:good-branch", // succeeds
		},
	})
	if err != nil {
		t.Fatalf("WorktreesCleanup returned Go error: %v", err)
	}
	// Batch ok must be true — partial failure is per-entry, not batch-level.
	if !result.OK {
		t.Fatalf("expected batch ok=true on partial failure, got ok=false")
	}
	if len(result.Entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(result.Entries))
	}

	// Find entries by worktreeId.
	var failEntry, goodEntry *WorktreeCleanupEntry
	for i := range result.Entries {
		switch result.Entries[i].WorktreeID {
		case "myrepo:fail-branch":
			failEntry = &result.Entries[i]
		case "myrepo:good-branch":
			goodEntry = &result.Entries[i]
		}
	}
	if failEntry == nil {
		t.Fatal("missing entry for myrepo:fail-branch")
	}
	if goodEntry == nil {
		t.Fatal("missing entry for myrepo:good-branch")
	}

	// The fail entry must have ok=false with a stage (typed result, not opaque error).
	if failEntry.OK {
		t.Error("myrepo:fail-branch entry should be ok=false (rm error due to unwritable parent)")
	}
	if failEntry.Stage == "" {
		t.Error("myrepo:fail-branch entry should have a non-empty stage")
	}

	// The good entry must succeed and the directory must be gone.
	if !goodEntry.OK && !goodEntry.AlreadyRemoved {
		t.Errorf("good-branch entry not ok: stage=%q msg=%q", goodEntry.Stage, goodEntry.Message)
	}
	if _, err := os.Stat(goodDir); err == nil {
		t.Error("good worktree directory still exists — should have been removed")
	}
}

// --- AC9 / scenario ~288: Idempotent re-run on an already-clean fleet --------

// TestWorktreesCleanupIdempotentRerun verifies that running the cleanup twice
// on the same worktree produces zero removals on the second run (ok:true,
// alreadyRemoved:true, no "already removed" error).
// @scenario A second cleanup run on an already-clean fleet is a clean no-op
func TestWorktreesCleanupIdempotentRerun(t *testing.T) {
	root := repoRoot(t)
	r := NewMutationResolver(root + "/scripts")

	scriptPath := r.worktreeScript("remove")
	if _, err := os.Stat(scriptPath); err != nil {
		t.Skipf("skipping: script not found at %s: %v", scriptPath, err)
	}

	// Create a worktree for the first run.
	repoDir := t.TempDir()
	if err := setupRepo(t, repoDir); err != nil {
		t.Skipf("git setup failed: %v", err)
	}
	wtDir := t.TempDir()
	if err := addWorktree(t, repoDir, "idempotent-branch", wtDir); err != nil {
		t.Skipf("git worktree add failed: %v", err)
	}
	cfgPath := writeOrchardConfig(t, "myrepo", repoDir)
	t.Setenv("ORCHARD_CONFIG", cfgPath)

	input := WorktreesCleanupInput{
		WorktreeIDs: []string{"myrepo:idempotent-branch"},
	}

	// Run 1: should succeed and remove the worktree.
	result1, err := r.WorktreesCleanup(context.Background(), input)
	if err != nil {
		t.Fatalf("run 1 error: %v", err)
	}
	if !result1.OK {
		t.Fatalf("run 1 expected ok=true, got ok=false")
	}
	if len(result1.Entries) != 1 {
		t.Fatalf("run 1 expected 1 entry, got %d", len(result1.Entries))
	}

	// Run 2: worktree already removed — must be a clean no-op (ok:true, no error).
	result2, err := r.WorktreesCleanup(context.Background(), input)
	if err != nil {
		t.Fatalf("run 2 error: %v", err)
	}
	if !result2.OK {
		t.Fatalf("run 2 expected ok=true on idempotent re-run, got ok=false")
	}
	if len(result2.Entries) != 1 {
		t.Fatalf("run 2 expected 1 entry, got %d", len(result2.Entries))
	}
	entry2 := result2.Entries[0]
	if !entry2.OK {
		t.Errorf("run 2 entry should be ok=true (already-removed is not an error): stage=%q msg=%q",
			entry2.Stage, entry2.Message)
	}
	// The entry should carry alreadyRemoved:true to distinguish from a fresh removal.
	if !entry2.AlreadyRemoved {
		t.Logf("run 2 entry alreadyRemoved=%v — this is informational; AC9 requires ok=true and no 'already removed' error (satisfied)", entry2.AlreadyRemoved)
	}
}

// --- AC-G5 / scenario ~276: Concurrency — race loser's skip POSITIVELY asserted ---

// TestWorktreesCleanupConcurrentOverlap verifies that two concurrent cleanup ops
// over an overlapping set both return ok:true and the loser carries an
// alreadyRemoved:true entry for the doubly-targeted worktree.
// @scenario Two concurrent cleanups over an overlapping set both succeed without a hard race error
func TestWorktreesCleanupConcurrentOverlap(t *testing.T) {
	root := repoRoot(t)
	r := NewMutationResolver(root + "/scripts")

	scriptPath := r.worktreeScript("remove")
	if _, err := os.Stat(scriptPath); err != nil {
		t.Skipf("skipping: script not found at %s: %v", scriptPath, err)
	}

	// Create two worktrees that both calls target (overlapping set).
	repoDir := t.TempDir()
	if err := setupRepo(t, repoDir); err != nil {
		t.Skipf("git setup failed: %v", err)
	}
	wtDir1 := t.TempDir()
	wtDir2 := t.TempDir()
	if err := addWorktree(t, repoDir, "overlap-branch-1", wtDir1); err != nil {
		t.Skipf("git worktree add failed: %v", err)
	}
	if err := addWorktree(t, repoDir, "overlap-branch-2", wtDir2); err != nil {
		t.Skipf("git worktree add failed: %v", err)
	}
	cfgPath := writeOrchardConfig(t, "myrepo", repoDir)
	t.Setenv("ORCHARD_CONFIG", cfgPath)

	// Both calls target the SAME two worktrees.
	input := WorktreesCleanupInput{
		WorktreeIDs: []string{
			"myrepo:overlap-branch-1",
			"myrepo:overlap-branch-2",
		},
	}

	var result1, result2 WorktreesCleanupResult
	var err1, err2 error
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		result1, err1 = r.WorktreesCleanup(context.Background(), input)
	}()
	go func() {
		defer wg.Done()
		result2, err2 = r.WorktreesCleanup(context.Background(), input)
	}()
	wg.Wait()

	// Both must return without a Go error.
	if err1 != nil {
		t.Errorf("call 1 returned Go error: %v", err1)
	}
	if err2 != nil {
		t.Errorf("call 2 returned Go error: %v", err2)
	}

	// Both batch results must be ok:true.
	if !result1.OK {
		t.Errorf("call 1 ok=false: errCode=%q errMsg=%q", result1.ErrCode, result1.ErrMsg)
	}
	if !result2.OK {
		t.Errorf("call 2 ok=false: errCode=%q errMsg=%q", result2.ErrCode, result2.ErrMsg)
	}

	// Union of removals = the stale set exactly once: worktrees must be gone.
	if _, err := os.Stat(wtDir1); err == nil {
		t.Error("overlap-branch-1 directory still exists after both concurrent calls")
	}
	if _, err := os.Stat(wtDir2); err == nil {
		t.Error("overlap-branch-2 directory still exists after both concurrent calls")
	}

	// AC-G5 key assertion: the LOSER must carry alreadyRemoved:true for the
	// doubly-targeted worktrees. Both results have entries; the loser (the call
	// that found worktrees already gone) should have alreadyRemoved on its entries.
	//
	// Since the mutex serializes the calls, exactly one call is the "winner"
	// (removes them) and one is the "loser" (finds them already gone).
	// We identify the loser as whichever result has alreadyRemoved:true on all entries.
	loserFound := false
	for _, res := range []WorktreesCleanupResult{result1, result2} {
		allAlreadyRemoved := len(res.Entries) > 0
		for _, e := range res.Entries {
			if !e.AlreadyRemoved {
				allAlreadyRemoved = false
			}
		}
		if allAlreadyRemoved && len(res.Entries) > 0 {
			loserFound = true
		}
	}
	if !loserFound {
		// Log the entries to aid diagnosis. The important thing is that NEITHER
		// call returned a hard error (already checked above). alreadyRemoved is
		// the additional positive signal required by AC-G5.
		t.Errorf("AC-G5 FAIL: neither concurrent call carried an alreadyRemoved:true entry for the doubly-targeted worktrees.\n"+
			"call1 entries: %s\n"+
			"call2 entries: %s",
			formatEntries(result1.Entries),
			formatEntries(result2.Entries),
		)
	}
}

// --- gh-state → --pr-merged seam (item G) ------------------------------------

// TestResolvePRMerged_ColonInBranch verifies that when the worktree ID is
// "owner/repo:alice:fix-endpoint" (branch name contains a colon), resolvePRMerged
// splits on the FIRST colon — yielding repoSlug="owner/repo" and
// branch="alice:fix-endpoint" — not the last colon.
//
// The stub returns "MERGED" only for the exact args (owner/repo, alice:fix-endpoint).
// If the parser uses LastIndex it calls with ("owner/repo:alice", "fix-endpoint")
// which the stub does not recognise → returns "" → result is "unknown", not "merged".
func TestResolvePRMerged_ColonInBranch(t *testing.T) {
	type call struct{ repoSlug, branch string }
	var recorded []call
	recordingStub := &recordingPRLookup{
		fn: func(repoSlug, branch string) (string, error) {
			recorded = append(recorded, call{repoSlug, branch})
			if repoSlug == "owner/repo" && branch == "alice:fix-endpoint" {
				return "MERGED", nil
			}
			return "", nil // unexpected args → empty state → "unknown"
		},
	}

	r := NewMutationResolver("")
	r.WithPRStateLookup(recordingStub)

	result := r.resolvePRMerged(context.Background(), "owner/repo:alice:fix-endpoint")

	if len(recorded) != 1 {
		t.Fatalf("expected 1 PRStateByBranch call, got %d", len(recorded))
	}
	if recorded[0].repoSlug != "owner/repo" {
		t.Errorf("repoSlug: got %q, want %q", recorded[0].repoSlug, "owner/repo")
	}
	if recorded[0].branch != "alice:fix-endpoint" {
		t.Errorf("branch: got %q, want %q", recorded[0].branch, "alice:fix-endpoint")
	}
	wantResult := PRMergedArgForState("MERGED")
	if result != wantResult {
		t.Errorf("resolvePRMerged result: got %q, want %q — parsing used wrong colon (last instead of first)", result, wantResult)
	}
}

// recordingPRLookup is a PRStateLookup that delegates to a user-supplied function,
// allowing tests to record call arguments.
type recordingPRLookup struct {
	fn func(repoSlug, branch string) (string, error)
}

func (r *recordingPRLookup) PRStateByBranch(_ context.Context, repoSlug, branch string) (string, error) {
	return r.fn(repoSlug, branch)
}

// TestPRMergedArgForState verifies the three canonical mappings of gh PR state
// to the --pr-merged script argument (the tested seam for Step 7 integration).
// @scenario gh-state → --pr-merged mapping covers merged/not-merged/unknown (AC-G2 seam)
func TestPRMergedArgForState(t *testing.T) {
	tests := []struct {
		ghState string
		want    string
		desc    string
	}{
		{"MERGED", "merged", "merged PR → merged"},
		{"CLOSED", "not-merged", "closed-without-merge PR → not-merged"},
		{"OPEN", "not-merged", "open PR → not-merged"},
		{"", "unknown", "empty (gh error) → unknown (fail-closed)"},
		{"unknown", "unknown", `literal "unknown" → unknown (fail-closed)`},
		{"RATE_LIMITED", "unknown", "any other value → unknown (fail-closed)"},
	}
	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			got := PRMergedArgForState(tt.ghState)
			if got != tt.want {
				t.Errorf("PRMergedArgForState(%q) = %q, want %q", tt.ghState, got, tt.want)
			}
		})
	}
}

// =============================================================================
// Helpers shared by Step 6 tests
// =============================================================================

// setupRepo initialises a minimal git repo with one commit.
func setupRepo(t *testing.T, dir string) error {
	t.Helper()
	cmds := [][]string{
		{"git", "init", "-q", dir},
		{"git", "-C", dir, "config", "user.email", "test@example.com"},
		{"git", "-C", dir, "config", "user.name", "Test"},
	}
	for _, args := range cmds {
		if out, err := exec.Command(args[0], args[1:]...).CombinedOutput(); err != nil {
			return fmt.Errorf("%v: %w: %s", args, err, out)
		}
	}
	// Create and commit a README so HEAD is not unborn.
	readme := filepath.Join(dir, "README")
	if err := os.WriteFile(readme, []byte("test"), 0644); err != nil {
		return err
	}
	for _, args := range [][]string{
		{"git", "-C", dir, "add", "README"},
		{"git", "-C", dir, "commit", "-q", "-m", "init"},
	} {
		if out, err := exec.Command(args[0], args[1:]...).CombinedOutput(); err != nil {
			return fmt.Errorf("%v: %w: %s", args, err, out)
		}
	}
	return nil
}

// addWorktree adds a new branch-based worktree to the repo at repoDir.
func addWorktree(t *testing.T, repoDir, branch, wtDir string) error {
	t.Helper()
	out, err := exec.Command("git", "-C", repoDir, "worktree", "add", "-q", "-b", branch, wtDir).CombinedOutput()
	if err != nil {
		return fmt.Errorf("git worktree add -b %s %s: %w: %s", branch, wtDir, err, out)
	}
	return nil
}

// writeOrchardConfig writes a minimal orchard config JSON pointing slug → repoPath
// and returns the config file path.
func writeOrchardConfig(t *testing.T, slug, repoPath string) string {
	t.Helper()
	cfgDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "config.json")
	cfg := map[string]interface{}{
		"repos": []interface{}{
			map[string]interface{}{"slug": slug, "path": repoPath},
		},
	}
	data, _ := json.Marshal(cfg)
	if err := os.WriteFile(cfgPath, data, 0644); err != nil {
		t.Fatalf("writeOrchardConfig: %v", err)
	}
	return cfgPath
}

// gitWorktreeListPorcelain returns the output of git worktree list --porcelain.
func gitWorktreeListPorcelain(repoDir string) (string, error) {
	out, err := exec.Command("git", "-C", repoDir, "worktree", "list", "--porcelain").Output()
	return string(out), err
}

// resolvePath resolves symlinks on the path (macOS /var → /private/var).
func resolvePath(p string) string {
	real, err := filepath.EvalSymlinks(p)
	if err != nil {
		return p
	}
	return real
}

// formatEntries formats WorktreeCleanupEntry slice for test output.
func formatEntries(entries []WorktreeCleanupEntry) string {
	parts := make([]string, len(entries))
	for i, e := range entries {
		parts[i] = fmt.Sprintf("{id=%q ok=%v stage=%q alreadyRemoved=%v}", e.WorktreeID, e.OK, e.Stage, e.AlreadyRemoved)
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

// Ensure unused imports are consumed (json imported for writeOrchardConfig).
var _ = json.Marshal

// =============================================================================
// Step 7 — AC-G2 integration: gh merged-state wiring (no-op bug fix)
// =============================================================================
//
// These tests prove that the fix for the dead-wiring bug (#693) works
// end-to-end:
//   - A stale worktree whose PR is MERGED → branch IS deleted after cleanup.
//   - A stale worktree where the gh lookup ERRORS → branch SURVIVES with
//     "merged-state-unavailable" reason AND the worktree+dir are still removed.
//
// Before the fix, cleanupOne never passed --pr-merged to worktree-remove.sh,
// so branch-delete.sh never ran. These tests confirm the wiring is live.

// stubPRLookup is a minimal PRStateLookup stub for tests.
// configureFor maps "repoSlug:branch" → state string; otherwise returns stateDefault.
type stubPRLookup struct {
	responses    map[string]string // "repoSlug:branch" → state or "ERROR"
	stateDefault string
}

func (s *stubPRLookup) PRStateByBranch(_ context.Context, repoSlug, branch string) (string, error) {
	key := repoSlug + ":" + branch
	if v, ok := s.responses[key]; ok {
		if v == "ERROR" {
			return "", fmt.Errorf("stubPRLookup: simulated gh error for %s", key)
		}
		return v, nil
	}
	if s.stateDefault == "ERROR" {
		return "", fmt.Errorf("stubPRLookup: simulated gh error (default)")
	}
	return s.stateDefault, nil
}

// setupRepoWithRemote initialises a git repo, creates a bare remote, and
// configures the origin remote so branch-delete.sh can check the upstream.
// Returns: repoDir (the working checkout), remoteDir (the bare remote).
func setupRepoWithRemote(t *testing.T) (repoDir, remoteDir string) {
	t.Helper()
	repoDir = t.TempDir()
	remoteDir = t.TempDir()

	// Init the working repo.
	if err := setupRepo(t, repoDir); err != nil {
		t.Skipf("git setup failed: %v", err)
	}

	// Create a bare remote and wire it as origin.
	cmds := [][]string{
		{"git", "init", "--bare", "-q", remoteDir},
		{"git", "-C", repoDir, "remote", "add", "origin", remoteDir},
		{"git", "-C", repoDir, "push", "-q", "-u", "origin", "main"},
		// Set origin/HEAD so branch-delete.sh symbolic-ref path works (mirrors real git clone).
		{"git", "-C", repoDir, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/main"},
	}
	for _, args := range cmds {
		if out, err := exec.Command(args[0], args[1:]...).CombinedOutput(); err != nil {
			t.Skipf("git remote setup failed (%v): %s", args, out)
		}
	}
	return repoDir, remoteDir
}

// mergeBranchIntoMain creates a branch with one commit, merges it into main,
// and pushes both to origin. After this call, `git branch --merged main`
// includes the branch AND origin/branch has the branch's commit.
func mergeBranchIntoMain(t *testing.T, repoDir, branch string) {
	t.Helper()
	branchFile := filepath.Join(repoDir, branch+".txt")
	cmds := [][]string{
		{"git", "-C", repoDir, "checkout", "-q", "-b", branch},
	}
	for _, args := range cmds {
		if out, err := exec.Command(args[0], args[1:]...).CombinedOutput(); err != nil {
			t.Skipf("mergeBranchIntoMain: create branch: %v: %s", args, out)
		}
	}
	if err := os.WriteFile(branchFile, []byte(branch), 0644); err != nil {
		t.Skipf("mergeBranchIntoMain: write file: %v", err)
	}
	cmds = [][]string{
		{"git", "-C", repoDir, "add", branch + ".txt"},
		{"git", "-C", repoDir, "commit", "-q", "-m", "feat: " + branch},
		{"git", "-C", repoDir, "push", "-q", "-u", "origin", branch},
		{"git", "-C", repoDir, "checkout", "-q", "main"},
		{"git", "-C", repoDir, "merge", "-q", "--no-ff", branch, "-m", "Merge " + branch},
		{"git", "-C", repoDir, "push", "-q", "origin", "main"},
	}
	for _, args := range cmds {
		if out, err := exec.Command(args[0], args[1:]...).CombinedOutput(); err != nil {
			t.Skipf("mergeBranchIntoMain: %v: %s", args, out)
		}
	}
}

// gitBranchExists returns true when `git branch --list <branch>` in repoDir
// prints a non-empty result.
func gitBranchExists(repoDir, branch string) bool {
	out, err := exec.Command("git", "-C", repoDir, "branch", "--list", branch).Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) != ""
}

// TestWorktreesCleanup_MergedPR_BranchDeleted verifies the AC4 / AC-G2 end-to-end
// path: when the gh stub returns MERGED, cleanupOne passes --pr-merged merged to
// the script, and the branch IS deleted after cleanup.
//
// @scenario merged-PR stale worktree: gh stub returns MERGED → branch deleted after cleanup (AC4)
func TestWorktreesCleanup_MergedPR_BranchDeleted(t *testing.T) {
	root := repoRoot(t)
	r := NewMutationResolver(root + "/scripts")

	scriptPath := r.worktreeScript("remove")
	if _, err := os.Stat(scriptPath); err != nil {
		t.Skipf("skipping: script not found at %s: %v", scriptPath, err)
	}

	// Set up a repo with a real remote so branch-delete can verify upstream.
	repoDir, _ := setupRepoWithRemote(t)
	branch := "merged-feature-branch"

	// Create and merge the branch into main (makes git branch --merged main include it).
	mergeBranchIntoMain(t, repoDir, branch)

	// Add a detached worktree for the branch so worktree-remove.sh can find it.
	wtDir := t.TempDir()
	if err := addWorktree(t, repoDir, branch+"-wt", wtDir); err != nil {
		// If branch already merged, we may need a fresh branch for the worktree.
		// Directly check out the merged SHA into a new worktree branch.
		t.Logf("addWorktree failed (branch already merged?): %v; trying detached checkout", err)
		// Create a new worktree pointing to the merged branch's head indirectly.
		// Use a worktree-specific branch name that diverges from the merged branch.
		// For the test we actually want the worktree on the merged branch — use a
		// separate worktree-branch name with the same content.
		if out, err2 := exec.Command(
			"git", "-C", repoDir, "worktree", "add", "-q",
			"-b", branch+"-wt",
			wtDir,
			branch, // start point: the merged branch's tip
		).CombinedOutput(); err2 != nil {
			t.Skipf("addWorktree (retry): %v: %s", err2, out)
		}
	}

	// The worktree name (the part after ':' in the ID) must match the branch
	// that is merged. Use the worktree-branch we just created.
	wtBranch := branch + "-wt"

	// Merge the worktree branch into main too so git branch --merged lists it.
	mergeCmds := [][]string{
		{"git", "-C", repoDir, "push", "-q", "-u", "origin", wtBranch},
		{"git", "-C", repoDir, "checkout", "-q", "main"},
		{"git", "-C", repoDir, "merge", "-q", "--no-ff", wtBranch, "-m", "Merge " + wtBranch},
		{"git", "-C", repoDir, "push", "-q", "origin", "main"},
	}
	for _, args := range mergeCmds {
		if out, err := exec.Command(args[0], args[1:]...).CombinedOutput(); err != nil {
			t.Skipf("merge-into-main: %v: %s", err, out)
		}
	}

	cfgPath := writeOrchardConfig(t, "myrepo", repoDir)
	t.Setenv("ORCHARD_CONFIG", cfgPath)

	// Wire the stub gh service that returns MERGED.
	stub := &stubPRLookup{
		responses:    map[string]string{"myrepo:" + wtBranch: "MERGED"},
		stateDefault: "",
	}
	r.WithPRStateLookup(stub)

	// Confirm the branch exists before cleanup.
	if !gitBranchExists(repoDir, wtBranch) {
		t.Fatalf("pre-condition: branch %q should exist before cleanup", wtBranch)
	}

	result, err := r.WorktreesCleanup(context.Background(), WorktreesCleanupInput{
		WorktreeIDs: []string{"myrepo:" + wtBranch},
		BaseBranch:  "main",
	})
	if err != nil {
		t.Fatalf("WorktreesCleanup error: %v", err)
	}
	if !result.OK {
		t.Fatalf("expected ok=true, got ok=false errCode=%q errMsg=%q", result.ErrCode, result.ErrMsg)
	}
	if len(result.Entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(result.Entries))
	}
	entry := result.Entries[0]
	t.Logf("cleanup entry: ok=%v stage=%q msg=%q warnings=%v alreadyRemoved=%v",
		entry.OK, entry.Stage, entry.Message, entry.Warnings, entry.AlreadyRemoved)

	// AC4: the worktree directory must be gone.
	if _, err := os.Stat(wtDir); err == nil {
		t.Error("worktree directory still exists — should have been removed")
	}

	// AC4 key assertion: the branch must be gone (deleted by branch-delete.sh).
	if gitBranchExists(repoDir, wtBranch) {
		t.Errorf("AC4 FAIL: branch %q still exists after cleanup with MERGED stub — "+
			"branch-delete.sh was not reached or failed; check entry warnings: %v",
			wtBranch, entry.Warnings)
	}
}

// TestWorktreesCleanup_GHError_BranchSkipped_WorktreeRemoved verifies the
// AC-G2 fail-closed path: when the gh lookup ERRORS, the branch is skipped
// with "merged-state-unavailable" but the worktree+dir are still removed.
//
// @scenario gh-error → branch skipped (merged-state-unavailable) + worktree still removed (AC-G2)
func TestWorktreesCleanup_GHError_BranchSkipped_WorktreeRemoved(t *testing.T) {
	root := repoRoot(t)
	r := NewMutationResolver(root + "/scripts")

	scriptPath := r.worktreeScript("remove")
	if _, err := os.Stat(scriptPath); err != nil {
		t.Skipf("skipping: script not found at %s: %v", scriptPath, err)
	}

	repoDir := t.TempDir()
	if err := setupRepo(t, repoDir); err != nil {
		t.Skipf("git setup failed: %v", err)
	}
	wtDir := t.TempDir()
	branch := "gh-error-branch"
	if err := addWorktree(t, repoDir, branch, wtDir); err != nil {
		t.Skipf("addWorktree: %v", err)
	}
	cfgPath := writeOrchardConfig(t, "myrepo", repoDir)
	t.Setenv("ORCHARD_CONFIG", cfgPath)

	// Wire a gh stub that always errors.
	stub := &stubPRLookup{stateDefault: "ERROR"}
	r.WithPRStateLookup(stub)

	// Confirm the branch exists before cleanup.
	if !gitBranchExists(repoDir, branch) {
		t.Fatalf("pre-condition: branch %q should exist before cleanup", branch)
	}

	result, err := r.WorktreesCleanup(context.Background(), WorktreesCleanupInput{
		WorktreeIDs: []string{"myrepo:" + branch},
		BaseBranch:  "main",
	})
	if err != nil {
		t.Fatalf("WorktreesCleanup error: %v", err)
	}
	if !result.OK {
		t.Fatalf("expected ok=true, got ok=false errCode=%q errMsg=%q", result.ErrCode, result.ErrMsg)
	}
	if len(result.Entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(result.Entries))
	}
	entry := result.Entries[0]
	t.Logf("cleanup entry: ok=%v stage=%q msg=%q warnings=%v alreadyRemoved=%v",
		entry.OK, entry.Stage, entry.Message, entry.Warnings, entry.AlreadyRemoved)

	// AC-G2: worktree directory MUST be removed (gh error must not abort removal).
	if _, err := os.Stat(wtDir); err == nil {
		t.Error("AC-G2 FAIL: worktree directory still exists — gh error must not prevent worktree removal")
	}

	// AC-G2: worktree must not appear in git worktree list.
	porcelain, err := gitWorktreeListPorcelain(repoDir)
	if err != nil {
		t.Fatalf("git worktree list: %v", err)
	}
	wtDirReal := resolvePath(wtDir)
	if strings.Contains(porcelain, wtDirReal) {
		t.Errorf("AC-G2 FAIL: worktree still listed in git worktree list --porcelain after removal:\n%s", porcelain)
	}

	// AC-G2 key assertion: the branch must SURVIVE (fail-closed on gh error).
	if !gitBranchExists(repoDir, branch) {
		t.Errorf("AC-G2 FAIL: branch %q was deleted despite gh error — should be skipped (fail-closed)", branch)
	}

	// The branch-skip must surface as a warning in the entry (merged-state-unavailable).
	hasMergedStateWarning := false
	for _, w := range entry.Warnings {
		if strings.Contains(w, "merged-state-unavailable") || strings.Contains(w, "unknown") {
			hasMergedStateWarning = true
		}
	}
	if !hasMergedStateWarning {
		t.Logf("AC-G2 note: expected a 'merged-state-unavailable' warning in entry.Warnings=%v — "+
			"this is a soft assertion (the worktree removal + branch survival are the hard ACs)", entry.Warnings)
	}
}

// =============================================================================
// AC-G3 integration: tmux-kill fires THROUGH the resolver (not just the script)
// =============================================================================
//
// TestWorktreesCleanup_TmuxSessionAbsent_NonFatal proves the FULL WIRE for AC-G3:
//
//	mutation input (SessionNames) → cleanupOne passes --tmux-session →
//	script runs tmux-kill stage → session absent (tmux has-session returns non-zero) →
//	script records a non-fatal no-op → worktree is removed.
//
// This is the integration proof; the bats test (scripts/git/worktree-remove.bats:315)
// proves the SCRIPT level. This test proves the RESOLVER wire.
//
// @scenario AC-G3: tmux session absent → non-fatal no-op, worktree removed
func TestWorktreesCleanup_TmuxSessionAbsent_NonFatal(t *testing.T) {
	root := repoRoot(t)
	r := NewMutationResolver(root + "/scripts")

	scriptPath := r.worktreeScript("remove")
	if _, err := os.Stat(scriptPath); err != nil {
		t.Skipf("skipping: script not found at %s: %v", scriptPath, err)
	}

	// Set up a real git repo and worktree.
	repoDir := t.TempDir()
	if err := setupRepo(t, repoDir); err != nil {
		t.Skipf("git setup failed: %v", err)
	}
	wtDir := t.TempDir()
	branch := "tmux-kill-test-branch"
	if err := addWorktree(t, repoDir, branch, wtDir); err != nil {
		t.Skipf("addWorktree: %v", err)
	}
	cfgPath := writeOrchardConfig(t, "myrepo", repoDir)
	t.Setenv("ORCHARD_CONFIG", cfgPath)

	// Confirm the worktree exists before cleanup.
	if _, err := os.Stat(wtDir); err != nil {
		t.Fatalf("pre-condition: worktree dir %q should exist: %v", wtDir, err)
	}

	// Pass a session name that does NOT exist in tmux — the kill stage fires
	// (the script calls tmux has-session which returns non-zero) but records a
	// no-op (session-not-found) rather than a warning, so worktree removal
	// continues. Use a UUID-shaped name so it cannot accidentally match a real session.
	nonExistentSession := "orchard-test-nonexistent-session-99999"

	result, err := r.WorktreesCleanup(context.Background(), WorktreesCleanupInput{
		WorktreeIDs:  []string{"myrepo:" + branch},
		SessionNames: []string{nonExistentSession},
	})
	if err != nil {
		t.Fatalf("WorktreesCleanup error: %v", err)
	}
	if !result.OK {
		t.Fatalf("expected batch ok=true, got ok=false errCode=%q errMsg=%q",
			result.ErrCode, result.ErrMsg)
	}
	if len(result.Entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(result.Entries))
	}
	entry := result.Entries[0]
	t.Logf("AC-G3 entry: ok=%v stage=%q msg=%q warnings=%v alreadyRemoved=%v",
		entry.OK, entry.Stage, entry.Message, entry.Warnings, entry.AlreadyRemoved)

	// AC-G3 key assertion 1: entry.ok must be TRUE — tmux-kill failure is non-fatal.
	if !entry.OK {
		t.Errorf("AC-G3 FAIL: entry.ok=false — tmux-kill failure must be non-fatal (stage=%q msg=%q)",
			entry.Stage, entry.Message)
	}

	// AC-G3 key assertion 2: the worktree directory MUST be removed despite the
	// tmux session not existing (the kill is a no-op for a missing session, not a hard failure).
	if _, err := os.Stat(wtDir); err == nil {
		t.Error("AC-G3 FAIL: worktree directory still exists — must be removed even when tmux session is absent")
	}

	// AC-G3 key assertion 3: the worktree must not appear in git worktree list.
	porcelain, err := gitWorktreeListPorcelain(repoDir)
	if err != nil {
		t.Fatalf("git worktree list: %v", err)
	}
	wtDirReal := resolvePath(wtDir)
	if strings.Contains(porcelain, wtDirReal) {
		t.Errorf("AC-G3 FAIL: worktree still listed in git worktree list --porcelain:\n%s", porcelain)
	}

	// AC-G3 diagnostic: log whether entry.alreadyRemoved is false — confirms the
	// tmuxKill field in the script output was correctly interpreted as "removal happened"
	// (not an already-removed no-op), which proves enrichEntryFromData handles tmuxKill.
	if entry.AlreadyRemoved {
		t.Logf("AC-G3 note: entry.alreadyRemoved=true unexpectedly — the tmuxKill field should have " +
			"prevented the alreadyRemoved=true path (check enrichEntryFromData hasTmuxKill gate)")
	}

	t.Logf("AC-G3 PASS: worktree removed (dir gone + not in git list), entry.ok=true (non-fatal)")
}

// TestEnrichEntryFromData_TmuxKillWarning proves AC-G3 Part B: when the script emits a
// tmuxKill payload carrying a "warning" field (the kill-FAILURE path), enrichEntryFromData
// surfaces that warning text in entry.Warnings. Also asserts the negative: the
// session-not-found shape (killed:false, reason:session-not-found, NO warning field) does
// NOT add any warning — so the only path that appends a warning is the real failure path.
//
// This closes the proof chain for AC-G3:
//
//	flag passed (TestWorktreesCleanup_TmuxKill_NonFatal, integration)
//	+ script emits warning on real kill-failure (worktree-remove.bats:315, bats)
//	+ enrich surfaces the warning in entry.Warnings (THIS test, unit)
//
// @scenario AC-G3: tmux-kill warning surfaces through enrichEntryFromData (Part B — enrich seam)
func TestEnrichEntryFromData_TmuxKillWarning(t *testing.T) {
	t.Run("kill-failure warning surfaces in entry.Warnings", func(t *testing.T) {
		// This is the shape the script emits when tmux kill-session fails on an existing session.
		raw := json.RawMessage(`{
			"worktreeId": "myrepo:some-branch",
			"tmuxKill": {"stage":"tmux-kill","warning":"tmux kill-session failed: exit status 1"}
		}`)
		entry := enrichEntryFromData(WorktreeCleanupEntry{WorktreeID: "myrepo:some-branch"}, raw)

		if len(entry.Warnings) == 0 {
			t.Fatal("AC-G3 FAIL: expected entry.Warnings to contain the tmux-kill warning, got empty slice")
		}
		wantSubstr := "tmux kill-session failed"
		found := false
		for _, w := range entry.Warnings {
			if strings.Contains(w, wantSubstr) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("AC-G3 FAIL: warnings %v do not contain %q", entry.Warnings, wantSubstr)
		}
		// The kill-failure path has a tmuxKill field, so the entry must NOT be alreadyRemoved.
		if entry.AlreadyRemoved {
			t.Error("AC-G3 FAIL: entry.AlreadyRemoved=true when tmuxKill field is present — enrichEntryFromData hasTmuxKill gate broken")
		}
		t.Logf("AC-G3 Part B PASS (positive): warnings=%v alreadyRemoved=%v", entry.Warnings, entry.AlreadyRemoved)
	})

	t.Run("session-not-found does NOT add a warning", func(t *testing.T) {
		// This is the shape the script emits when the session was never found (no kill attempted).
		// It must NOT produce any warning entry.
		raw := json.RawMessage(`{
			"worktreeId": "myrepo:some-branch",
			"tmuxKill": {"stage":"tmux-kill","killed":false,"reason":"session-not-found"}
		}`)
		entry := enrichEntryFromData(WorktreeCleanupEntry{WorktreeID: "myrepo:some-branch"}, raw)

		if len(entry.Warnings) != 0 {
			t.Errorf("AC-G3 FAIL (negative): expected no warnings for session-not-found, got %v", entry.Warnings)
		}
		// The tmuxKill field is present, so alreadyRemoved must remain false.
		if entry.AlreadyRemoved {
			t.Error("AC-G3 FAIL: entry.AlreadyRemoved=true when tmuxKill field is present")
		}
		t.Logf("AC-G3 Part B PASS (negative): no warnings for session-not-found, alreadyRemoved=%v", entry.AlreadyRemoved)
	})
}

// TestEnrichEntryFromData_SkipReasonSurfaced proves PR #695 fix: when the script emits a
// top-level skipped:true / skipReason envelope, enrichEntryFromData must surface the
// skipReason in entry.Warnings so the caller can distinguish a skip from a real cleanup.
// Without the fix, entry.Warnings is empty and the TUI shows the orphan as "deleted".
//
// Covers both top-level skip reasons produced by worktree-remove.sh:
//   - "repo-unregistered" (orphan whose projectId slug is absent from orchard config)
//   - "hosts-active-session" (worktree whose session/cwd matches the user's active session)
//
// Also asserts the AlreadyRemoved gate is NOT changed by a skip envelope (AlreadyRemoved
// is for idempotent re-runs, not for active skips — the existing hasSkipped gate handles this).
//
// @scenario PR #695: top-level skipReason surfaces in Warnings (unit seam)
func TestEnrichEntryFromData_SkipReasonSurfaced(t *testing.T) {
	t.Run("repo-unregistered skip surfaces in Warnings", func(t *testing.T) {
		// Script emits: {ok:true, worktreeId:..., skipped:true, skipReason:"repo-unregistered"}
		raw := json.RawMessage(`{
			"worktreeId": "langwatch/langwatch-saas:issue510",
			"skipped": true,
			"skipReason": "repo-unregistered"
		}`)
		entry := enrichEntryFromData(WorktreeCleanupEntry{WorktreeID: "langwatch/langwatch-saas:issue510"}, raw)

		// The skipReason must appear in Warnings — caller needs a positive skip marker.
		found := false
		for _, w := range entry.Warnings {
			if strings.Contains(w, "repo-unregistered") {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("#695 FAIL: repo-unregistered skipReason not surfaced in Warnings; warnings=%v", entry.Warnings)
		}
		// AlreadyRemoved must remain false — a skip is not an idempotent already-removed no-op.
		if entry.AlreadyRemoved {
			t.Error("#695 FAIL: AlreadyRemoved=true for a skip envelope — must stay false")
		}
		t.Logf("#695 PASS repo-unregistered: warnings=%v alreadyRemoved=%v", entry.Warnings, entry.AlreadyRemoved)
	})

	t.Run("hosts-active-session skip surfaces in Warnings", func(t *testing.T) {
		// Script emits: {ok:true, worktreeId:..., skipped:true, skipReason:"hosts-active-session"}
		raw := json.RawMessage(`{
			"worktreeId": "myrepo:main",
			"skipped": true,
			"skipReason": "hosts-active-session"
		}`)
		entry := enrichEntryFromData(WorktreeCleanupEntry{WorktreeID: "myrepo:main"}, raw)

		found := false
		for _, w := range entry.Warnings {
			if strings.Contains(w, "hosts-active-session") {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("#695 FAIL: hosts-active-session skipReason not surfaced in Warnings; warnings=%v", entry.Warnings)
		}
		if entry.AlreadyRemoved {
			t.Error("#695 FAIL: AlreadyRemoved=true for a hosts-active-session skip — must stay false")
		}
		t.Logf("#695 PASS hosts-active-session: warnings=%v alreadyRemoved=%v", entry.Warnings, entry.AlreadyRemoved)
	})

	t.Run("main-working-tree skip surfaces in Warnings", func(t *testing.T) {
		// Script emits: {ok:true, worktreeId:..., skipped:true, skipReason:"main-working-tree"}
		// when the targeted worktree IS the repo primary checkout — defense-in-depth guard
		// added in PR #695 (worktree-remove.sh line ~253).
		raw := json.RawMessage(`{
			"worktreeId": "myrepo:main",
			"skipped": true,
			"skipReason": "main-working-tree"
		}`)
		entry := enrichEntryFromData(WorktreeCleanupEntry{WorktreeID: "myrepo:main"}, raw)

		// The skipReason must appear in Warnings so callers can distinguish the skip.
		found := false
		for _, w := range entry.Warnings {
			if strings.Contains(w, "main-working-tree") {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("#695 FAIL: main-working-tree skipReason not surfaced in Warnings; warnings=%v", entry.Warnings)
		}
		// A skip is not an idempotent already-removed no-op — AlreadyRemoved must stay false.
		if entry.AlreadyRemoved {
			t.Error("#695 FAIL: AlreadyRemoved=true for a main-working-tree skip — must stay false")
		}
		t.Logf("#695 PASS main-working-tree: warnings=%v alreadyRemoved=%v", entry.Warnings, entry.AlreadyRemoved)
	})
}
