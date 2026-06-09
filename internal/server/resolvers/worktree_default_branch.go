package resolvers

import (
	"os"
	"path/filepath"
	"strings"

	gh "github.com/drewdrewthis/orchardist/internal/server/providers/gh"
)

// readDefaultBranch returns the default branch of the git project that
// owns the worktree at worktreePath. It reads the HEAD file of the
// project's bare git dir — not the worktree's own HEAD — so it reflects
// the branch the project was cloned on (typically "main" or "master").
//
// Returns ("", false) on any failure: missing .git, unreadable files,
// unexpected format. Callers treat false as "don't skip" — the exclusion
// is a best-effort optimisation, never an error path.
//
// Project gitdir resolution is delegated to gh.ResolveGitDirForWorktree
// so this package does not duplicate the linked-worktree commondir-follow
// logic. Two reasons to change → one place to change.
func readDefaultBranch(worktreePath string) (string, bool) {
	gitDir, err := gh.ResolveGitDirForWorktree(worktreePath)
	if err != nil {
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
