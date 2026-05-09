// Structural tests for scripts/init/orchard.service (AC3 of issue #464).
//
// These tests parse the shipped systemd unit file as plain text — no systemd
// binary or Linux kernel required — so they run on macOS CI too. They enforce:
//
//  1. The namespace-isolating directives that broke tmux socket visibility
//     (PrivateTmp=yes, ProtectHome=read-only) are absent from the shipped unit.
//  2. The documentation contract around TMUX_TMPDIR and the clean-restart
//     requirement after upgrade is present in the unit's leading comment block.
//
// The test file is located via runtime.Caller so the relative-path math stays
// correct regardless of the working directory go test is invoked from.

package daemon

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// serviceUnitPath resolves the absolute path to scripts/init/orchard.service
// by walking up from this test file's directory to the repo root.
func serviceUnitPath(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller(0) failed: cannot determine test file location")
	}
	// thisFile is internal/cli/daemon/orchard_service_test.go
	// Repo root is three directories up.
	root := filepath.Join(filepath.Dir(thisFile), "..", "..", "..")
	return filepath.Join(root, "scripts", "init", "orchard.service")
}

// parseServiceSection returns the active (non-commented) directives from the
// [Service] section of a systemd unit file. A directive is active when it is
// in the [Service] block and its line does NOT start with '#'.
//
// The returned map is keyed by the directive name (e.g. "PrivateTmp") and
// holds ALL values for that key as a slice — systemd allows directives like
// `Environment=` to appear multiple times, and a `map[string]string` would
// silently drop earlier values. Single-value directives can use values[key][0]
// once they confirm the key exists.
func parseServiceSection(content string) map[string][]string {
	directives := make(map[string][]string)
	inService := false
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		// Track section transitions.
		if strings.HasPrefix(trimmed, "[") {
			inService = trimmed == "[Service]"
			continue
		}
		if !inService {
			continue
		}
		// Skip blank lines and comments.
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		// Parse KEY=VALUE. Preserve repeats by appending to the slice.
		if idx := strings.IndexByte(trimmed, '='); idx > 0 {
			key := strings.TrimSpace(trimmed[:idx])
			val := strings.TrimSpace(trimmed[idx+1:])
			directives[key] = append(directives[key], val)
		}
	}
	return directives
}

// readUnitFile reads scripts/init/orchard.service and returns its content.
func readUnitFile(t *testing.T) string {
	t.Helper()
	path := serviceUnitPath(t)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("cannot read unit file at %s: %v\nEnsure scripts/init/orchard.service exists in the repo root.", path, err)
	}
	return string(data)
}

// TestShippedSystemdUnit_NoPrivateTmp_Yes asserts that the shipped
// scripts/init/orchard.service does NOT contain an active PrivateTmp=yes
// directive in its [Service] section.
//
// PrivateTmp=yes was the root cause of issue #464: it placed the daemon in a
// private /tmp mount namespace, hiding the user's tmux socket at
// /tmp/tmux-<uid>/default from every `tmux` invocation the daemon made.
func TestShippedSystemdUnit_NoPrivateTmp_Yes(t *testing.T) {
	t.Parallel()
	content := readUnitFile(t)
	directives := parseServiceSection(content)

	vals, found := directives["PrivateTmp"]
	if !found {
		// Absent entirely — correct.
		return
	}
	for _, val := range vals {
		if strings.EqualFold(val, "yes") || strings.EqualFold(val, "true") || val == "1" {
			t.Errorf(
				"scripts/init/orchard.service: [Service] contains active PrivateTmp=%s\n"+
					"This directive isolates /tmp and hides the tmux socket at /tmp/tmux-<uid>/default.\n"+
					"It was removed in #464 — do not re-add it without updating the issue post-mortem.",
				val,
			)
		}
	}
}

// TestShippedSystemdUnit_NoProtectHome_ReadOnly asserts that the shipped
// scripts/init/orchard.service does NOT contain an active
// ProtectHome=read-only directive in its [Service] section.
//
// ProtectHome=read-only was removed alongside PrivateTmp=yes in #464 because
// it blocked reads under ~/ outside the explicit ReadWritePaths, breaking
// provider access to the user's tmux state.
func TestShippedSystemdUnit_NoProtectHome_ReadOnly(t *testing.T) {
	t.Parallel()
	content := readUnitFile(t)
	directives := parseServiceSection(content)

	vals, found := directives["ProtectHome"]
	if !found {
		return
	}
	for _, val := range vals {
		if strings.EqualFold(val, "read-only") {
			t.Errorf(
				"scripts/init/orchard.service: [Service] contains active ProtectHome=read-only\n"+
					"This directive breaks provider access to the user's home directory.\n"+
					"It was removed in #464 — do not re-add it without updating the issue post-mortem.",
			)
		}
	}
}

