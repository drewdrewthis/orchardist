// Package gh — GraphQL enrichment for PullRequest.
//
// This file adds EnrichPullRequest, which fetches the five fields that
// the GitHub REST list endpoint does not return: mergeable, mergeStateStatus,
// reviewDecision, statusCheckRollup, and labels. These require a dedicated
// GraphQL round-trip.
//
// The result is merged back into the per-key prs cache so subsequent
// GetPullRequest calls return the enriched view. Cache TTL is 60s
// (enrichmentTTL) — shorter than CacheTTL because mergeable and CI
// status change faster than basic PR metadata.
//
// UNKNOWN mergeable is never written to the cache so the next call
// always re-fetches. This avoids the #367 flap pattern where a
// transient UNKNOWN hardens into a stale cached value.
package gh

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// enrichmentTTL is shorter than CacheTTL because mergeable + CI flap fast.
const enrichmentTTL = 60 * time.Second

// enrichPRQuery is the GitHub GraphQL query that fetches the five
// enrichment fields. The statusCheckRollup is read from the head
// commit rather than from a separate check-runs endpoint so we get
// the aggregated state in a single round-trip.
const enrichPRQuery = `query($owner:String!,$name:String!,$number:Int!){
  repository(owner:$owner,name:$name){
    pullRequest(number:$number){
      mergeable
      mergeStateStatus
      reviewDecision
      labels(first:50){ nodes{ name color description } }
      commits(last:1){ nodes{ commit{ statusCheckRollup{ state } } } }
    }
  }
}`

// PhaseLabels are orchard lifecycle tags that are excluded from
// PullRequest.Labels. Only user-assigned labels are surfaced.
// See ~/.claude/skills/gh-tag/ for the canonical list.
var phaseLabels = map[string]struct{}{
	"investigating": {},
	"needs-plan":    {},
	"needs-repro":   {},
	"planned":       {},
	"in-progress":   {},
	"in-ai-review":  {},
	"pr-ready":      {},
	"blocked":       {},
}

// enrichRaw is the wire-level shape of the GitHub GraphQL enrichment
// response. Kept package-private; callers see the typed PullRequest.
type enrichRaw struct {
	Data struct {
		Repository struct {
			PullRequest struct {
				Mergeable        string  `json:"mergeable"`
				MergeStateStatus string  `json:"mergeStateStatus"`
				ReviewDecision   *string `json:"reviewDecision"`
				Labels           struct {
					Nodes []struct {
						Name        string `json:"name"`
						Color       string `json:"color"`
						Description string `json:"description"`
					} `json:"nodes"`
				} `json:"labels"`
				Commits struct {
					Nodes []struct {
						Commit struct {
							StatusCheckRollup *struct {
								State string `json:"state"`
							} `json:"statusCheckRollup"`
						} `json:"commit"`
					} `json:"nodes"`
				} `json:"commits"`
			} `json:"pullRequest"`
		} `json:"repository"`
	} `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors,omitempty"`
}

// prEnrichEntry holds a cached enriched PullRequest and the time it was
// last fetched. Stored in the per-key prs map alongside the base entry.
type prEnrichEntry struct {
	value PullRequest
	at    time.Time
}

// enrichedAt stores the enrichment timestamps separately from the base
// prEntry so we can apply the shorter enrichmentTTL without touching
// the CacheTTL-governed REST data. Protected by prMu.
//
// We reuse the existing prs map value for storage — the enrichment is
// merged into the PullRequest struct in place, and the enrichAt map
// tracks when that last happened.

