// Package config implements the orchard `Repo` provider.
//
// Per ADR-015 a Repo is declared in ~/.orchard/config.json and is
// read-only from orchard's POV. Mutations are CLI-driven edits to the
// config file; the running daemon reflects them via fsnotify. This
// package owns the file format, the JSON adapter, and the provider that
// surfaces Repos to the GraphQL resolver.
//
// Schema: per ADR-015 the file has exactly three top-level keys —
// `version`, `repos`, and `peers`. Older shapes (`projects[]`) are not
// supported on read or write.
package config

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"strings"
)

// Lister is the narrow read-side contract the resolvers and the
// request-scoped DataLoader depend on. Defined here (consumer-side) so
// callers don't reach into the full Provider surface.
type Lister interface {
	List(ctx context.Context) ([]Repo, error)
}

// RepoID is the stable identifier for a repo across config edits and
// daemon restarts. Derived from the slug.
type RepoID string

// Repo is the in-memory representation of a configured repo. The
// wire-level GraphQL type lives in internal/server/graphql; this type
// is what the provider stores and what the resolver maps from.
// On-disk serialisation goes through `RepoRow` — Repo itself never
// hits a marshaller, hence no struct tags.
type Repo struct {
	ID   RepoID
	Slug string
	Path string
}

// File is the on-disk shape of ~/.orchard/config.json (ADR-015).
//
// Three top-level keys: `version`, `repos`, `peers`. Unknown fields are
// tolerated on read (json.Unmarshal default behaviour) so older daemons
// can read newer configs without crashing. On write, only these three
// keys are emitted.
type File struct {
	Version int        `json:"version"`
	Repos   []RepoRow  `json:"repos"`
	Peers   []PeerRow  `json:"peers,omitempty"`
}

// RepoRow is one entry in `repos`. Identity is `slug`; display name is
// derived from `path`. The `Remotes` array is preserved verbatim from
// disk for callers that need cross-machine SSH worktree config; the
// daemon itself does not use it.
type RepoRow struct {
	Slug    string        `json:"slug"`
	Path    string        `json:"path"`
	Remotes []RemoteEntry `json:"remotes,omitempty"`
}

// RemoteEntry mirrors the per-repo remote SSH host config used by the
// Rust orchard-tui binary's federation layer. The daemon stores it
// verbatim so writes round-trip; only the Rust side interprets it.
type RemoteEntry struct {
	Name            string `json:"name"`
	Host            string `json:"host"`
	Path            string `json:"path"`
	Shell           string `json:"shell,omitempty"`
	Type            string `json:"type"`
	AllowTransitive bool   `json:"allow_transitive,omitempty"`
}

// PeerRow mirrors a peer entry. The peerproxy provider owns the rich
// type; this struct exists so File can round-trip the array on write.
//
// `omitempty` on `tls` matches `peerproxy.PeerConfig.TLS` so add-repo
// and add-peer round-trip identically — without this, every add-repo
// invocation would re-emit `"tls": false` for peers that were
// originally written without the key.
type PeerRow struct {
	Name    string `json:"name"`
	Address string `json:"address"`
	TLS     bool   `json:"tls,omitempty"`
}

// Normalise fills in missing fields per ADR-015's identity rules.
//
//   - If Slug is blank, derive a fallback identity from Path.
//   - The ID always derives from Slug; the file may carry an empty ID
//     and Normalise will fill it.
//
// The function is pure and idempotent — calling it on an already-
// normalised row leaves the row unchanged.
func (r RepoRow) Normalise() RepoRow {
	out := r
	if out.Slug == "" {
		out.Slug = slugOrHash(filepath.Base(out.Path), out.Path)
	}
	return out
}

// ToRepo lifts a RepoRow into the in-memory Repo type after
// normalisation.
func (r RepoRow) ToRepo() Repo {
	n := r.Normalise()
	return Repo{
		ID:   RepoID(n.Slug),
		Slug: n.Slug,
		Path: n.Path,
	}
}

// DisplayName returns the human-readable label for a repo. Computed
// rather than stored: it's the basename of Path, falling back to the
// repo portion of Slug.
func (r Repo) DisplayName() string {
	if base := filepath.Base(r.Path); base != "" && base != "." && base != "/" {
		return base
	}
	if i := strings.LastIndex(r.Slug, "/"); i >= 0 && i+1 < len(r.Slug) {
		return r.Slug[i+1:]
	}
	return r.Slug
}

// slugOrHash returns a lowercase slug of name, or the first 12 hex chars
// of sha256(directory) when the slug is empty (e.g. when name was a
// non-ASCII path basename that contained no slug-friendly runes).
func slugOrHash(name, directory string) string {
	if s := slug(name); s != "" {
		return s
	}
	sum := sha256.Sum256([]byte(directory))
	return hex.EncodeToString(sum[:])[:12]
}

// slug lowercases name, replaces runs of non-alphanumeric runes with a
// single dash, and trims leading/trailing dashes. Pure ASCII; non-ASCII
// runes are dropped entirely.
func slug(name string) string {
	var b strings.Builder
	prevDash := true // suppress leading dash
	for _, r := range name {
		switch {
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r + ('a' - 'A'))
			prevDash = false
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			prevDash = false
		default:
			if !prevDash {
				b.WriteRune('-')
				prevDash = true
			}
		}
	}
	out := b.String()
	out = strings.TrimRight(out, "-")
	return out
}
