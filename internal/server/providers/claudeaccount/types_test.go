package claudeaccount_test

import (
	"errors"
	"testing"

	"github.com/drewdrewthis/orchardist/internal/server/providers/claudeaccount"
)

// TestAccountID_GraphQLID asserts the canonical id format.
// This is the contract clients rely on for Query.node(id) lookups.
func TestAccountID_GraphQLID(t *testing.T) {
	id := claudeaccount.AccountID{HostID: "alpha", Email: "alice@example.com"}
	if got, want := id.GraphQLID(), "ClaudeAccount:alpha:alice@example.com"; got != want {
		t.Errorf("GraphQLID = %q, want %q", got, want)
	}
}

// TestToolNotInstalledError_IsSentinel asserts errors.Is matches the
// sentinel so resolvers can detect not-installed without string-matching.
func TestToolNotInstalledError_IsSentinel(t *testing.T) {
	err := &claudeaccount.ToolNotInstalledError{Tool: "claude"}
	if !errors.Is(err, claudeaccount.ErrToolNotInstalled) {
		t.Errorf("errors.Is should match the sentinel; got false")
	}
}

// TestToolNotInstalledError_MessageNamesTool asserts the rendered
// message identifies the missing tool — clients render this verbatim
// in CLI / GraphQL error responses.
func TestToolNotInstalledError_MessageNamesTool(t *testing.T) {
	err := &claudeaccount.ToolNotInstalledError{Tool: "ccusage"}
	if got := err.Error(); got == "" || !contains(got, "ccusage") {
		t.Errorf("Error() = %q, want it to mention ccusage", got)
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (func() bool {
		for i := 0; i+len(needle) <= len(haystack); i++ {
			if haystack[i:i+len(needle)] == needle {
				return true
			}
		}
		return false
	})()
}