// EnrichPullRequest fetches the GraphQL-only enrichment fields
// (mergeable, mergeStateStatus, reviewDecision, statusCheckRollup,
// labels) for the given PR key, merges the result into the per-key
// prs cache, and returns the fully-enriched PullRequest.
//
// Cache behaviour:
//   - A hit within enrichmentTTL (60s) returns the cached enriched value.
//   - UNKNOWN mergeable is never cached — the next call re-fetches so
//     the transient computing state does not stick (#367 contract).
//   - A miss fetches from GitHub GraphQL and caches (unless UNKNOWN).
func (p *Provider) EnrichPullRequest(ctx context.Context, key PullRequestKey) (PullRequest, error) {
	// --- cache check ---
	p.prMu.RLock()
	entry, ok := p.prs[key]
	enrichedAt, hasEnriched := p.enrichAt[key]
	p.prMu.RUnlock()

	if ok && hasEnriched && p.clock().Sub(enrichedAt) < enrichmentTTL {
		return entry.value, nil
	}

	// --- fetch ---
	c, err := p.httpClient(ctx)
	if err != nil {
		return PullRequest{}, err
	}

	variables := map[string]any{
		"owner":  key.Owner,
		"name":   key.Name,
		"number": key.Number,
	}
	raw, err := c.GraphQL(ctx, enrichPRQuery, variables)
	if err != nil {
		return PullRequest{}, fmt.Errorf("EnrichPullRequest graphql: %w", err)
	}

	var envelope enrichRaw
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return PullRequest{}, fmt.Errorf("EnrichPullRequest decode: %w", err)
	}
	if len(envelope.Errors) > 0 {
		msgs := make([]string, 0, len(envelope.Errors))
		for _, e := range envelope.Errors {
			msgs = append(msgs, e.Message)
		}
		return PullRequest{}, fmt.Errorf("EnrichPullRequest graphql errors: %s", strings.Join(msgs, "; "))
	}

	wire := envelope.Data.Repository.PullRequest

	// --- map fields ---
	mergeable := mapMergeableState(wire.Mergeable)

	var rd *ReviewDecision
	if wire.ReviewDecision != nil {
		mapped := ReviewDecision(*wire.ReviewDecision)
		rd = &mapped
	}

	labels := make([]Label, 0, len(wire.Labels.Nodes))
	for _, n := range wire.Labels.Nodes {
		labels = append(labels, Label{
			Name:        n.Name,
			Color:       n.Color,
			Description: n.Description,
		})
	}

	ciStatus := CiStatusUnknown
	if len(wire.Commits.Nodes) > 0 {
		commit := wire.Commits.Nodes[0].Commit
		if commit.StatusCheckRollup != nil {
			ciStatus = mapStatusCheckRollup(commit.StatusCheckRollup.State)
		}
	}

	// --- merge into existing base entry ---
	p.prMu.Lock()
	base := p.prs[key]
	base.value.Mergeable = mergeable
	base.value.MergeStateStatus = wire.MergeStateStatus
	base.value.ReviewDecision = rd
	base.value.StatusCheckRollup = ciStatus
	base.value.Labels = filterPhaseLabels(labels)

	// Cache the enriched result only when mergeable is definitive.
	// UNKNOWN means GitHub is still computing — don't cache it so the
	// next call re-fetches (#367 contract).
	if mergeable != MergeableStateUnknown {
		p.prs[key] = base
		p.enrichAt[key] = p.clock()
	}
	enriched := base.value
	p.prMu.Unlock()

	return enriched, nil
}

// mapMergeableState maps the raw GitHub string to the typed enum.
// Anything unrecognised maps to UNKNOWN, which is the safe fallback.
func mapMergeableState(s string) MergeableState {
	switch s {
	case "MERGEABLE":
		return MergeableStateMergeable
	case "CONFLICTING":
		return MergeableStateConflicting
	default:
		return MergeableStateUnknown
	}
}

// mapStatusCheckRollup maps GitHub's CommitStatusState / CheckStatusState
// to our CiStatus enum.
//
// Mapping rules (per issue #442 spec):
//   - any FAILURE or ERROR → FAILURE
//   - any PENDING or EXPECTED → PENDING
//   - SUCCESS → SUCCESS
//   - empty / nil / unknown → UNKNOWN
func mapStatusCheckRollup(state string) CiStatus {
	switch state {
	case "SUCCESS":
		return CiStatusSuccess
	case "FAILURE", "ERROR":
		return CiStatusFailure
	case "PENDING", "EXPECTED":
		return CiStatusPending
	default:
		return CiStatusUnknown
	}
}

// filterPhaseLabels returns the input slice with orchard phase labels
// removed, preserving the relative order of the remaining labels.
func filterPhaseLabels(in []Label) []Label {
	out := make([]Label, 0, len(in))
	for _, l := range in {
		if _, isPhase := phaseLabels[l.Name]; !isPhase {
			out = append(out, l)
		}
	}
	return out
}
