package gh

import (
	"bufio"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// ReadOriginURL reads `.git/config` for a working tree and returns the
// `origin` remote's URL. Mirrors the git provider's policy of reading
// the on-disk layout directly rather than shelling out to git.
//
// Returns errNoOriginRemote when the file exists but no `[remote
// "origin"]` block has a `url =` entry; errNoGitDir when there is no
// `.git` at the path; other I/O errors propagate.
func ReadOriginURL(workdir string) (string, error) {
	gitDir, err := resolveGitDirForWorktree(workdir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", errNoGitDir
		}
		return "", err
	}
	cfgPath := filepath.Join(gitDir, "config")
	file, err := os.Open(cfgPath) //nolint:gosec // trusted internal path
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", errNoOriginRemote
		}
		return "", err
	}
	defer func() { _ = file.Close() }()

	scanner := bufio.NewScanner(file)
	inOrigin := false
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") {
			inOrigin = strings.EqualFold(line, `[remote "origin"]`)
			continue
		}
		if !inOrigin {
			continue
		}
		// Lines look like `url = https://...` or `url=git@...`.
		idx := strings.IndexByte(line, '=')
		if idx <= 0 {
			continue
		}
		key := strings.TrimSpace(line[:idx])
		val := strings.TrimSpace(line[idx+1:])
		if strings.EqualFold(key, "url") && val != "" {
			return val, nil
		}
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	return "", errNoOriginRemote
}

// ParseGitHubURL extracts the owner and repo name from a GitHub
// remote URL. Handles the three common forms:
//
//   - https://github.com/owner/repo.git
//   - git@github.com:owner/repo.git
//   - ssh://git@github.com/owner/repo.git
//
// Returns ok=false for any URL that is not GitHub (or GHES — out of
// scope for v1). The `.git` suffix is stripped.
func ParseGitHubURL(raw string) (owner, name string, ok bool) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", "", false
	}

	// SSH shorthand: git@github.com:owner/repo[.git]
	if strings.HasPrefix(s, "git@github.com:") {
		s = strings.TrimPrefix(s, "git@github.com:")
		return splitOwnerName(stripGit(s))
	}

	// ssh:// or https:// — strip scheme + host.
	for _, prefix := range []string{
		"ssh://git@github.com/",
		"https://github.com/",
		"http://github.com/",
		"git://github.com/",
	} {
		if strings.HasPrefix(s, prefix) {
			s = strings.TrimPrefix(s, prefix)
			return splitOwnerName(stripGit(s))
		}
	}
	return "", "", false
}

func stripGit(s string) string {
	return strings.TrimSuffix(strings.TrimSpace(s), ".git")
}

func splitOwnerName(s string) (string, string, bool) {
	parts := strings.SplitN(s, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	// owner/name should not have further slashes; reject "owner/sub/name".
	if strings.Contains(parts[1], "/") {
		return "", "", false
	}
	return parts[0], parts[1], true
}

// resolveGitDirForWorktree mirrors the git provider's logic so this
// package doesn't need to depend on it. Handles both `.git` as a
// directory and as a file pointing at the actual git directory
// (linked-worktree case).
func resolveGitDirForWorktree(workdir string) (string, error) {
	candidate := filepath.Join(workdir, ".git")
	info, err := os.Stat(candidate)
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		return candidate, nil
	}
	body, err := os.ReadFile(candidate) //nolint:gosec // trusted internal path
	if err != nil {
		return "", err
	}
	gd := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(string(body)), "gitdir:"))
	if gd == "" {
		return "", os.ErrNotExist
	}
	if !filepath.IsAbs(gd) {
		gd = filepath.Join(workdir, gd)
	}
	return filepath.Clean(gd), nil
}
