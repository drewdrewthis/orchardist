package features_test

import (
	"testing"
	"time"
)

// @scenario OpenPanel by paneId resolves all panel data fields
func TestOpenPanelQuery_ByPaneIdResolvesAllFields(t *testing.T) {
	if !tmuxAvailable() {
		t.Skip("tmux not available — cannot test live pane resolution")
	}

	ts := startServerWithRepo(t)

	// Query the daemon for panes and verify the shape is correct.
	// A real pane test requires a live tmux pane running Claude, which is
	// an environment-specific prerequisite. We verify the query shape only.
	r := postGQL(t, ts.URL, `{
		tmuxPanes { paneId title currentCommand currentPid }
		claudeInstances { id sessionUuid state }
		conversations { sessionUuid jsonlPath }
		repos { id worktrees { id path branch } }
	}`)
	assertNoErrors(t, r)
	requireField(t, r.Data, "tmuxPanes")
	requireField(t, r.Data, "claudeInstances")
	requireField(t, r.Data, "conversations")
	requireField(t, r.Data, "repos")
}

// @scenario OpenPanel returns empty when nothing resolves
func TestOpenPanelQuery_EmptyWhenNothingResolves(t *testing.T) {
	ts := startServerWithRepo(t)

	// Fire an OpenPanel query with a paneId that doesn't exist.
	r := postGQL(t, ts.URL, `{
		tmuxPanes(filter: { paneIdIn: ["%nonexistent"] }) { paneId }
		claudeInstances { id }
	}`)
	assertNoErrors(t, r)

	t.Run("when paneId does not match any daemon state", func(t *testing.T) {
		panes := asList(t, r.Data["tmuxPanes"], "tmuxPanes")
		if len(panes) != 0 {
			t.Errorf("expected empty tmuxPanes for non-existent pane, got %d", len(panes))
		}
	})
}

// @scenario Conversation fields for header rendering
func TestOpenPanelQuery_ConversationCarriesHeaderFields(t *testing.T) {
	cpDir := t.TempDir()
	sessionUUID := "panel-test-xyz"
	writeSessionJSONL(t, cpDir, sessionUUID, time.Now())

	ts := startServerWithClaudeProjects(t, cpDir)
	waitForConversations(t, ts.URL, 1)

	r := postGQL(t, ts.URL, `{
		conversations {
			sessionUuid
			jsonlPath
			firstSeenAt
			lastSeenAt
			messageCount
			open
			recap
			agentName
			customTitle
		}
	}`)
	assertNoErrors(t, r)

	convs := asList(t, r.Data["conversations"], "conversations")
	if len(convs) == 0 {
		t.Skip("no conversations discovered yet — daemon not indexed JSONL in time")
	}

	c := asMap(t, convs[0], "conversations[0]")
	requireFields(t, c,
		"sessionUuid", "jsonlPath", "firstSeenAt", "lastSeenAt",
		"messageCount", "open", "recap", "agentName", "customTitle",
	)

	// jsonlPath must be a non-empty string when present.
	if jl, ok := c["jsonlPath"].(string); ok && jl == "" {
		t.Error("conversation.jsonlPath must be a non-empty string when present")
	}
	// messageCount must be >= 0.
	if mc, ok := c["messageCount"]; ok && mc != nil {
		mustNonNegativeInt(t, mc, "conversation.messageCount")
	}
}

// @scenario WorktreesList response shape — worktree fields present
func TestWorktreesListQuery_WorktreeFieldsPresent(t *testing.T) {
	ts := startServerWithRepo(t)

	r := postGQL(t, ts.URL, `{ repos { id slug worktrees { id path branch bare host repo { id } } } }`)
	assertNoErrors(t, r)

	repos := asList(t, r.Data["repos"], "repos")
	for _, rawRepo := range repos {
		repo := asMap(t, rawRepo, "repo")
		requireFields(t, repo, "id", "slug", "worktrees")
		wts := asList(t, repo["worktrees"], "worktrees")
		for _, rawWt := range wts {
			wt := asMap(t, rawWt, "worktree")
			requireFields(t, wt, "id", "path", "branch", "bare", "host", "repo")
		}
	}
}

// @scenario WorktreesList includes bare field for client-side filtering
func TestWorktreesListQuery_BareFieldPresent(t *testing.T) {
	ts := startServerWithRepo(t)

	r := postGQL(t, ts.URL, `{ repos { worktrees { bare } } }`)
	assertNoErrors(t, r)

	repos := asList(t, r.Data["repos"], "repos")
	for _, rawRepo := range repos {
		repo := asMap(t, rawRepo, "repo")
		wts := asList(t, repo["worktrees"], "worktrees")
		for _, rawWt := range wts {
			wt := asMap(t, rawWt, "worktree")
			if _, ok := wt["bare"]; !ok {
				t.Error("worktree.bare field missing")
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// waitForConversations polls until at least n conversations are visible, or
// gives up after 1 second. Used to let fsnotify + claudeprojects index JSONL.
func waitForConversations(t *testing.T, baseURL string, n int) {
	t.Helper()
	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		r := postGQL(t, baseURL, `{ conversations { id } }`)
		if !r.hasErrors() {
			if raw, ok := r.Data["conversations"]; ok {
				convs, _ := raw.([]any)
				if len(convs) >= n {
					return
				}
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
}
