package resolvers

import (
	"os"
	"path/filepath"
	"strings"
)

// readDefaultBranch returns the default branch of the git project that
// owns the worktree at worktreePath. It reads the HEAD file of the
// project's bare git dir — not the worktree's own HEAD — so it reflects
// the branch the project was cloned on (typically "main" or "master").
//
// Returns ("", false) on any failure: missing .git, unreadable files,
// unexpected format. Callers treat false as "don't skip" — the exclusion
// is a best-effort optimisation, never an error path.
func readDefaultBranch(worktreePath string) (string, bool) {
	gitDir, ok := resolveProjectGitDir(worktreePath)
	if !ok {
		return "", false
	}
	headPath := filepath.Join(gitDir, "HEAD")
	data, err := os.ReadFile(headPath) //nolint:gosec // trusted internal path
	if err != nil {
		return "", false
	}
	line := strings.TrimSpace(string(data))
	// HEAD format for a symbolic ref: "ref: refs/heads/<branch>"
	const prefix = "ref: refs/heads/"
	if !strings.HasPrefix(line, prefix) {
		return "", false
	}
	branch := strings.TrimPrefix(line, prefix)
	if branch == "" {
		return "", false
	}
	return branch, true
}

// resolveProjectGitDir finds the bare git directory for the project that
// owns worktreePath. It handles three layouts:
//
//  1. Ordinary working tree: <workdir>/.git is a directory.
//     → the project git dir IS <workdir>/.git.
//
//  2. Linked worktree: <workdir>/.git is a file containing
//     "gitdir: <abs-path-to-worktree-gitdir>" which points into
//     <project-bare>/.git/worktrees/<name>/. The project's bare git
//     dir is two levels up from the worktrees/<name> entry.
//
//  3. No .git at all: return false.
func resolveProjectGitDir(worktreePath string) (string, bool) {
	candidate := filepath.Join(worktreePath, ".git")
	info, err := os.Stat(candidate)
	if err != nil {
		return "", false
	}
	if info.IsDir() {
		// Ordinary working tree — the project git dir is right here.
		return candidate, true
	}
	// Linked worktree: .git is a file.
	data, err := os.ReadFile(candidate) //nolint:gosec
	if err != nil {
		return "", false
	}
	line := strings.TrimSpace(string(data))
	// Format: "gitdir: /abs/path/to/.git/worktrees/<name>"
	const prefix = "gitdir:"
	if !strings.HasPrefix(line, prefix) {
		return "", false
	}
	worktreeGitDir := strings.TrimSpace(strings.TrimPrefix(line, prefix))
	if !filepath.IsAbs(worktreeGitDir) {
		worktreeGitDir = filepath.Join(worktreePath, worktreeGitDir)
	}
	worktreeGitDir = filepath.Clean(worktreeGitDir)

	// Walk up past "worktrees/<name>" to reach the project's bare git dir.
	// <project-bare>/.git/worktrees/<name> → <project-bare>/.git
	parent := filepath.Dir(filepath.Dir(worktreeGitDir))
	if parent == "" || parent == worktreeGitDir {
		return "", false
	}
	return parent, true
}
