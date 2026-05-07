package gh

import "testing"

// TestExtractIssueNumber covers the branch-parse rules ported from
// crates/orchard/src/github.rs. The table includes all examples from the
// feature file plus representative edge cases.
func TestExtractIssueNumber(t *testing.T) {
	tests := []struct {
		branch  string
		wantN   int
		wantOK  bool
		comment string
	}{
		// Keyword pattern — original branch.
		{
			branch:  "issue441/schema-enrichment",
			wantN:   441,
			wantOK:  true,
			comment: "keyword pattern wins on original branch",
		},
		{
			branch:  "Issue-123-something",
			wantN:   123,
			wantOK:  true,
			comment: "keyword pattern case-insensitive, hyphen variant",
		},
		// Keyword pattern — small N (no minimum floor on keyword path).
		{
			branch:  "issue/1",
			wantN:   1,
			wantOK:  true,
			comment: "lowercase issue/ with small N is valid for keyword path",
		},
		{
			branch:  "issue/42",
			wantN:   42,
			wantOK:  true,
			comment: "lowercase issue/ with N<100 still matches keyword pattern",
		},
		{
			branch:  "Issue/777",
			wantN:   777,
			wantOK:  true,
			comment: "mixed-case Issue/ parses keyword pattern",
		},
		// Keyword pattern on stripped branch.
		{
			branch:  "feat/issue441-description",
			wantN:   441,
			wantOK:  true,
			comment: "keyword pattern on stripped branch (prefix feat/ stripped)",
		},
		// Leading number >= 100 on stripped.
		{
			branch:  "441-some-slug",
			wantN:   441,
			wantOK:  true,
			comment: "leading number >= 100 on original (no prefix to strip)",
		},
		// Embedded number >= 100 on stripped.
		{
			branch:  "feature-441-slug",
			wantN:   441,
			wantOK:  true,
			comment: "embedded number >= 100 on stripped (prefix feature- not stripped but 441 found embedded)",
		},
		// Leading number below 100 floor — must return false.
		{
			branch:  "12-something",
			wantN:   0,
			wantOK:  false,
			comment: "leading number 12 fails >= 100 floor",
		},
		// No number at all.
		{
			branch:  "feature/no-number",
			wantN:   0,
			wantOK:  false,
			comment: "no number anywhere",
		},
		// Keyword precedence: "issue441/441-other" → 441 via keyword, not 441 again.
		{
			branch:  "issue441/441-other",
			wantN:   441,
			wantOK:  true,
			comment: "keyword pattern on original wins first",
		},
		// Empty branch.
		{
			branch:  "",
			wantN:   0,
			wantOK:  false,
			comment: "empty branch returns no match",
		},
		// Embedded number exactly at the 100 boundary.
		{
			branch:  "fix-100-something",
			wantN:   100,
			wantOK:  true,
			comment: "embedded number exactly at 100 boundary is accepted",
		},
		// Embedded number just below 100 floor.
		{
			branch:  "fix-99-something",
			wantN:   0,
			wantOK:  false,
			comment: "embedded number 99 fails >= 100 floor",
		},
		// Leading number at the 100 boundary on a prefixed branch.
		{
			branch:  "feat/100-description",
			wantN:   100,
			wantOK:  true,
			comment: "leading number 100 on stripped branch accepted",
		},
		// Branch with only alpha chars.
		{
			branch:  "main",
			wantN:   0,
			wantOK:  false,
			comment: "default-like branch with no numbers",
		},
		// issue0 — keyword matches but n < 1, should be rejected.
		// Note: the regex requires \d+, so "issue0" captures "0" → n=0 < 1.
		{
			branch:  "issue0",
			wantN:   0,
			wantOK:  false,
			comment: "issue0 is below the n>=1 floor on the keyword path",
		},
		// ISSUENNN (no separator) — the regex allows the separator to be absent.
		{
			branch:  "ISSUE999fix",
			wantN:   999,
			wantOK:  true,
			comment: "uppercase ISSUE with no separator still matches keyword pattern",
		},
	}

	for _, tt := range tests {
		t.Run(tt.branch+"_"+tt.comment, func(t *testing.T) {
			gotN, gotOK := ExtractIssueNumber(tt.branch)
			if gotOK != tt.wantOK {
				t.Errorf("ExtractIssueNumber(%q) ok=%v, want %v (%s)", tt.branch, gotOK, tt.wantOK, tt.comment)
				return
			}
			if gotOK && gotN != tt.wantN {
				t.Errorf("ExtractIssueNumber(%q) n=%d, want %d (%s)", tt.branch, gotN, tt.wantN, tt.comment)
			}
		})
	}
}