// TestShippedSystemdUnit_TMUX_TMPDIR_Documented asserts the documentation
// contract around TMUX_TMPDIR.
//
// Per the AC3 spec: TMUX_TMPDIR is intentionally NOT set so the daemon's
// `tmux` client inherits the user's $TMPDIR (or the system default /tmp) and
// therefore talks to the same socket the user's interactive sessions use.
//
// The test verifies one of:
//   (a) no active Environment=TMUX_TMPDIR= directive is present (correct — it
//       should NOT be set), OR
//   (b) if an Environment= directive for TMUX_TMPDIR is present, the unit's
//       leading comment block contains both "TMUX_TMPDIR" and the words
//       "intentionally NOT set" (or equivalent) documenting why.
//
// Either way the leading comment MUST explain the decision so future
// maintainers don't re-introduce the directive thinking it's needed.
func TestShippedSystemdUnit_TMUX_TMPDIR_Documented(t *testing.T) {
	t.Parallel()
	content := readUnitFile(t)

	// Check whether any active Environment= line sets TMUX_TMPDIR. systemd
	// allows multiple Environment= directives, so we scan all values for the
	// "Environment" key — not just the first.
	directives := parseServiceSection(content)
	tmuxTmpdirSet := false
	for _, v := range directives["Environment"] {
		if strings.Contains(v, "TMUX_TMPDIR") {
			tmuxTmpdirSet = true
			break
		}
	}

	// Regardless of whether the directive is present, the comment block must
	// document the decision. We scan the entire file's comment lines (not just
	// the leading block) because the [Service] section also carries inline
	// comments explaining the rationale.
	fullComments := extractAllCommentLines(content)
	hasTmuxTmpdirMention := strings.Contains(fullComments, "TMUX_TMPDIR")
	hasIntentionallyNotSet := strings.Contains(strings.ToLower(fullComments), "intentionally not set")

	if tmuxTmpdirSet {
		// If the directive is set, the documentation must still explain
		// why TMUX_TMPDIR is managed the way it is.
		if !hasTmuxTmpdirMention || !hasIntentionallyNotSet {
			t.Errorf(
				"scripts/init/orchard.service: Environment= sets TMUX_TMPDIR but the\n"+
					"comment block does not contain both \"TMUX_TMPDIR\" and \"intentionally NOT set\"\n"+
					"(or equivalent explanation). Document the decision so maintainers know why.\n"+
					"Comment lines found:\n%s", fullComments,
			)
		}
	} else {
		// TMUX_TMPDIR is absent (correct). The comment block must still
		// document that this is intentional.
		if !hasTmuxTmpdirMention || !hasIntentionallyNotSet {
			t.Errorf(
				"scripts/init/orchard.service: TMUX_TMPDIR is absent from Environment=\n"+
					"(correct) but the comment block does not explain why.\n"+
					"Expected the comments to contain both \"TMUX_TMPDIR\" and \"intentionally NOT set\".\n"+
					"This documents the AC3 design decision for future maintainers.\n"+
					"Comment lines found:\n%s", fullComments,
			)
		}
	}
}

// TestShippedSystemdUnit_DocumentsCleanRestart asserts that the unit's comment
// block documents the clean-restart requirement when upgrading from a pre-#464
// install.
//
// After replacing the unit file, users MUST run `systemctl --user restart` (not
// just daemon-reload) so the running daemon's mount namespace re-binds to the
// host's /tmp. This comment is the primary user-facing guidance for the
// upgrade path.
func TestShippedSystemdUnit_DocumentsCleanRestart(t *testing.T) {
	t.Parallel()
	content := readUnitFile(t)

	allComments := extractAllCommentLines(content)
	lowerComments := strings.ToLower(allComments)

	hasRestart := strings.Contains(lowerComments, "restart")
	hasDaemonReload := strings.Contains(lowerComments, "daemon-reload")
	hasNamespace := strings.Contains(lowerComments, "namespace")
	hasTmp := strings.Contains(lowerComments, "/tmp")

	if !hasRestart {
		t.Errorf(
			"scripts/init/orchard.service: comment block does not mention \"restart\".\n"+
				"The upgrade path requires a clean restart after replacing the unit file —\n"+
				"document this so users know they must run `systemctl --user restart`.\n"+
				"Comment lines found:\n%s", allComments,
		)
	}

	if !hasDaemonReload && !hasNamespace && !hasTmp {
		t.Errorf(
			"scripts/init/orchard.service: comment block mentions \"restart\" but does\n"+
				"not mention \"daemon-reload\", \"namespace\", or \"/tmp\".\n"+
				"The comment must explain WHY a clean restart is required (mount namespace\n"+
				"rebinding so the process can see /tmp/tmux-<uid>/default).\n"+
				"Comment lines found:\n%s", allComments,
		)
	}
}

// extractAllCommentLines returns a single string containing all comment lines
// (lines beginning with '#') from the unit file, joined by newlines. This
// covers both the leading comment block and inline section comments.
func extractAllCommentLines(content string) string {
	var buf strings.Builder
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			buf.WriteString(trimmed)
			buf.WriteByte('\n')
		}
	}
	return buf.String()
}
