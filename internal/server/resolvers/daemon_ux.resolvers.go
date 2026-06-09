// Helpers shared by the daemon-UX resolvers (#469): SchemaSDL, WorkView,
// DaemonState, and the per-type subscriptions (tmuxSessionsChanged,
// pullRequestChanged, runChanged, worktreeChanged).
//
// The resolver method receivers themselves live in schema.resolvers.go (gqlgen
// owns that file). Only constants and helper functions live here so gqlgen's
// codegen doesn't see duplicate method declarations.

package resolvers

import (
	"strings"
	"time"

	"github.com/drewdrewthis/orchardist/internal/server/adapter"
	gitprovider "github.com/drewdrewthis/orchardist/internal/server/providers/git"
)

// metaProviderWorkView labels the Meta envelope returned alongside the
// composite WorkView. Stable so clients can switch on it.
const metaProviderWorkView = "workView"

// nowRFC3339 returns the current wall-clock time in RFC3339 form. Wrapped so tests can monkey-patch in the future if needed.
func nowRFC3339() *string {
	t := time.Now().UTC().Format(time.RFC3339)
	return &t
}

// splitRepo splits an "owner/name" repo coordinate. Returns ok=false on malformed input (missing slash, empty parts).
func splitRepo(repo string) (owner, name string, ok bool) {
	idx := strings.Index(repo, "/")
	if idx <= 0 || idx == len(repo)-1 {
		return "", "", false
	}
	return repo[:idx], repo[idx+1:], true
}

// worktreeEventMatchesProject is a best-effort filter that decides whether a git invalidation belongs to the requested project. The git provider keys events on "<projectID>:<worktreeName>", so the project id is the prefix.
func worktreeEventMatchesProject(ev adapter.InvalidationEvent[gitprovider.WorktreeID], project string) bool {
	key := string(ev.Key)
	if key == "" || project == "" {
		return false
	}
	prefix := project + ":"
	return strings.HasPrefix(key, prefix) || key == project
}
