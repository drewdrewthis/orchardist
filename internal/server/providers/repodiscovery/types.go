// Package repodiscovery unifies configured repos with repos auto-discovered
// from tmux pane CWDs and Claude Code conversation CWDs (issue #527).
//
// The package exposes a single [Provider] that implements the
// resolvers.ReposLister contract by composing:
//
//  1. configured: the existing `~/.orchard/config.json repos[]` list,
//     authoritative for slug / path overrides.
//  2. tmux: every tmux pane's `pane_current_path`, walked up to the
//     nearest `.git` parent.
//  3. claudeprojects: every Claude Code conversation's `cwd`, walked up
//     to the nearest `.git` parent.
//
// Dedupe key is the absolute, symlink-resolved path of the repo root
// (the `.git` parent). Configured entries always win on collision; for
// auto-discovered entries the [Provider] derives a slug from the
// basename, suffixing with the parent directory when two basenames
// would otherwise collide.
//
// A short-TTL cache fronts the union — discovery is cheap but not free,
// and resolver-side calls fire on every `workView` query. Phantom
// configured entries (those whose directory has been deleted) are
// dropped at refresh time; the [Provider] logs the drop once per
// refresh so stale fixture rows don't surface as `0 worktrees` ghosts
// in the dashboard.
package repodiscovery

import (
	"context"

	"github.com/drewdrewthis/git-orchard-rs/internal/server/providers/config"
)

// Source returns absolute repo-root paths (the directory containing a
// `.git` entry). The contract is loose on purpose: each adapter feeds
// raw candidate CWDs through [walkToRepoRoot] before returning, so the
// [Provider] receives already-canonical absolute paths.
//
// Roots is called on every discovery refresh; implementations should be
// fast (a single CLI exec or in-memory snapshot read) and non-blocking
// under network failure. Returning an error degrades to "this source
// contributed nothing this tick" — siblings still feed the union.
type Source interface {
	Roots(ctx context.Context) ([]string, error)
}

// ConfiguredLister is the read-side dependency on the existing config
// provider. The package only consumes its in-memory cache, never its
// adapter.
type ConfiguredLister = config.Lister
