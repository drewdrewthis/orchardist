// Unit tests for the gh-provider → GraphQL projection helpers used by
// the per-field PullRequest enrichment resolvers. These mappers must
// preserve schema invariants when GitHub returns wire values that fall
// outside the documented enum (forward-compat with new GitHub values).
package resolvers

import (
	"testing"

	graphql1 "github.com/drewdrewthis/orchardist/internal/server/graphql"
	"github.com/drewdrewthis/orchardist/internal/server/providers/gh"
)

// TestMapReviewDecision_UnknownWireValueReturnsNil locks the contract
// that an unknown ReviewDecision wire value (a new enum GitHub may
// add — historically COMMENTED was added years after APPROVED) maps
// to nil rather than silently substituting REVIEW_REQUIRED. The schema
// allows null; surfacing null is the honest signal to callers.
func TestMapReviewDecision_UnknownWireValueReturnsNil(t *testing.T) {
	cases := []gh.ReviewDecision{
		"FUTURE_VALUE",
		"COMMENTED_AND_DISMISSED",
		"",
	}
	for _, tc := range cases {
		got := mapReviewDecision(&tc)
		if got != nil {
			t.Errorf("mapReviewDecision(%q) = %v, want nil for unknown wire value", tc, *got)
		}
	}
}

// TestMapReviewDecision_NilInputReturnsNil keeps the trivial case
// honest: when the provider has no review decision, the resolver must
// surface null rather than a synthetic value.
func TestMapReviewDecision_NilInputReturnsNil(t *testing.T) {
	if got := mapReviewDecision(nil); got != nil {
		t.Errorf("mapReviewDecision(nil) = %v, want nil", *got)
	}
}

// TestMapReviewDecision_KnownValuesProjectCorrectly is the happy-path
// table covering every documented GitHub enum value.
func TestMapReviewDecision_KnownValuesProjectCorrectly(t *testing.T) {
	cases := []struct {
		in   gh.ReviewDecision
		want graphql1.ReviewDecisionEnum
	}{
		{gh.ReviewDecisionApproved, graphql1.ReviewDecisionEnumApproved},
		{gh.ReviewDecisionChangesRequested, graphql1.ReviewDecisionEnumChangesRequested},
		{gh.ReviewDecisionReviewRequired, graphql1.ReviewDecisionEnumReviewRequired},
		{gh.ReviewDecisionCommented, graphql1.ReviewDecisionEnumCommented},
		{gh.ReviewDecisionDismissed, graphql1.ReviewDecisionEnumDismissed},
	}
	for _, tc := range cases {
		got := mapReviewDecision(&tc.in)
		if got == nil {
			t.Errorf("mapReviewDecision(%q) = nil, want %q", tc.in, tc.want)
			continue
		}
		if *got != tc.want {
			t.Errorf("mapReviewDecision(%q) = %q, want %q", tc.in, *got, tc.want)
		}
	}
}
