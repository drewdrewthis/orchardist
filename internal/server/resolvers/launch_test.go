package resolvers

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSanitizeTmuxName(t *testing.T) {
	cases := map[string]string{
		"orchardist":     "orchardist",
		"new session 1":      "new-session-1",
		"feat/foo.bar:baz":   "feat-foo-bar-baz",
		"  padded  ":         "padded",
		"--leading-trailing-": "leading-trailing",
		"a..b__c":            "a-b__c",
		"":                   "",
		"...":                "",
	}
	for in, want := range cases {
		if got := sanitizeTmuxName(in); got != want {
			t.Errorf("sanitizeTmuxName(%q) = %q; want %q", in, got, want)
		}
	}
}

func TestShellSingleQuote(t *testing.T) {
	cases := map[string]string{
		"plain":          "'plain'",
		"with space":     "'with space'",
		"it's a trap":    `'it'\''s a trap'`,
		"a 'b' c":        `'a '\''b'\'' c'`,
		"$(rm -rf /)":    "'$(rm -rf /)'", // metachars stay literal inside single quotes
	}
	for in, want := range cases {
		if got := shellSingleQuote(in); got != want {
			t.Errorf("shellSingleQuote(%q) = %q; want %q", in, got, want)
		}
	}
}

func TestBuildClaudeLaunch(t *testing.T) {
	uuid := "11111111-2222-3333-4444-555555555555"

	got := buildClaudeLaunch(uuid, "my-sess", "", "")
	for _, want := range []string{
		"claude",
		"--session-id " + uuid,
		"--dangerously-skip-permissions",
		"--effort max",
		"--name 'my-sess'",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("buildClaudeLaunch base missing %q in %q", want, got)
		}
	}
	if strings.Contains(got, "--model") {
		t.Errorf("buildClaudeLaunch should omit --model when empty: %q", got)
	}

	withAll := buildClaudeLaunch(uuid, "s", "claude-sonnet-4-6", "do the thing")
	if !strings.Contains(withAll, "--model 'claude-sonnet-4-6'") {
		t.Errorf("expected --model in %q", withAll)
	}
	// Prompt is the trailing positional, single-quoted.
	if !strings.HasSuffix(withAll, "'do the thing'") {
		t.Errorf("expected prompt as trailing positional in %q", withAll)
	}

	// A prompt with a single quote must stay shell-safe.
	tricky := buildClaudeLaunch(uuid, "s", "", "don't break")
	if !strings.HasSuffix(tricky, `'don'\''t break'`) {
		t.Errorf("prompt single-quote not escaped: %q", tricky)
	}
}

func TestResolveLaunchDir(t *testing.T) {
	if _, err := resolveLaunchDir(""); err == nil {
		t.Error("expected error for empty cwd")
	}
	if _, err := resolveLaunchDir("/no/such/dir/orchard-xyz"); err == nil {
		t.Error("expected error for missing dir")
	}

	// A real file is not a directory.
	f, err := os.CreateTemp(t.TempDir(), "notadir")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := resolveLaunchDir(f.Name()); err == nil {
		t.Errorf("expected error for non-directory %q", f.Name())
	}

	// A real dir resolves and round-trips.
	dir := t.TempDir()
	got, err := resolveLaunchDir("  " + dir + "  ")
	if err != nil {
		t.Fatalf("resolveLaunchDir(%q) err: %v", dir, err)
	}
	if got != dir {
		t.Errorf("resolveLaunchDir trimmed = %q; want %q", got, dir)
	}

	// ~ expansion lands under $HOME.
	home, err := os.UserHomeDir()
	if err == nil {
		got, err := resolveLaunchDir("~")
		if err != nil {
			t.Fatalf("resolveLaunchDir(\"~\") err: %v", err)
		}
		if got != home {
			t.Errorf("resolveLaunchDir(\"~\") = %q; want %q", got, home)
		}
		// ~/sub expands too (use an existing subdir).
		sub := filepath.Join(home, ".claude")
		if fi, statErr := os.Stat(sub); statErr == nil && fi.IsDir() {
			if got, err := resolveLaunchDir("~/.claude"); err != nil || got != sub {
				t.Errorf("resolveLaunchDir(\"~/.claude\") = %q, %v; want %q", got, err, sub)
			}
		}
	}
}
