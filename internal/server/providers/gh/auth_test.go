package gh_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/drewdrewthis/git-orchard-rs/internal/server/providers/gh"
)

// TestCommandAuthSource_NoBinary asserts that a missing `gh` binary
// surfaces ErrGHNotInstalled — the typed error the resolver layer
// translates into a per-field GraphQL error.
func TestCommandAuthSource_NoBinary(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("PATH-substituted shellout test is POSIX-only")
	}
	t.Setenv("PATH", "")
	src := gh.NewCommandAuthSource()
	_, err := src.Token(context.Background())
	if !errors.Is(err, gh.ErrGHNotInstalled) {
		t.Errorf("err = %v, want ErrGHNotInstalled", err)
	}
}

// TestCommandAuthSource_NotAuthed shells out to a fake `gh` that exits
// 1 (mimicking `gh auth token` when nobody is logged in). The provider
// must wrap that into ErrNotAuthenticated.
func TestCommandAuthSource_NotAuthed(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("PATH-substituted shellout test is POSIX-only")
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "gh")
	body := `#!/bin/sh
echo "You are not logged into any GitHub hosts" 1>&2
exit 1
`
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)

	src := gh.NewCommandAuthSource()
	_, err := src.Token(context.Background())
	if !errors.Is(err, gh.ErrNotAuthenticated) {
		t.Errorf("err = %v, want ErrNotAuthenticated", err)
	}
}

// TestCommandAuthSource_HappyPath drives the success path — a fake `gh`
// that prints a token to stdout. The provider must return that token
// verbatim with leading/trailing whitespace stripped.
func TestCommandAuthSource_HappyPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("PATH-substituted shellout test is POSIX-only")
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "gh")
	body := `#!/bin/sh
echo "  ghp_AAAA1111  "
exit 0
`
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)

	src := gh.NewCommandAuthSource()
	token, err := src.Token(context.Background())
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if token != "ghp_AAAA1111" {
		t.Errorf("token = %q, want ghp_AAAA1111 (whitespace stripped)", token)
	}
}

// TestStaticAuthSource asserts the trivial test seam.
func TestStaticAuthSource(t *testing.T) {
	t.Run("happy", func(t *testing.T) {
		s := &gh.StaticAuthSource{TokenValue: "x"}
		got, err := s.Token(context.Background())
		if err != nil || got != "x" {
			t.Errorf("got %q, %v; want x, nil", got, err)
		}
	})
	t.Run("empty token → not authed", func(t *testing.T) {
		s := &gh.StaticAuthSource{}
		_, err := s.Token(context.Background())
		if !errors.Is(err, gh.ErrNotAuthenticated) {
			t.Errorf("err = %v, want ErrNotAuthenticated", err)
		}
	})
	t.Run("explicit error wins", func(t *testing.T) {
		boom := errors.New("boom")
		s := &gh.StaticAuthSource{Err: boom}
		_, err := s.Token(context.Background())
		if !errors.Is(err, boom) {
			t.Errorf("err = %v, want boom", err)
		}
	})
}
