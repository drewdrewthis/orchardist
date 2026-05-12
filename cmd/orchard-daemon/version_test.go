// AC1 — Scenario 1 & 2: daemon version baked at release time via -ldflags.
//
// Scenario 1: go build with -ldflags "-X main.version=1.2.3" → binary
// prints "1.2.3" when run with --version.
//
// Scenario 2: go build without -ldflags → binary prints "dev" (the
// package-level default declared in main.go) when run with --version.
package main_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestVersionBaked_LdflagsInjectsSemver covers AC1 Scenario 1:
// make daemon VERSION=1.2.3 bakes the semver via -ldflags.
func TestVersionBaked_LdflagsInjectsSemver(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping binary build test in short mode")
	}

	bin := buildDaemon(t, "-X main.version=1.2.3")

	out := runVersion(t, bin)
	if !strings.Contains(out, "1.2.3") {
		t.Errorf("--version output = %q, want it to contain 1.2.3", out)
	}
}

// TestVersionBaked_DefaultIsDevWithoutLdflags covers AC1 Scenario 2:
// go build without -ldflags keeps "dev" as the version string.
func TestVersionBaked_DefaultIsDevWithoutLdflags(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping binary build test in short mode")
	}

	bin := buildDaemon(t, "")

	out := runVersion(t, bin)
	if !strings.Contains(out, "dev") {
		t.Errorf("--version output = %q, want it to contain dev", out)
	}
}

// buildDaemon compiles cmd/orchard-daemon into a temp dir and returns the
// binary path. ldflags is the raw value passed to -ldflags; empty string
// omits the flag entirely (plain go build).
func buildDaemon(t *testing.T, ldflags string) string {
	t.Helper()

	dir := t.TempDir()
	bin := filepath.Join(dir, "orchard-daemon")

	repoRoot := findRepoRoot(t)
	args := []string{"build", "-o", bin}
	if ldflags != "" {
		args = append(args, "-ldflags", ldflags)
	}
	args = append(args, "./cmd/orchard-daemon")

	cmd := exec.Command("go", args...)
	cmd.Dir = repoRoot
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build failed: %v\n%s", err, out)
	}
	return bin
}

// runVersion executes the binary with --version and returns stdout+stderr.
func runVersion(t *testing.T, bin string) string {
	t.Helper()
	cmd := exec.Command(bin, "--version")
	out, err := cmd.CombinedOutput()
	if err != nil {
		// cobra's --version exits 0; any exit error is unexpected.
		t.Fatalf("%s --version: %v\n%s", bin, err, out)
	}
	return string(out)
}

// findRepoRoot walks up from the test file's directory looking for go.mod.
func findRepoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found walking up from", dir)
		}
		dir = parent
	}
}
