package gh

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
)

// AuthSource lets the provider learn its bearer token without
// committing to a specific subprocess. The default impl shells out to
// `gh auth token`; tests inject a stub so they don't need a real `gh`
// binary on PATH.
type AuthSource interface {
	// Token reads the current token. Implementations cache or pass
	// through as they see fit; the provider calls this once at boot.
	Token(ctx context.Context) (string, error)
}

// CommandAuthSource shells out to the `gh` CLI. The command is
// configurable so tests can swap it for a fake binary on PATH (see
// gh_e2e_test.go's PATH substitution trick).
//
// `gh auth token` writes the bearer token to stdout and exits 0 when
// the user is logged in. Any non-zero exit code is mapped to
// ErrNotAuthenticated; an exec.LookPath failure → ErrGHNotInstalled.
type CommandAuthSource struct {
	// Command is the executable name resolved against PATH; defaults
	// to "gh" via NewCommandAuthSource.
	Command string
	// Args is the arg vector. Defaults to {"auth", "token"}.
	Args []string
}

// NewCommandAuthSource returns the production AuthSource — `gh auth
// token` resolved against PATH.
func NewCommandAuthSource() *CommandAuthSource {
	return &CommandAuthSource{
		Command: "gh",
		Args:    []string{"auth", "token"},
	}
}

// Token implements AuthSource.
func (s *CommandAuthSource) Token(ctx context.Context) (string, error) {
	cmd := s.Command
	if cmd == "" {
		cmd = "gh"
	}
	args := s.Args
	if len(args) == 0 {
		args = []string{"auth", "token"}
	}

	// Resolve the binary first so a missing-gh setup gets a clean,
	// actionable error instead of "exec: \"gh\": executable file not
	// found in $PATH" leaking up the resolver chain.
	if _, err := exec.LookPath(cmd); err != nil {
		return "", ErrGHNotInstalled
	}

	c := exec.CommandContext(ctx, cmd, args...)
	var stdout, stderr bytes.Buffer
	c.Stdout = &stdout
	c.Stderr = &stderr
	if err := c.Run(); err != nil {
		// gh exits non-zero when the user is not logged in, prints a
		// "You are not logged into any GitHub hosts" message on stderr.
		// Either way, surface the typed error so the resolver layer
		// can return per-field GraphQL errors.
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return "", fmt.Errorf("%w: %s", ErrNotAuthenticated, strings.TrimSpace(stderr.String()))
		}
		return "", fmt.Errorf("run gh auth token: %w", err)
	}

	token := strings.TrimSpace(stdout.String())
	if token == "" {
		return "", ErrNotAuthenticated
	}
	return token, nil
}

// StaticAuthSource returns a fixed token. Useful in tests that already
// have the token in hand and don't want to round-trip through a fake
// binary.
type StaticAuthSource struct {
	TokenValue string
	Err        error
}

// Token implements AuthSource.
func (s *StaticAuthSource) Token(_ context.Context) (string, error) {
	if s.Err != nil {
		return "", s.Err
	}
	if s.TokenValue == "" {
		return "", ErrNotAuthenticated
	}
	return s.TokenValue, nil
}

// authState holds the cached token + the error from the most recent
// resolution attempt. Once set, the Provider treats it as immutable for
// the daemon's lifetime — re-auth is not in v1 scope.
type authState struct {
	token string
	err   error
}

// authCache memoises an AuthSource across calls. The provider calls
// this once at Start; subsequent gets are O(1).
type authCache struct {
	source AuthSource
	once   sync.Once
	state  authState
}

func newAuthCache(s AuthSource) *authCache {
	if s == nil {
		s = NewCommandAuthSource()
	}
	return &authCache{source: s}
}

// Resolve runs the underlying source on first call and caches the
// result. Subsequent calls are lock-free.
func (c *authCache) Resolve(ctx context.Context) (string, error) {
	c.once.Do(func() {
		t, err := c.source.Token(ctx)
		c.state = authState{token: t, err: err}
	})
	return c.state.token, c.state.err
}
