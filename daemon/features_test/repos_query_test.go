package features_test

import (
	"testing"
	"time"
)

// @scenario repos query returns array with required fields
func TestReposQuery_ReturnsRequiredFields(t *testing.T) {
	ts := startServerWithRepo(t)

	r := postGQL(t, ts.URL, `{ repos { id slug path worktrees { id } } }`)
	assertNoErrors(t, r)

	t.Run("when repos query returns", func(t *testing.T) {
		reposRaw, ok := r.Data["repos"]
		if !ok || reposRaw == nil {
			t.Fatal("repos field missing from response")
		}
		repos := asList(t, reposRaw, "repos")
		if len(repos) == 0 {
			t.Fatal("repos: expected at least one repo, got empty list")
		}
		for i, raw := range repos {
			repo := asMap(t, raw, "repos[i]")
			requireFields(t, repo, "id", "slug", "path", "worktrees")

			_ = i // suppress unused warning
			wts := asList(t, repo["worktrees"], "repos[].worktrees")
			// At minimum the main worktree must be present.
			if len(wts) == 0 {
				t.Errorf("repos[].worktrees: expected at least one worktree, got empty")
			}
		}
	})
}

// @scenario worktree carries branch, head, bare, host, repo, ahead, behind
func TestReposQuery_WorktreeCarriesRequiredFields(t *testing.T) {
	ts := startServerWithRepo(t)

	r := postGQL(t, ts.URL, `{ repos { worktrees { branch head bare host ahead behind repo { id } } } }`)
	assertNoErrors(t, r)

	repos := asList(t, r.Data["repos"], "repos")
	for _, rawRepo := range repos {
		repo := asMap(t, rawRepo, "repo")
		worktrees := asList(t, repo["worktrees"], "worktrees")
		for _, rawWt := range worktrees {
			wt := asMap(t, rawWt, "worktree")
			// branch and head must be present (may be null for bare worktrees).
			requireField(t, wt, "branch")
			requireField(t, wt, "head")
			requireField(t, wt, "bare")
			requireField(t, wt, "host")
			requireField(t, wt, "ahead")
			requireField(t, wt, "behind")
			requireField(t, wt, "repo")
		}
	}
}

// @scenario worktree carries nullable pr object with number, state, title
func TestReposQuery_WorktreeCarriesNullablePr(t *testing.T) {
	ts := startServerWithRepo(t)

	r := postGQL(t, ts.URL, `{ repos { worktrees { pr { number state title } } } }`)
	assertNoErrors(t, r)

	repos := asList(t, r.Data["repos"], "repos")
	for _, rawRepo := range repos {
		repo := asMap(t, rawRepo, "repo")
		worktrees := asList(t, repo["worktrees"], "worktrees")
		for _, rawWt := range worktrees {
			wt := asMap(t, rawWt, "worktree")
			// pr key must be present (value may be null).
			if _, ok := wt["pr"]; !ok {
				t.Error("worktree.pr field missing from response")
			}
			// If pr is non-null, it must have number, state, title.
			if wt["pr"] != nil {
				pr := asMap(t, wt["pr"], "pr")
				requireFields(t, pr, "number", "state", "title")
			}
		}
	}
}

// @scenario worktree carries nullable issue object with number, state, title
func TestReposQuery_WorktreeCarriesNullableIssue(t *testing.T) {
	ts := startServerWithRepo(t)

	r := postGQL(t, ts.URL, `{ repos { worktrees { issue { number state title } } } }`)
	assertNoErrors(t, r)

	repos := asList(t, r.Data["repos"], "repos")
	for _, rawRepo := range repos {
		repo := asMap(t, rawRepo, "repo")
		worktrees := asList(t, repo["worktrees"], "worktrees")
		for _, rawWt := range worktrees {
			wt := asMap(t, rawWt, "worktree")
			if _, ok := wt["issue"]; !ok {
				t.Error("worktree.issue field missing from response")
			}
			if wt["issue"] != nil {
				issue := asMap(t, wt["issue"], "issue")
				requireFields(t, issue, "number", "state", "title")
			}
		}
	}
}

// @scenario repos returns empty list when no repos configured
func TestReposQuery_EmptyWhenNoReposConfigured(t *testing.T) {
	// Use minimal server with no git provider / repos lister.
	ts := startMinimalServer(t)

	r := postGQL(t, ts.URL, `{ repos { id slug } }`)
	assertNoErrors(t, r)

	repos := asList(t, r.Data["repos"], "repos")
	if len(repos) != 0 {
		t.Errorf("expected empty repos, got %d entries", len(repos))
	}
}

// @scenario repos query round-trip latency is within interactive budget
func TestReposQuery_LatencyWithinBudget(t *testing.T) {
	ts := startServerWithRepo(t)

	const budget = 50 * time.Millisecond
	// Run 5 samples; check the median is within budget.
	var durations []time.Duration
	for i := 0; i < 5; i++ {
		_, elapsed := postGQLTimed(t, ts.URL, `{ repos { worktrees { branch } } }`)
		durations = append(durations, elapsed)
	}

	// P50 check (median of 5).
	sortDurations(durations)
	median := durations[len(durations)/2]
	if median > budget {
		t.Errorf("repos query P50 latency %v exceeds %v budget", median, budget)
	}
}

// sortDurations sorts in ascending order (insertion sort; small N).
func sortDurations(d []time.Duration) {
	for i := 1; i < len(d); i++ {
		for j := i; j > 0 && d[j] < d[j-1]; j-- {
			d[j], d[j-1] = d[j-1], d[j]
		}
	}
}
