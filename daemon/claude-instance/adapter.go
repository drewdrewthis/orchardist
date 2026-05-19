// adapter.go — I/O boundary for the claude-instance domain.
//
// All filesystem reads (jsonl transcript parsing) live here. Pure functions in
// provider.go consume the resulting []Record slices; no I/O in provider.go.
package claudeinstance

import (
	"context"
	"os"
	"path/filepath"
	"strings"
)

// ─── Path helpers ──────────────────────────────────────────────────────────────

// encodeCwd applies claude's project-directory naming convention: every '/'
// AND every '.' in the absolute cwd becomes '-'. Verified empirically against
// ~/.claude/projects on a live host.
func encodeCwd(cwd string) string {
	return strings.NewReplacer("/", "-", ".", "-").Replace(cwd)
}

// encodeCwdPath builds the full path to the jsonl file for a given
// projectsDir, cwd, and sessionUUID.
func encodeCwdPath(projectsDir, cwd, sessionUUID string) string {
	return filepath.Join(projectsDir, encodeCwd(cwd), sessionUUID+".jsonl")
}

// ─── FsSnapshotReader ─────────────────────────────────────────────────────────

// FsSnapshotReader is the production SnapshotReader. Resolves files under
// projectsDir (default ~/.claude/projects) on demand. Satisfies the
// SnapshotReader interface defined in service.go.
type FsSnapshotReader struct {
	projectsDir string
}

// NewFsSnapshotReader constructs a reader rooted at projectsDir. When
// empty, it resolves to ~/.claude/projects. Returns nil when the home
// directory is unresolvable.
func NewFsSnapshotReader(projectsDir string) *FsSnapshotReader {
	if projectsDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil
		}
		projectsDir = filepath.Join(home, ".claude", "projects")
	}
	return &FsSnapshotReader{projectsDir: projectsDir}
}

// ReadSnapshot reads and decodes all non-sidechain records for the given
// cwd+sessionUUID. Returns (nil, false) when the file does not exist.
func (r *FsSnapshotReader) ReadSnapshot(_ context.Context, cwd, sessionUUID string) ([]Record, bool) {
	if r == nil || cwd == "" || sessionUUID == "" {
		return nil, false
	}
	records, err := readRecordsFromPath(r.projectsDir, cwd, sessionUUID)
	if err != nil || records == nil {
		return nil, false
	}
	return records, true
}
