package gh

import "time"

// export_test.go exposes package-private helpers for the external test package
// (package gh_test). This file is only compiled during testing.

// ExportMapStatusCheckRollup wraps mapStatusCheckRollup for external tests.
func ExportMapStatusCheckRollup(state string) CiStatus {
	return mapStatusCheckRollup(state)
}

// ExportFilterPhaseLabels wraps filterPhaseLabels for external tests.
// Accepts label names as a slice of strings (the historical shape) and
// converts to []Label internally, then projects the filtered result
// back down to a []string of names so existing tests stay terse.
func ExportFilterPhaseLabels(in []string) []string {
	labels := make([]Label, 0, len(in))
	for _, n := range in {
		labels = append(labels, Label{Name: n})
	}
	filtered := filterPhaseLabels(labels)
	out := make([]string, 0, len(filtered))
	for _, l := range filtered {
		out = append(out, l.Name)
	}
	return out
}

// ExportEnrichTimestamp returns the recorded enrichment timestamp for a
// given PR key, or the zero time if EnrichPullRequest never wrote one.
// External tests use this to directly assert the UNKNOWN-not-cached
// invariant rather than inferring it from HTTP call counts.
func (p *Provider) ExportEnrichTimestamp(key PullRequestKey) time.Time {
	p.prMu.RLock()
	defer p.prMu.RUnlock()
	return p.enrichAt[key]
}
