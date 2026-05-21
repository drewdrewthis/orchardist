package claudeprojects

import (
	"os"
	"testing"
)

// TestReadLatestRecap_AgainstRealJSONL drives the fold against the user's
// real session jsonl from ~/.claude/projects/-Users-hope--config-orchard/
// 920a0b2f-e86f-4668-a103-59f3672bd1c2.jsonl which is known to contain a
// /recap invocation. Skipped when the fixture file is not present (i.e.
// in CI). This is the real-runtime drive that proves the production
// path works against actual Claude Code output, not just synthetic
// fixtures.
func TestReadLatestRecap_AgainstRealJSONL(t *testing.T) {
	const fixture = "/Users/hope/.claude/projects/-Users-hope--config-orchard/920a0b2f-e86f-4668-a103-59f3672bd1c2.jsonl"
	info, err := os.Stat(fixture)
	if err != nil {
		t.Skipf("fixture not available: %v", err)
	}
	got, err := readLatestRecap(fixture, info.Size())
	if err != nil {
		t.Fatalf("readLatestRecap: %v", err)
	}
	if got == nil {
		t.Fatal("expected non-nil recap from real /recap-bearing jsonl")
	}
	if len(*got) == 0 {
		t.Error("recap is empty string; expected text")
	}
	t.Logf("recap (first 200 chars): %.200s", *got)
}
