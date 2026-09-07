package main

import (
	"strings"
	"testing"
)

const slowLeadFixture = `{"data":{
  "claudeInstances":[
    {"pane":"%1","session":{"issueNumber":844,"prNumber":840,"prUrl":"https://github.com/drewdrewthis/orchardist/pull/840","gitBranch":"feat/a","cwd":"/ws/a"}}
  ],
  "tmuxSessions":[{"name":"work-a","worktree":"/ws/a","branch":"feat/a"}],
  "tmuxPanes":[{"key":"%1","session":"work-a"}]
}}`

// Only one session, so the aliased enrichment query uses index 0.
const slowEnrichFixture = `{"data":{
  "i0":{"number":844,"title":"the issue title"},
  "p0":{"number":840,"state":"OPEN"},
  "q0":{"path":"/ws/a"}
}}`

// AC6: the slow lane enriches a row via issue/pullRequest/paneForBranch. The
// enriched row's issue number and title match the fixture; the PR is attached
// with its verdict marked unknown (renders "—"); worktree drift is unknown.
func TestSupergraphSlowLaneEnriches(t *testing.T) {
	fakeSupergraph(t, func(body string) string {
		if strings.Contains(body, "issue(key:") {
			return slowEnrichFixture
		}
		return slowLeadFixture
	}, healthAllOK)

	msg, ok := fetchSlowSupergraph().(slowDataMsg)
	if !ok {
		t.Fatalf("want slowDataMsg, got %T", msg)
	}
	if msg.err != nil {
		t.Fatalf("slow lane error: %v", msg.err)
	}
	w, ok := msg.wtBySession["work-a"]
	if !ok {
		t.Fatal("no worktree info for work-a")
	}
	if w.Issue == nil || w.Issue.Number != 844 || w.Issue.Title != "the issue title" {
		t.Fatalf("issue = %+v, want #844 'the issue title'", w.Issue)
	}
	if w.PR == nil || w.PR.Number != 840 || !w.PR.unknown {
		t.Fatalf("pr = %+v, want #840 with unknown verdict", w.PR)
	}
	if !w.DriftUnknown {
		t.Error("worktree drift should be unknown on supergraph")
	}
	if msg.repoBySess["work-a"] != "drewdrewthis/orchardist" {
		t.Errorf("repo = %q, want drewdrewthis/orchardist", msg.repoBySess["work-a"])
	}

	// the actual surface: the row after the join carries the issue
	m := &model{
		rows:        []row{{session: "work-a"}},
		wtBySession: msg.wtBySession,
		repoBySess:  msg.repoBySess,
		wtByPath:    msg.wtByPath,
		repoByPath:  msg.repoByPath,
	}
	m.join()
	if m.rows[0].issueNum != 844 || m.rows[0].issueTitle != "the issue title" {
		t.Errorf("joined row = #%d %q, want #844 'the issue title'",
			m.rows[0].issueNum, m.rows[0].issueTitle)
	}
	if !m.rows[0].driftUnknown {
		t.Error("join did not carry driftUnknown to the row")
	}
}

// ownerRepoFromURL is the only supergraph source for a worktree's repo
// identity, parsed from the session's PR URL.
func TestOwnerRepoFromURL(t *testing.T) {
	cases := []struct {
		url, owner, repo string
	}{
		{"https://github.com/drewdrewthis/orchardist/pull/840", "drewdrewthis", "orchardist"},
		{"https://github.com/a/b/issues/5", "a", "b"},
		{"not a url", "", ""},
		{"https://github.com/onlyowner", "", ""},
	}
	for _, c := range cases {
		o, r := ownerRepoFromURL(c.url)
		if o != c.owner || r != c.repo {
			t.Errorf("ownerRepoFromURL(%q) = %q/%q, want %q/%q", c.url, o, r, c.owner, c.repo)
		}
	}
}
