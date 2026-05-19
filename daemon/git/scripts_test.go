package git

import (
	"encoding/json"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// scriptsDir returns the absolute path to the scripts/ directory.
// This file lives at daemon/git/scripts_test.go within the worktree root.
// scripts/ is at ../../scripts/ relative to daemon/git/.
func scriptsDir(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not determine test file path")
	}
	// file = <worktree-root>/daemon/git/scripts_test.go
	// scripts/ = <worktree-root>/scripts/
	worktreeRoot := filepath.Join(filepath.Dir(file), "..", "..")
	return filepath.Clean(filepath.Join(worktreeRoot, "scripts"))
}

// runScript runs a bash script with args and returns (stdout, exitCode).
func runScript(t *testing.T, script string, args ...string) ([]byte, int) {
	t.Helper()
	cmdArgs := append([]string{filepath.Join(scriptsDir(t), script)}, args...)
	cmd := exec.Command("bash", cmdArgs...)
	out, err := cmd.Output()
	exitCode := 0
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			exitCode = ee.ExitCode()
			// Use stderr as output when stdout is empty.
			if len(out) == 0 {
				out = ee.Stderr
			}
		} else {
			t.Fatalf("exec error: %v", err)
		}
	}
	return out, exitCode
}

// assertL2Envelope verifies the L2 {ok, data?, error?} envelope shape (T2).
func assertL2Envelope(t *testing.T, raw []byte, expectOK bool) map[string]interface{} {
	t.Helper()
	var env map[string]interface{}
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("envelope is not valid JSON: %v\nraw: %s", err, string(raw))
	}
	ok, hasOK := env["ok"].(bool)
	if !hasOK {
		t.Fatalf("envelope missing 'ok' field: %s", string(raw))
	}
	if ok != expectOK {
		t.Errorf("expected ok=%v, got ok=%v\nenvelope: %s", expectOK, ok, string(raw))
	}
	if expectOK {
		// ok:true MUST have data field (may be null per L2).
		if _, hasData := env["data"]; !hasData {
			t.Errorf("ok:true envelope missing 'data': %s", string(raw))
		}
	} else {
		// ok:false MUST have error.code and error.message.
		errObj, hasErr := env["error"].(map[string]interface{})
		if !hasErr {
			t.Fatalf("ok:false envelope missing 'error': %s", string(raw))
		}
		if _, hasCode := errObj["code"]; !hasCode {
			t.Errorf("error object missing 'code': %s", string(raw))
		}
		if _, hasMsg := errObj["message"]; !hasMsg {
			t.Errorf("error object missing 'message': %s", string(raw))
		}
	}
	return env
}

// TestGitWorktreeCreateEnvelopeFailureMissingRepo verifies that calling
// git-worktree-create.sh --json with a missing repo returns ok:false with
// the correct L2 error envelope (T2: failure path).
func TestGitWorktreeCreateEnvelopeFailureMissingRepo(t *testing.T) {
	out, exitCode := runScript(t, "git-worktree-create.sh",
		"--repo", "nonexistent-repo",
		"--branch", "test-branch",
		"--json",
	)
	if exitCode == 0 {
		t.Errorf("expected non-zero exit on failure, got 0")
	}
	assertL2Envelope(t, out, false)
}

// TestGitWorktreeCreateEnvelopeFailureMissingBranch verifies the branch
// validation failure path (T2).
func TestGitWorktreeCreateEnvelopeFailureMissingBranch(t *testing.T) {
	out, exitCode := runScript(t, "git-worktree-create.sh",
		"--repo", "some-repo",
		// no --branch
		"--json",
	)
	if exitCode == 0 {
		t.Errorf("expected non-zero exit on missing branch, got 0")
	}
	assertL2Envelope(t, out, false)
}

// TestGitWorktreeRemoveEnvelopeFailureMissingID verifies remove failure path (T2).
func TestGitWorktreeRemoveEnvelopeFailureMissingID(t *testing.T) {
	out, exitCode := runScript(t, "git-worktree-remove.sh",
		// no --worktree-id
		"--json",
	)
	if exitCode == 0 {
		t.Errorf("expected non-zero exit on missing worktree-id, got 0")
	}
	assertL2Envelope(t, out, false)
}

// TestGitWorktreeMoveEnvelopeFailureMissingNewPath verifies move failure (T2).
func TestGitWorktreeMoveEnvelopeFailureMissingNewPath(t *testing.T) {
	out, exitCode := runScript(t, "git-worktree-move.sh",
		"--worktree-id", "proj:main",
		// no --new-path
		"--json",
	)
	if exitCode == 0 {
		t.Errorf("expected non-zero exit on missing new-path, got 0")
	}
	assertL2Envelope(t, out, false)
}

// TestGitFetchEnvelopeFailureMissingID verifies fetch failure path (T2).
func TestGitFetchEnvelopeFailureMissingID(t *testing.T) {
	out, exitCode := runScript(t, "git-fetch.sh",
		// no --worktree-id
		"--json",
	)
	if exitCode == 0 {
		t.Errorf("expected non-zero exit on missing worktree-id, got 0")
	}
	assertL2Envelope(t, out, false)
}

// TestGitPullEnvelopeFailureMissingID verifies pull failure path (T2).
func TestGitPullEnvelopeFailureMissingID(t *testing.T) {
	out, exitCode := runScript(t, "git-pull.sh",
		// no --worktree-id
		"--json",
	)
	if exitCode == 0 {
		t.Errorf("expected non-zero exit on missing worktree-id, got 0")
	}
	assertL2Envelope(t, out, false)
}

// TestGitPushEnvelopeFailureMissingID verifies push failure path (T2).
func TestGitPushEnvelopeFailureMissingID(t *testing.T) {
	out, exitCode := runScript(t, "git-push.sh",
		// no --worktree-id
		"--json",
	)
	if exitCode == 0 {
		t.Errorf("expected non-zero exit on missing worktree-id, got 0")
	}
	assertL2Envelope(t, out, false)
}
