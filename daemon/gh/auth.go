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

// AuthSource lets the provider learn its bearer token without committing
// to a specific subprocess. The default impl shells out to `gh auth token`;
// tests inject a stub.
type AuthSource interface {
	Token(ctx context.Context) (string, error)
}

// CommandAuthSource shells out to the `gh` CLI.
// The Command and Args fields are configurable so tests can swap them.
type CommandAuthSource struct {
	Command string
	Args    []string
}

// NewCommandAuthSource returns the production AuthSource — `gh auth token`.
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

	if _, err := exec.LookPath(cmd); err != nil {
		return "", ErrGHNotInstalled
	}

	c := exec.CommandContext(ctx, cmd, args...)
	var stdout, stderr bytes.Buffer
	c.Stdout = &stdout
	c.Stderr = &stderr
	if err := c.Run(); err != nil {
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
// have the token in hand.
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

// authCache memoises an AuthSource across calls. The provider calls
// Resolve once at Start; subsequent gets are O(1).
type authCache struct {
	source AuthSource
	once   sync.Once
	state  authState
}

type authState struct {
	token string
	err   error
}

func newAuthCache(s AuthSource) *authCache {
	if s == nil {
		s = NewCommandAuthSource()
	}
	return &authCache{source: s}
}

// Resolve runs the underlying source on first call and caches the result.
// Subsequent calls are lock-free.
func (c *authCache) Resolve(ctx context.Context) (string, error) {
	c.once.Do(func() {
		t, err := c.source.Token(ctx)
		c.state = authState{token: t, err: err}
	})
	return c.state.token, c.state.err
}
