// Package claudeaccount implements the ClaudeAccount domain — orchard's
// reflection of the local `claude` CLI's auth subject and the quota numbers
// `ccusage` reports for that subject.
//
// Per the domain README and S16a/S16b:
//   - Typed core: Query.claudeAccounts — cached, loader-batched.
//   - Pass-through: Query.claudeCli(tool, args) — top-level only, 30s timeout,
//     concurrency cap 4, not cached, not subscribable.
//
// PII: the adapter MUST NOT log raw stdout from `claude auth status` — it
// contains the user's email.
package claudeaccount

import (
	"errors"
	"fmt"
	"time"
)

// AccountID uniquely identifies a ClaudeAccount across hosts.
// HostID is typically the local machine UUID. Email is whatever
// `claude auth status` reports for the active session.
type AccountID struct {
	HostID string
	Email  string
}

// GraphQLID returns the stable orchard id for a ClaudeAccount node.
// Format: "ClaudeAccount:<hostID>:<email>"
func (id AccountID) GraphQLID() string {
	return fmt.Sprintf("ClaudeAccount:%s:%s", id.HostID, id.Email)
}

// Account is the in-memory representation of a ClaudeAccount.
//
// QuotaUsed, QuotaCap, and QuotaResetsAt are pointers because each is
// independently nullable per S5: ccusage may know one but not another.
// QuotaEstimated is always true in v1 (ccusage is the only source).
type Account struct {
	ID             AccountID
	QuotaUsed      *float64
	QuotaCap       *float64
	QuotaEstimated bool
	QuotaResetsAt  *time.Time
}

// ErrToolNotInstalled signals that the underlying CLI (`claude` or `ccusage`)
// is not on PATH. Use errors.Is(err, ErrToolNotInstalled) at the resolver
// layer to detect not-installed without string-matching.
var ErrToolNotInstalled = errors.New("claudeaccount: required CLI not installed")

// ToolNotInstalledError carries which tool was missing alongside the
// sentinel ErrToolNotInstalled. Tool is one of "claude" or "ccusage".
type ToolNotInstalledError struct {
	Tool string
}

// Error renders the missing-tool message. Wording is stable so the resolver
// can include it verbatim in the GraphQL error body.
func (e *ToolNotInstalledError) Error() string {
	return fmt.Sprintf("claudeaccount: %s CLI not installed (run `which %s` to verify)", e.Tool, e.Tool)
}

// Unwrap chains to the sentinel so callers can match with errors.Is.
func (e *ToolNotInstalledError) Unwrap() error {
	return ErrToolNotInstalled
}
