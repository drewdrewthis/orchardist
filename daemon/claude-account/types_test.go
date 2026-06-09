package claudeaccount_test

import (
	"errors"
	"testing"

	claudeaccount "github.com/drewdrewthis/orchardist/daemon/claude-account"
)

// TestAccountID_GraphQLID asserts the canonical id format.
// Clients rely on this for Query.node(id) lookups.
func TestAccountID_GraphQLID(t *testing.T) {
	id := claudeaccount.AccountID{HostID: "alpha", Email: "alice@example.com"}
	want := "ClaudeAccount:alpha:alice@example.com"
	if got := id.GraphQLID(); got != want {
		t.Errorf("GraphQLID = %q, want %q", got, want)
	}
}

// TestToolNotInstalledError_IsSentinel asserts errors.Is matches the sentinel
// so resolvers can detect not-installed without string-matching.
func TestToolNotInstalledError_IsSentinel(t *testing.T) {
	err := &claudeaccount.ToolNotInstalledError{Tool: "claude"}
	if !errors.Is(err, claudeaccount.ErrToolNotInstalled) {
		t.Error("errors.Is should match ErrToolNotInstalled sentinel; got false")
	}
}

// TestToolNotInstalledError_MessageNamesTool asserts the rendered message
// identifies the missing tool — callers surface it in CLI / GraphQL errors.
func TestToolNotInstalledError_MessageNamesTool(t *testing.T) {
	err := &claudeaccount.ToolNotInstalledError{Tool: "ccusage"}
	msg := err.Error()
	if msg == "" || !substr(msg, "ccusage") {
		t.Errorf("Error() = %q, want it to mention ccusage", msg)
	}
}

func substr(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
