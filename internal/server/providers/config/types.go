// Package config implements the orchard `Project` provider.
//
// Per ADR-011 §5.1 a Project is declared in ~/.config/orchard/config.json
// and is read-only from orchard's POV. Mutations are CLI-driven edits to
// the config file; the running daemon reflects them via fsnotify. This
// package owns the file format, the JSON adapter, and the provider that
// surfaces Projects to the GraphQL resolver.
package config

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"strings"
)

// ProjectID is the stable identifier for a project across config edits
// and daemon restarts.
type ProjectID string

// Project is the in-memory representation of a configured project.
// The wire-level GraphQL type lives in internal/server/graphql; this
// type is what the provider stores and what the resolver maps from.
type Project struct {
	ID        ProjectID `json:"id"`
	Directory string    `json:"directory"`
	Name      string    `json:"name"`
}

// File is the on-disk shape of ~/.config/orchard/config.json.
//
// Versioning is explicit so future schema bumps can be detected. v1
// carries only `projects`; later workstreams may add fields. Unknown
// fields are tolerated (json.Unmarshal default behaviour) so older
// daemons can read newer configs without crashing.
type File struct {
	Version  int          `json:"version"`
	Projects []ProjectRow `json:"projects"`
}

// ProjectRow is one entry in `projects`. All three fields are emitted by
// `orchard config add-repo`; `id` is normalised to a slug if blank, and
// `name` defaults to the basename of `directory` if blank.
type ProjectRow struct {
	ID        ProjectID `json:"id"`
	Directory string    `json:"directory"`
	Name      string    `json:"name"`
}

// Normalise fills in missing fields per the documented conventions.
//
//   - If Name is blank, take the basename of Directory.
//   - If ID is blank, slugify Name; fall back to a short hash of
//     Directory when slugification yields the empty string.
//
// The function is pure and idempotent — calling it on an already-
// normalised row leaves the row unchanged.
func (r ProjectRow) Normalise() ProjectRow {
	out := r
	if out.Name == "" {
		out.Name = filepath.Base(out.Directory)
	}
	if out.ID == "" {
		out.ID = ProjectID(slugOrHash(out.Name, out.Directory))
	}
	return out
}

// ToProject lifts a ProjectRow into the in-memory Project type after
// normalisation. The provider calls this on every cache load. ProjectRow
// and Project share the same layout so a struct conversion is sufficient.
func (r ProjectRow) ToProject() Project {
	return Project(r.Normalise())
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
