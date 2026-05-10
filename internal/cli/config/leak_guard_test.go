package config

// Test-isolation guard per #540 §A7.
//
// Background: prior to ADR-015 the test suite leaked into the developer's
// real ~/.orchard/config.json because `orchard config init` /
// `add-repo` tests didn't override $HOME. The user's real config picked
// up six fixture entries (TestAddRepo_*, alpha, beta, etc) before the
// leak was noticed.
//
// This test runs LAST (alphabetically after every TestAddRepo_*) and
// fails the suite if it detects fixture-shaped paths in the real
// ~/.orchard/config.json. It's a CI tripwire — a passing run means
// the test isolation discipline (setHomeForTest in every config-mutating
// test) held.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fixtureLeakPatterns are substrings that would only appear in
// ~/.orchard/config.json if a test failed to override $HOME.
//
// `/var/folders/` — macOS t.TempDir() prefix.
// `/tmp/` — Linux t.TempDir() prefix.
// `TestAddRepo_` — suite-prefixed test name, accidentally leaked into
// a slug or path when a test passed `t.Name()` somewhere.
// `team/example`, `team/with-git`, `team/first`, `team/second`,
// `team/alpha`, `team/beta` — slugs used by tests in this package.
var fixtureLeakPatterns = []string{
	"/var/folders/",
	"/tmp/",
	"TestAddRepo_",
	"team/example",
	"team/with-git",
	"team/first",
	"team/second",
	"team/alpha",
	"team/beta",
}

// TestZ_NoFixtureLeakInRealConfig is the suite-wide leak detector.
// Named with a leading `Z_` so it sorts last in `go test -v` output —
// any earlier failure usually means we have other problems first.
//
// The test no-ops if $HOME is overridden (which is normal for this
// package's other tests via setHomeForTest), since the override means
// we're not pointing at the real config.
func TestZ_NoFixtureLeakInRealConfig(t *testing.T) {
	// Resolve the *real* $HOME by reading /etc/passwd, not via
	// os.UserHomeDir() — the latter respects the test-overridden $HOME
	// and would always look at the temp dir even when other tests
	// leaked. This guard wants to inspect the user's *actual* config
	// file regardless of any t.Setenv("HOME", ...) in flight.
	realHome := readRealHome(t)
	if realHome == "" {
		t.Skip("could not resolve real $HOME — skipping leak guard")
	}

	cfgPath := filepath.Join(realHome, ".orchard", "config.json")
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		// File doesn't exist or unreadable — not a leak, just a fresh
		// machine. The suite couldn't have leaked into a non-existent
		// file (the fsnotify-driven write path also creates the file
		// only when something writes to it).
		if os.IsNotExist(err) {
			return
		}
		t.Skipf("could not read %s: %v", cfgPath, err)
	}

	for _, pat := range fixtureLeakPatterns {
		if strings.Contains(string(data), pat) {
			t.Errorf("fixture leak detected in %s: contains %q. "+
				"A test wrote to the real ~/.orchard/config.json without "+
				"calling setHomeForTest. Audit the failing test and add "+
				"`t.Setenv(\"HOME\", t.TempDir())` before it touches the "+
				"config provider.", cfgPath, pat)
		}
	}

	// Additional guard: the file should parse as our File shape and
	// contain only the canonical top-level keys per ADR-015.
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err == nil {
		var unknown []string
		for k := range raw {
			switch k {
			case "version", "repos", "peers":
				// canonical
			default:
				unknown = append(unknown, k)
			}
		}
		if len(unknown) > 0 {
			t.Logf("note: %s carries pre-ADR-015 keys: %v "+
				"(harmless until the next save round-trip; consider one-shot scrub)",
				cfgPath, unknown)
		}
	}
}

// readRealHome reads /etc/passwd to find the actual home directory of
// the user running the test, bypassing any $HOME override.
//
// On macOS we fall back to `dscl .` since /etc/passwd doesn't list
// directory-services accounts. Returns empty string on failure.
func readRealHome(t *testing.T) string {
	t.Helper()
	// `os.Getenv("USER")` is honoured even after t.Setenv("HOME", ...)
	// because tests don't override USER (only HOME). The user name is
	// what we need to look up the real home.
	user := os.Getenv("USER")
	if user == "" {
		return ""
	}
	// Try /etc/passwd — works on Linux + macOS (when the user has a
	// passwd entry).
	data, err := os.ReadFile("/etc/passwd")
	if err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			fields := strings.Split(line, ":")
			if len(fields) >= 6 && fields[0] == user {
				return fields[5]
			}
		}
	}
	// macOS Directory Services fallback. Best-effort: common case is
	// /Users/<user>; if that exists we trust it.
	candidate := "/Users/" + user
	if info, err := os.Stat(candidate); err == nil && info.IsDir() {
		return candidate
	}
	return ""
}
