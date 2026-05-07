package gh

import (
	"regexp"
	"strconv"
)

// Package-level compiled regexes — compiling once at init time is cheaper
// than sync.Once for patterns that are always needed.
var (
	// issueKeywordRe matches "issue" (case-insensitive) optionally followed by
	// "/" or "-" and then the issue number. Applied to BOTH the original branch
	// name and the prefix-stripped version.
	issueKeywordRe = regexp.MustCompile(`(?i)issue[/\-]?(\d+)`)

	// leadingNumberRe matches a numeric sequence at the start of the stripped
	// branch followed by a hyphen. Only N >= 100 is accepted by the caller.
	leadingNumberRe = regexp.MustCompile(`^(\d+)-`)

	// embeddedNumberRe matches a numeric sequence surrounded by a leading
	// hyphen and either a trailing hyphen or end-of-string. Only N >= 100 is
	// accepted by the caller.
	embeddedNumberRe = regexp.MustCompile(`-(\d+)(?:-|$)`)

	// stripPrefixRe matches a common branch-name prefix such as "feat/",
	// "fix-", "wip_". The prefix is one or more alphanumeric chars (starting
	// with a letter) followed by "/" or "_".
	stripPrefixRe = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9]*[/_]`)
)

// ExtractIssueNumber attempts to derive a GitHub issue number from a branch
// name, mirroring the logic in crates/orchard/src/github.rs.
//
// Rules applied IN ORDER — the first match wins:
//
//  1. Keyword pattern (case-insensitive, no minimum N):
//     (?i)issue[/\-]?(\d+)
//     Applied to BOTH the original branch name AND the prefix-stripped version.
//     Accepts N >= 1.
//
//  2. Leading number on the stripped branch:
//     ^(\d+)-
//     Accepts N >= 100 only.
//
//  3. Embedded number on the stripped branch:
//     -(\d+)(?:-|$)
//     Accepts N >= 100 only.
//
// The "stripped" version removes any leading prefix matching
// ^[a-zA-Z][a-zA-Z0-9]*[/_] (e.g. "feat/", "fix-", "wip_").
//
// Returns (n, true) on a successful match, (0, false) otherwise.
func ExtractIssueNumber(branch string) (int, bool) {
	if branch == "" {
		return 0, false
	}

	// Compute the prefix-stripped variant once.
	stripped := stripPrefixRe.ReplaceAllLiteralString(branch, "")

	// 1. Keyword pattern on original and stripped.
	for _, candidate := range []string{branch, stripped} {
		if m := issueKeywordRe.FindStringSubmatch(candidate); m != nil {
			n, err := strconv.Atoi(m[1])
			if err == nil && n >= 1 {
				return n, true
			}
		}
	}

	// 2. Leading number (>= 100) on stripped.
	if m := leadingNumberRe.FindStringSubmatch(stripped); m != nil {
		n, err := strconv.Atoi(m[1])
		if err == nil && n >= 100 {
			return n, true
		}
	}

	// 3. Embedded number (>= 100) on stripped.
	if m := embeddedNumberRe.FindStringSubmatch(stripped); m != nil {
		n, err := strconv.Atoi(m[1])
		if err == nil && n >= 100 {
			return n, true
		}
	}

	return 0, false
}
