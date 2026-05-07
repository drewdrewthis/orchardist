package gh

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
