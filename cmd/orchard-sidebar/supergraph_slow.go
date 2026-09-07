package main

// Supergraph slow lane (#844). The daemon walks workView.repos.worktrees in one
// query; supergraph has no such join, so this adapter resolves each tmux
// session's worktree enrichment from the typed leaves: tmuxSessions gives the
// branch and worktree path, claudeInstances the issue/PR numbers and the PR URL
// (the only supergraph source for a repo's owner/name), and issue(key)/
// pullRequest(key)/paneForBranch(branch) fill the issue title, PR and pane
// path. Fields supergraph does not expose — PR draft/reviewDecision/checks/
// mergeState, worktree ahead/behind — are marked unknown so the view renders
// "—" rather than a fabricated verdict or a false zero.

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

const sgSlowLeadQuery = `{
  claudeInstances { pane session { issueNumber prNumber prUrl gitBranch cwd } }
  tmuxSessions { name worktree branch }
  tmuxPanes { key session }
}`

type sgSlowLeadResp struct {
	Data struct {
		ClaudeInstances []struct {
			Pane    string `json:"pane"`
			Session struct {
				IssueNumber *int    `json:"issueNumber"`
				PrNumber    *int    `json:"prNumber"`
				PrURL       *string `json:"prUrl"`
				GitBranch   *string `json:"gitBranch"`
				Cwd         string  `json:"cwd"`
			} `json:"session"`
		} `json:"claudeInstances"`
		TmuxSessions []sgTmuxSession `json:"tmuxSessions"`
		TmuxPanes    []struct {
			Key     string `json:"key"`
			Session string `json:"session"`
		} `json:"tmuxPanes"`
	} `json:"data"`
}

// sgEnrich is one session's resolved enrichment inputs before the keyed lookups.
type sgEnrich struct {
	session string
	branch  string
	path    string
	issueNo int
	prNo    int
	owner   string // parsed from the session's prUrl; "" when it has no PR
	repo    string
}

func fetchSlowSupergraph() tea.Msg {
	var lead sgSlowLeadResp
	if err := post(sgSlowLeadQuery, 90*time.Second, &lead); err != nil {
		return slowDataMsg{err: err}
	}

	paneToSession := map[string]string{}
	for _, p := range lead.Data.TmuxPanes {
		paneToSession[p.Key] = p.Session
	}

	enrich := map[string]*sgEnrich{}
	for _, s := range lead.Data.TmuxSessions {
		e := &sgEnrich{session: s.Name}
		if s.Branch != nil {
			e.branch = *s.Branch
		}
		if s.Worktree != nil {
			e.path = *s.Worktree
		}
		enrich[s.Name] = e
	}
	for _, ci := range lead.Data.ClaudeInstances {
		name := paneToSession[ci.Pane]
		if name == "" {
			continue
		}
		e, ok := enrich[name]
		if !ok {
			e = &sgEnrich{session: name}
			enrich[name] = e
		}
		if e.branch == "" && ci.Session.GitBranch != nil {
			e.branch = *ci.Session.GitBranch
		}
		if e.path == "" && ci.Session.Cwd != "" {
			e.path = ci.Session.Cwd
		}
		if ci.Session.IssueNumber != nil {
			e.issueNo = *ci.Session.IssueNumber
		}
		if ci.Session.PrNumber != nil {
			e.prNo = *ci.Session.PrNumber
		}
		if ci.Session.PrURL != nil {
			e.owner, e.repo = ownerRepoFromURL(*ci.Session.PrURL)
		}
	}

	enriched := resolveSupergraphEnrichment(enrich)
	return slowDataMsg{
		wtBySession: enriched.bySession,
		repoBySess:  enriched.repoBySession,
		wtByPath:    enriched.byPath,
		repoByPath:  enriched.repoByPath,
	}
}

type sgSlowResult struct {
	bySession     map[string]wtInfo
	repoBySession map[string]string
	byPath        map[string]wtInfo
	repoByPath    map[string]string
}

