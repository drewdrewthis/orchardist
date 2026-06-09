package gh_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/drewdrewthis/orchardist/internal/server/providers/gh"
)

// TestParseLinkNext exercises the Link-header parser used by the
// paginating helper. The fixtures cover GitHub's documented shapes
// plus the empty/no-next degenerate cases.
func TestParseLinkNext(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"only_last", `<https://api.github.com/x?page=5>; rel="last"`, ""},
		{
			"next_then_last",
			`<https://api.github.com/x?page=2>; rel="next", <https://api.github.com/x?page=5>; rel="last"`,
			"https://api.github.com/x?page=2",
		},
		{
			"prev_next_last",
			`<https://api.github.com/x?page=1>; rel="prev", <https://api.github.com/x?page=3>; rel="next", <https://api.github.com/x?page=5>; rel="last"`,
			"https://api.github.com/x?page=3",
		},
		{
			"rel_without_quotes",
			`<https://api.github.com/x?page=2>; rel=next`,
			"https://api.github.com/x?page=2",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := gh.ExportParseLinkNext(tc.in)
			if got != tc.want {
				t.Fatalf("ParseLinkNext(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestListPulls_PaginationFollowsLinkRelNext asserts the client walks
// the Link header chain and concatenates results across pages.
func TestListPulls_PaginationFollowsLinkRelNext(t *testing.T) {
	var hits int32
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/alice/repo/pulls", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		page := r.URL.Query().Get("page")
		switch page {
		case "", "1":
			// First page: link to page 2.
			w.Header().Set(
				"Link",
				fmt.Sprintf(`<%s?page=2>; rel="next", <%s?page=2>; rel="last"`,
					"http://"+r.Host+"/repos/alice/repo/pulls",
					"http://"+r.Host+"/repos/alice/repo/pulls",
				),
			)
			_, _ = w.Write([]byte(`[{"number":1,"title":"one","state":"open","html_url":"u1","user":{"login":"bob"},"base":{"ref":"main"},"head":{"ref":"feature-1"}}]`))
		case "2":
			// Last page: no Link rel="next".
			_, _ = w.Write([]byte(`[{"number":2,"title":"two","state":"open","html_url":"u2","user":{"login":"bob"},"base":{"ref":"main"},"head":{"ref":"feature-2"}}]`))
		default:
			t.Errorf("unexpected page %q", page)
		}
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	c := gh.NewClient(ts.URL, "tok")
	prs, err := c.ListPulls(context.Background(), "alice", "repo", gh.PullRequestStateOpen)
	if err != nil {
		t.Fatalf("ListPulls: %v", err)
	}
	if hits := atomic.LoadInt32(&hits); hits != 2 {
		t.Fatalf("hits = %d, want 2 (one per page)", hits)
	}
	if len(prs) != 2 {
		t.Fatalf("len(prs) = %d, want 2: %+v", len(prs), prs)
	}
	if prs[0].Number != 1 || prs[1].Number != 2 {
		t.Errorf("PR numbers = [%d %d], want [1 2]", prs[0].Number, prs[1].Number)
	}
}

// TestListIssues_PaginationFollowsLinkRelNext mirrors the PRs case for
// the issues endpoint — a different top-level wire shape (no PR field
// stripping) so the assertion catches per-endpoint regressions.
func TestListIssues_PaginationFollowsLinkRelNext(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/alice/repo/issues", func(w http.ResponseWriter, r *http.Request) {
		page := r.URL.Query().Get("page")
		switch page {
		case "", "1":
			w.Header().Set(
				"Link",
				fmt.Sprintf(`<http://%s/repos/alice/repo/issues?page=2>; rel="next"`, r.Host),
			)
			_, _ = w.Write([]byte(`[{"number":10,"title":"i10","state":"open","html_url":"u","user":{"login":"bob"},"labels":[]}]`))
		case "2":
			_, _ = w.Write([]byte(`[{"number":20,"title":"i20","state":"open","html_url":"u","user":{"login":"bob"},"labels":[]}]`))
		default:
			t.Errorf("unexpected page %q", page)
		}
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	c := gh.NewClient(ts.URL, "tok")
	issues, err := c.ListIssues(context.Background(), "alice", "repo", gh.IssueStateOpen)
	if err != nil {
		t.Fatalf("ListIssues: %v", err)
	}
	if len(issues) != 2 {
		t.Fatalf("len(issues) = %d, want 2", len(issues))
	}
	if issues[0].Number != 10 || issues[1].Number != 20 {
		t.Errorf("issue numbers = [%d %d], want [10 20]", issues[0].Number, issues[1].Number)
	}
}

// TestListWorkflowRuns_PaginationEnvelopeShape verifies pagination
// works for the envelope-shaped workflow runs endpoint.
func TestListWorkflowRuns_PaginationEnvelopeShape(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/alice/repo/actions/runs", func(w http.ResponseWriter, r *http.Request) {
		page := r.URL.Query().Get("page")
		switch page {
		case "", "1":
			w.Header().Set(
				"Link",
				fmt.Sprintf(`<http://%s/repos/alice/repo/actions/runs?page=2>; rel="next"`, r.Host),
			)
			_, _ = w.Write([]byte(`{"workflow_runs":[{"id":111,"name":"a","path":"a.yml"}]}`))
		case "2":
			_, _ = w.Write([]byte(`{"workflow_runs":[{"id":222,"name":"b","path":"b.yml"}]}`))
		default:
			t.Errorf("unexpected page %q", page)
		}
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	c := gh.NewClient(ts.URL, "tok")
	runs, err := c.ListWorkflowRuns(context.Background(), "alice", "repo")
	if err != nil {
		t.Fatalf("ListWorkflowRuns: %v", err)
	}
	if len(runs) != 2 {
		t.Fatalf("len(runs) = %d, want 2", len(runs))
	}
	if runs[0].RunID != 111 || runs[1].RunID != 222 {
		t.Errorf("run ids = [%d %d], want [111 222]", runs[0].RunID, runs[1].RunID)
	}
}

// TestListPulls_SafetyCapBoundsTotalRequests asserts the pagination
// loop stops at MaxPagesOverride even when GitHub keeps offering a
// rel="next" link.
func TestListPulls_SafetyCapBoundsTotalRequests(t *testing.T) {
	var hits int32
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/alice/repo/pulls", func(w http.ResponseWriter, r *http.Request) {
		count := atomic.AddInt32(&hits, 1)
		// Always serve a rel="next" pointing at the next page number;
		// the cap should stop us regardless.
		w.Header().Set(
			"Link",
			fmt.Sprintf(`<http://%s/repos/alice/repo/pulls?page=%d>; rel="next"`, r.Host, count+1),
		)
		fmt.Fprintf(w, `[{"number":%d,"title":"t","state":"open","html_url":"u","user":{"login":"bob"},"base":{"ref":"main"},"head":{"ref":"f%d"}}]`, count, count)
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	c := gh.NewClient(ts.URL, "tok")
	c.MaxPagesOverride = 3
	prs, err := c.ListPulls(context.Background(), "alice", "repo", gh.PullRequestStateOpen)
	if err != nil {
		t.Fatalf("ListPulls: %v", err)
	}
	if got := atomic.LoadInt32(&hits); got != 3 {
		t.Fatalf("hits = %d, want 3 (cap)", got)
	}
	if len(prs) != 3 {
		t.Fatalf("len(prs) = %d, want 3", len(prs))
	}
}

// TestListPulls_PaginationRateLimitPropagates asserts rate-limit on a
// second page propagates the typed error rather than silently
// truncating.
func TestListPulls_PaginationRateLimitPropagates(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/alice/repo/pulls", func(w http.ResponseWriter, r *http.Request) {
		page := r.URL.Query().Get("page")
		switch page {
		case "", "1":
			w.Header().Set(
				"Link",
				fmt.Sprintf(`<http://%s/repos/alice/repo/pulls?page=2>; rel="next"`, r.Host),
			)
			_, _ = w.Write([]byte(`[{"number":1,"title":"t","state":"open","html_url":"u","user":{"login":"bob"},"base":{"ref":"main"},"head":{"ref":"f1"}}]`))
		case "2":
			w.Header().Set("X-RateLimit-Remaining", "0")
			w.Header().Set("X-RateLimit-Reset", "1700000000")
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"message":"rate limit"}`))
		}
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	c := gh.NewClient(ts.URL, "tok")
	_, err := c.ListPulls(context.Background(), "alice", "repo", gh.PullRequestStateOpen)
	if !gh.IsRateLimited(err) {
		t.Fatalf("err = %v, want rate-limited", err)
	}
}

// TestListPulls_PaginationUnauthorizedPropagates asserts a 401 on
// page 2 surfaces ErrNotAuthenticated cleanly.
func TestListPulls_PaginationUnauthorizedPropagates(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/alice/repo/pulls", func(w http.ResponseWriter, r *http.Request) {
		page := r.URL.Query().Get("page")
		switch page {
		case "", "1":
			w.Header().Set(
				"Link",
				fmt.Sprintf(`<http://%s/repos/alice/repo/pulls?page=2>; rel="next"`, r.Host),
			)
			_, _ = w.Write([]byte(`[]`))
		case "2":
			w.WriteHeader(http.StatusUnauthorized)
		}
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	c := gh.NewClient(ts.URL, "tok")
	_, err := c.ListPulls(context.Background(), "alice", "repo", gh.PullRequestStateOpen)
	if !errors.Is(err, gh.ErrNotAuthenticated) {
		t.Fatalf("err = %v, want ErrNotAuthenticated", err)
	}
}

// TestListPulls_SinglePageNoLinkHeader keeps the single-page path
// trivially correct: a response without a Link header completes after
// one round-trip.
func TestListPulls_SinglePageNoLinkHeader(t *testing.T) {
	var hits int32
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/alice/repo/pulls", func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&hits, 1)
		_, _ = w.Write([]byte(`[{"number":1,"title":"only","state":"open","html_url":"u","user":{"login":"bob"},"base":{"ref":"main"},"head":{"ref":"only"}}]`))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	c := gh.NewClient(ts.URL, "tok")
	prs, err := c.ListPulls(context.Background(), "alice", "repo", gh.PullRequestStateOpen)
	if err != nil {
		t.Fatalf("ListPulls: %v", err)
	}
	if hits := atomic.LoadInt32(&hits); hits != 1 {
		t.Fatalf("hits = %d, want 1", hits)
	}
	if len(prs) != 1 {
		t.Fatalf("len(prs) = %d, want 1", len(prs))
	}
}

// TestMaxPagesDefault confirms the public constant matches the cap
// described in #579: 10 pages at per_page=100 = 1000 items.
func TestMaxPagesDefault(t *testing.T) {
	if gh.MaxPages != 10 {
		t.Errorf("MaxPages = %d, want 10", gh.MaxPages)
	}
}

