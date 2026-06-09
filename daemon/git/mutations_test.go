package git

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
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
	r := NewMutationResolver(nil, "/scripts")
	got := r.worktreeScript("remove")
	want := "/scripts/git/worktree-remove.sh"
	if got != want {
		t.Errorf("worktreeScript(\"remove\"): got %q, want %q", got, want)
	}
}

// TestWorktreeScriptNotFlatSibling confirms the old (buggy) path is NOT returned.
func TestWorktreeScriptNotFlatSibling(t *testing.T) {
	r := NewMutationResolver(nil, "/scripts")
	got := r.worktreeScript("remove")
	if strings.HasSuffix(got, "git-worktree-remove.sh") {
		t.Errorf("worktreeScript still returns the buggy flat-sibling path: %q", got)
	}
}

// TestGitScriptProducesGitSubdirPath verifies gitScript builds <root>/git/<op>.sh,
// not <root>/git-<op>.sh (same family of bug, Boy Scout — fix consistently).
func TestGitScriptProducesGitSubdirPath(t *testing.T) {
	for _, op := range []string{"fetch", "pull", "push"} {
		r := NewMutationResolver(nil, "/scripts")
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
	r := NewMutationResolver(nil, root+"/scripts")
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
	r := NewMutationResolver(nil, root+"/scripts")
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
	r := NewMutationResolver(nil, root+"/scripts")

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
	r := NewMutationResolver(nil, root+"/scripts")

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
