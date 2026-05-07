package gh

import "time"

// export_test.go exposes package-private helpers for the external test package
// (package gh_test). This file is only compiled during testing.

// ExportMapStatusCheckRollup wraps mapStatusCheckRollup for external tests.
func ExportMapStatusCheckRollup(state string) CiStatus {
	return mapStatusCheckRollup(state)
}

// ExportFilterPhaseLabels wraps filterPhaseLabels for external tests.
func ExportFilterPhaseLabels(in []string) []string {
	return filterPhaseLabels(in)
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