// resolveSupergraphEnrichment issues one aliased query for every session's
// issue/pullRequest/paneForBranch lookup, then folds the results into the same
// maps the daemon slow lane returns. Batching into a single request keeps the
// slow lane one round trip regardless of session count.
func resolveSupergraphEnrichment(enrich map[string]*sgEnrich) sgSlowResult {
	res := sgSlowResult{
		bySession:     map[string]wtInfo{},
		repoBySession: map[string]string{},
		byPath:        map[string]wtInfo{},
		repoByPath:    map[string]string{},
	}

	// Stable alias order so the query and its decode agree.
	names := make([]string, 0, len(enrich))
	for name := range enrich {
		names = append(names, name)
	}

	var b strings.Builder
	b.WriteString("{\n")
	for i, name := range names {
		e := enrich[name]
		if e.owner != "" && e.repo != "" && e.issueNo > 0 {
			fmt.Fprintf(&b, "  i%d: issue(key:%s) { number title }\n", i,
				gqlStr(fmt.Sprintf("issue:%s/%s#%d", e.owner, e.repo, e.issueNo)))
		}
		if e.owner != "" && e.repo != "" && e.prNo > 0 {
			fmt.Fprintf(&b, "  p%d: pullRequest(key:%s) { number state }\n", i,
				gqlStr(fmt.Sprintf("pr:%s/%s#%d", e.owner, e.repo, e.prNo)))
		}
		if e.branch != "" {
			fmt.Fprintf(&b, "  q%d: paneForBranch(branch:%s) { path }\n", i, gqlStr(e.branch))
		}
	}
	b.WriteString("}")

	var raw struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	// A failed enrichment query still yields worktree rows from the lead data —
	// branch/path are known without it; only issue/PR titles are lost.
	if err := post(b.String(), 90*time.Second, &raw); err != nil {
		logf("supergraph slow enrich: %v", err)
	}

	for i, name := range names {
		e := enrich[name]
		w := wtInfo{Branch: e.branch, Path: e.path, DriftUnknown: true}

		if m := raw.Data[fmt.Sprintf("i%d", i)]; hasData(m) {
			var iss struct {
				Number int    `json:"number"`
				Title  string `json:"title"`
			}
			if json.Unmarshal(m, &iss) == nil && iss.Number > 0 {
				w.Issue = &struct {
					Number int    `json:"number"`
					Title  string `json:"title"`
				}{Number: iss.Number, Title: iss.Title}
			}
		}
		if m := raw.Data[fmt.Sprintf("p%d", i)]; hasData(m) {
			var pr struct {
				Number int    `json:"number"`
				State  string `json:"state"`
			}
			if json.Unmarshal(m, &pr) == nil && pr.Number > 0 {
				// unknown: supergraph's PullRequest exposes no draft/
				// reviewDecision/statusCheckRollup/mergeStateStatus
				// (TODO(supergraph#26)), so prStatus renders "—".
				w.PR = &prInfo{Number: pr.Number, State: pr.State, unknown: true}
			}
		}
		if w.Path == "" {
			if m := raw.Data[fmt.Sprintf("q%d", i)]; hasData(m) {
				var pane struct {
					Path *string `json:"path"`
				}
				if json.Unmarshal(m, &pane) == nil && pane.Path != nil {
					w.Path = *pane.Path
				}
			}
		}

		res.bySession[name] = w
		if w.Path != "" {
			p := filepath.Clean(w.Path)
			res.byPath[p] = w
			if e.owner != "" && e.repo != "" {
				res.repoByPath[p] = e.owner + "/" + e.repo
			}
		}
		if e.owner != "" && e.repo != "" {
			res.repoBySession[name] = e.owner + "/" + e.repo
		}
	}
	return res
}

// gqlStr renders a Go string as a GraphQL/JSON string literal (quoted and
// escaped), so a branch name or issue key with special characters is inlined
// safely into a query built by hand.
func gqlStr(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// ownerRepoFromURL pulls the owner and repo out of a GitHub PR/issue URL such
// as https://github.com/drewdrewthis/orchardist/pull/840 — the only supergraph
// source for a worktree's repo identity (its ClaudeSession carries prUrl but no
// owner/name fields). Returns empty strings when the URL is not a recognizable
// github.com/<owner>/<repo>/... path.
func ownerRepoFromURL(raw string) (string, string) {
	i := strings.Index(raw, "github.com/")
	if i < 0 {
		return "", ""
	}
	parts := strings.Split(strings.Trim(raw[i+len("github.com/"):], "/"), "/")
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return "", ""
	}
	return parts[0], parts[1]
}
