// resolver_meta.go — resolver helpers for the Meta type (R6: one file per
// GraphQL type). Meta is owned by daemon-meta and used by other domains to
// surface provenance envelopes alongside their list fields.
package daemonmeta

import "time"

// BuildMeta constructs a Meta envelope for a provider, recording the current
// time as the successful fetch timestamp. Call this in the resolver of any
// list field whose provider can fail.
//
// Used by other domains that need a Meta envelope; they import this package.
func BuildMeta(provider string) Meta {
	t := time.Now().UTC().Format(time.RFC3339)
	return Meta{
		Provider:              provider,
		LastSuccessfulFetchAt: &t,
	}
}

// BuildMetaWithFailure constructs a Meta envelope recording a provider failure.
// LastSuccessfulFetchAt is nil to signal no successful fetch was possible.
func BuildMetaWithFailure(provider, reason string) Meta {
	return Meta{
		Provider:      provider,
		FailureReason: &reason,
	}
}

// Meta is the domain model for the Meta provenance envelope.
// Other domains return Meta alongside list fields to distinguish
// "valid empty" from "data unavailable" (#469 F1).
type Meta struct {
	// Provider is the stable label (e.g. "tmux", "git", "gh").
	Provider string
	// LastSuccessfulFetchAt is the RFC3339 timestamp of the last
	// successful refresh; nil when no successful refresh has happened.
	LastSuccessfulFetchAt *string
	// FailureReason is a human-readable failure reason; nil when healthy.
	FailureReason *string
}
