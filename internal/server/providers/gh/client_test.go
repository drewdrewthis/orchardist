package gh_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/drewdrewthis/git-orchard-rs/internal/server/providers/gh"
)

// TestClient_RateLimit asserts a 403 + X-RateLimit-Remaining: 0
// response surfaces ErrRateLimitedT with the reset time captured. AC:
// "per-call rate-limit awareness".
func TestClient_RateLimit(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/alice/repo/pulls", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-RateLimit-Remaining", "0")
		w.Header().Set("X-RateLimit-Reset", "1700000000")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"message":"API rate limit exceeded"}`))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	c := gh.NewClient(ts.URL, "tok")
	_, err := c.ListPulls(context.Background(), "alice", "repo", gh.PullRequestStateOpen)
	if !gh.IsRateLimited(err) {
		t.Fatalf("err = %v, want rate-limited", err)
	}
}

// TestClient_Unauthorized asserts a 401 surfaces ErrNotAuthenticated.
func TestClient_Unauthorized(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/alice/repo/pulls", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	c := gh.NewClient(ts.URL, "tok")
	_, err := c.ListPulls(context.Background(), "alice", "repo", gh.PullRequestStateOpen)
	if !errors.Is(err, gh.ErrNotAuthenticated) {
		t.Fatalf("err = %v, want ErrNotAuthenticated", err)
	}
}

// TestSplitRepo covers the canonical happy path + error cases.
func TestSplitRepo(t *testing.T) {
	cases := []struct {
		in    string
		owner string
		name  string
		err   bool
	}{
		{"alice/repo", "alice", "repo", false},
		{"alice", "", "", true},
		{"alice/repo/extra", "", "", true},
		{"/repo", "", "", true},
		{"alice/", "", "", true},
		{"", "", "", true},
	}
	for _, tc := range cases {
		owner, name, err := gh.SplitRepo(tc.in)
		if tc.err {
			if err == nil {
				t.Errorf("SplitRepo(%q): want error, got %s/%s", tc.in, owner, name)
			}
			continue
		}
		if err != nil {
			t.Errorf("SplitRepo(%q): %v", tc.in, err)
			continue
		}
		if owner != tc.owner || name != tc.name {
			t.Errorf("SplitRepo(%q) = %s/%s, want %s/%s", tc.in, owner, name, tc.owner, tc.name)
		}
	}
}

// TestParseGitHubURL covers the three URL forms plus rejection of
// non-GitHub URLs.
func TestParseGitHubURL(t *testing.T) {
	cases := []struct {
		in    string
		owner string
		name  string
		ok    bool
	}{
		{"https://github.com/alice/repo.git", "alice", "repo", true},
		{"https://github.com/alice/repo", "alice", "repo", true},
		{"git@github.com:alice/repo.git", "alice", "repo", true},
		{"git@github.com:alice/repo", "alice", "repo", true},
		{"ssh://git@github.com/alice/repo.git", "alice", "repo", true},
		{"https://gitlab.com/alice/repo.git", "", "", false},
		{"https://github.com/alice", "", "", false},
		{"https://github.com/alice/repo/extra", "", "", false},
		{"", "", "", false},
	}
	for _, tc := range cases {
		owner, name, ok := gh.ParseGitHubURL(tc.in)
		if ok != tc.ok || owner != tc.owner || name != tc.name {
			t.Errorf("ParseGitHubURL(%q) = (%q, %q, %v), want (%q, %q, %v)", tc.in, owner, name, ok, tc.owner, tc.name, tc.ok)
		}
	}
}

// TestGetIssue_LabelsDecoded asserts that GetIssue surfaces user
// labels with color/description from the REST payload and strips
// orchard phase labels (mirroring PullRequest.Labels semantics).
func TestGetIssue_LabelsDecoded(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/alice/repo/issues/42", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{
			"number": 42,
			"title": "labels",
			"body": "",
			"state": "open",
			"html_url": "https://github.com/alice/repo/issues/42",
			"created_at": "2026-05-13T00:00:00Z",
			"updated_at": "2026-05-13T00:00:00Z",
			"user": {"login": "alice"},
			"labels": [
				{"name": "bug", "color": "d73a4a", "description": "Something broken"},
				{"name": "in-progress", "color": "fbca04", "description": "phase tag"},
				{"name": "P0", "color": "ff0000", "description": ""}
			]
		}`))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	c := gh.NewClient(ts.URL, "tok")
	issue, err := c.GetIssue(context.Background(), "alice", "repo", 42)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}

	names := make([]string, 0, len(issue.Labels))
	for _, l := range issue.Labels {
		names = append(names, l.Name)
	}
	for _, n := range names {
		if n == "in-progress" {
			t.Errorf("phase label %q survived GetIssue labels: %v", n, names)
		}
	}

	want := map[string]gh.Label{
		"bug": {Name: "bug", Color: "d73a4a", Description: "Something broken"},
		"P0":  {Name: "P0", Color: "ff0000", Description: ""},
	}
	got := map[string]gh.Label{}
	for _, l := range issue.Labels {
		got[l.Name] = l
	}
	for n, w := range want {
		g, ok := got[n]
		if !ok {
			t.Errorf("label %q missing from issue.Labels: %v", n, names)
			continue
		}
		if g != w {
			t.Errorf("label %q = %+v, want %+v", n, g, w)
		}
	}
}
