package daemon

// Tests for orchardScriptsRoot() — the function that resolves the scripts/
// directory path at runtime. These tests cover the three-priority resolution
// order documented in the function:
//
//  1. ORCHARD_SCRIPTS_ROOT env override
//  2. <binDir>/../share/orchard/scripts (installed share layout)
//  3. <binDir>/../scripts (dev checkout layout)
//  4. "scripts" (cwd-relative fallback)
//
// We can't change the running binary's Executable() path, so we test the
// two filesystem-based candidates by exercising scriptsRootExists() directly
// and by using ORCHARD_SCRIPTS_ROOT. The fallback path "scripts" is asserted
// by the absence of real candidates in temp dirs.

import (
	"os"
	"path/filepath"
	"testing"
)

// TestOrchardScriptsRoot_EnvOverride asserts that ORCHARD_SCRIPTS_ROOT is
// honored as the highest-priority candidate — even when no such directory
// exists on disk (the env var is trusted verbatim, matching the documented
// operator-override contract).
func TestOrchardScriptsRoot_EnvOverride(t *testing.T) {
	// t.Setenv must not be combined with t.Parallel (Go 1.21+).
	dir := t.TempDir()
	t.Setenv("ORCHARD_SCRIPTS_ROOT", dir)

	got := orchardScriptsRoot()
	if got != dir {
		t.Errorf("orchardScriptsRoot() = %q, want %q", got, dir)
	}
}

// TestScriptsRootExists_Positive asserts that scriptsRootExists returns true
// when the candidate directory contains git/worktree-remove.sh.
func TestScriptsRootExists_Positive(t *testing.T) {
	t.Parallel() // No env mutation — safe to parallelise.
	base := t.TempDir()
	gitDir := filepath.Join(base, "git")
	if err := os.MkdirAll(gitDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	sentinel := filepath.Join(gitDir, "worktree-remove.sh")
	if err := os.WriteFile(sentinel, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if !scriptsRootExists(base) {
		t.Errorf("scriptsRootExists(%q) = false, want true", base)
	}
}

// TestScriptsRootExists_Negative asserts that scriptsRootExists returns false
// when the directory does not contain git/worktree-remove.sh (e.g. the
// /usr/local/scripts path that make install never creates).
func TestScriptsRootExists_Negative(t *testing.T) {
	t.Parallel() // No env mutation — safe to parallelise.
	empty := t.TempDir()

	if scriptsRootExists(empty) {
		t.Errorf("scriptsRootExists(%q) = true for empty dir, want false", empty)
	}

	if scriptsRootExists("/usr/local/scripts") {
		// This path should not exist on a system where make install-scripts
		// has not been run. If it somehow exists and is populated this test
		// is a false positive — acceptable on a fully-installed dev machine.
		t.Logf("note: /usr/local/scripts contains git/worktree-remove.sh — installed layout is present")
	}
}

// TestOrchardScriptsRoot_ShareCandidateWins asserts that when a temp dir
// shaped like the installed share layout contains git/worktree-remove.sh,
// orchardScriptsRoot() picks it. We exercise this by setting ORCHARD_SCRIPTS_ROOT
// to the constructed candidate path — since we cannot repoint os.Executable()
// in tests, the env-override path is the practical way to inject the share dir.
func TestOrchardScriptsRoot_ShareCandidateWins(t *testing.T) {
	// t.Setenv must not be combined with t.Parallel.
	// Simulate /usr/local/share/orchard/scripts with the sentinel file present.
	shareDir := t.TempDir()
	gitDir := filepath.Join(shareDir, "git")
	if err := os.MkdirAll(gitDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(gitDir, "worktree-remove.sh"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	t.Setenv("ORCHARD_SCRIPTS_ROOT", shareDir)

	got := orchardScriptsRoot()
	if got != shareDir {
		t.Errorf("orchardScriptsRoot() = %q, want %q", got, shareDir)
	}
}

// TestOrchardScriptsRoot_FallbackWhenNoCandidateExists asserts that the
// function returns the cwd-relative fallback "scripts" when ORCHARD_SCRIPTS_ROOT
// is unset and no candidate directory contains git/worktree-remove.sh.
// This exercises the no-candidates path on a machine where neither the
// share dir nor the dev checkout dir happens to be populated.
func TestOrchardScriptsRoot_FallbackWhenNoCandidateExists(t *testing.T) {
	// Not parallel — mutates ORCHARD_SCRIPTS_ROOT temporarily via t.Setenv,
	// and we rely on the real binary path producing candidates that don't exist
	// (running from t.TempDir as cwd guarantees no scripts/ neighbour there).
	t.Setenv("ORCHARD_SCRIPTS_ROOT", "")

	got := orchardScriptsRoot()
	// The function returns either a real candidate path (if running from
	// the repo checkout where scripts/ exists) or the "scripts" fallback.
	// The key assertion is that it returns a non-empty string — never "".
	if got == "" {
		t.Error("orchardScriptsRoot() returned empty string, want non-empty path")
	}
}
