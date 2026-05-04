package claudeaccount

import (
	"errors"
	"fmt"
	"time"
)

// AccountID uniquely identifies a ClaudeAccount across hosts. v1 only
// enumerates the local host, but the host-qualified shape lets
// federated daemons (Workstream F) merge tables without rekeying.
//
// HostID is a free-form string in v1 — typically the local machine's
// id (e.g. /etc/machine-id on Linux, IOPlatformUUID on macOS). Tests
// use a stable fixture (e.g. "test-host"). Email is whatever
// `claude auth status` reports for the active session — empty when no
// session is signed in.
type AccountID struct {
	HostID string
	Email  string
}

// GraphQLID returns the stable orchard id for a ClaudeAccount node.
// Mirrors other Node implementers (e.g. "Host:<machineId>") so the
// Query.node(id) lookup is uniform across types.
func (id AccountID) GraphQLID() string {
	return fmt.Sprintf("ClaudeAccount:%s:%s", id.HostID, id.Email)
}

// Account is the in-memory representation of a ClaudeAccount as the
// provider sees it. It is what the cache holds and what the resolver
// maps onto graphql.ClaudeAccount.
//
// QuotaUsed, QuotaCap, and QuotaResetsAt are pointers because each is
// independently nullable: ccusage may know one value but not another
// (e.g. used > 0 but cap unknown when no rate limit has been applied
// yet for a fresh install).
//
// **doc-as-code**: QuotaEstimated is a hard-coded `true` for the
// lifetime of v1. The convention is: `quotaEstimated = true` whenever
// the quota numbers came from `ccusage` rather than a first-party
// Anthropic API. ccusage is the only source v1 supports, so the field
// is wired to `true` at construction time. When a first-party API
// becomes available, the adapter populates QuotaEstimated based on
// which source actually answered.
type Account struct {
	ID             AccountID
	QuotaUsed      *float64
	QuotaCap       *float64
	QuotaEstimated bool
	QuotaResetsAt  *time.Time
}

// ErrToolNotInstalled signals that the underlying CLI (`claude` or
// `ccusage`) is not on PATH. The provider propagates the error
// unchanged; the resolver wraps it as a per-field GraphQL error.
//
// Use errors.Is(err, ErrToolNotInstalled) at the resolver layer to
// detect the not-installed case without string-matching.
var ErrToolNotInstalled = errors.New("claudeaccount: required CLI not installed")

// ToolNotInstalledError carries which tool was missing alongside the
// sentinel ErrToolNotInstalled. The Tool field is one of "claude" or
// "ccusage". Marshalling to JSON intentionally exposes only the tool
// name — never the env-PATH that resolved or did not resolve.
type ToolNotInstalledError struct {
	Tool string
}

// Error renders the missing-tool message. Stable wording so the
// resolver can compose it into the GraphQL error body without losing
// the tool identity.
func (e *ToolNotInstalledError) Error() string {
	return fmt.Sprintf("claudeaccount: %s CLI not installed (run `which %s` to verify)", e.Tool, e.Tool)
}

// Unwrap chains to the sentinel so callers can match with errors.Is.
func (e *ToolNotInstalledError) Unwrap() error {
	return ErrToolNotInstalled
}
