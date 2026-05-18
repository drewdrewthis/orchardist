package claudeaccount

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

// ShellAdapter implements the raw I/O for the claude-account domain by
// shelling out to the local `claude` and `ccusage` CLIs.
//
// Stateless — the Provider above owns the cache and the poll loop. Each
// Fetch / FetchAll triggers fresh shellouts; throttling is the Provider's
// responsibility (L4).
//
// runner is overridable for tests so we can avoid touching a real binary.
// Production wires execRunner.
//
// PII: MUST NOT log raw stdout — `claude auth status` emits the user's email.
type ShellAdapter struct {
	hostID string
	logger *slog.Logger
	runner CommandRunner
}

// CommandRunner is the test seam. Production wires execRunner, which kills
// the entire process group on context cancellation. Tests inject a stub.
type CommandRunner interface {
	Run(ctx context.Context, name string, args ...string) ([]byte, error)
}

// execRunner is the real implementation. It uses exec.CommandContext with a
// Cancel that sends SIGKILL to the entire process group so helpers forked by
// `bunx ccusage` don't leak children when the daemon's context is cancelled.
type execRunner struct{}

// Run shells out and returns merged stdout. On context cancel, SIGKILL is
// sent to the negative pid (the process group).
func (execRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	out, err := cmd.Output()
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return nil, &ToolNotInstalledError{Tool: name}
		}
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return nil, fmt.Errorf("%s exited %d: %s", name, ee.ExitCode(), strings.TrimSpace(string(ee.Stderr)))
		}
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	return out, nil
}

// NewShellAdapter constructs a ShellAdapter rooted at the given host id.
// Logger may be nil; defaults to slog.Default().
func NewShellAdapter(hostID string, logger *slog.Logger) *ShellAdapter {
	if logger == nil {
		logger = slog.Default()
	}
	return &ShellAdapter{
		hostID: hostID,
		logger: logger,
		runner: execRunner{},
	}
}

// HostID exposes the host prefix every AccountID this adapter mints will carry.
func (a *ShellAdapter) HostID() string { return a.hostID }

// WithRunner returns a copy of a using the given runner. For tests only.
func (a *ShellAdapter) WithRunner(r CommandRunner) *ShellAdapter {
	cp := *a
	cp.runner = r
	return &cp
}

// Fetch returns the Account for one AccountID. v1 only knows the local
// account; delegates to FetchAll.
func (a *ShellAdapter) Fetch(ctx context.Context, id AccountID) (Account, error) {
	all, err := a.FetchAll(ctx)
	if err != nil {
		return Account{}, err
	}
	acc, ok := all[id]
	if !ok {
		return Account{}, fmt.Errorf("claudeaccount: account %s not found", id.GraphQLID())
	}
	return acc, nil
}

// FetchAll runs `claude auth status --json` and `ccusage blocks --json`
// and merges the results into the single Account v1 surfaces.
//
// Degrades gracefully when ccusage is missing: returns a quota-less Account
// plus the error so the resolver can render a per-field GraphQL error for
// quota fields without blanking the email field.
func (a *ShellAdapter) FetchAll(ctx context.Context) (map[AccountID]Account, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	email, err := a.fetchEmail(ctx)
	if err != nil {
		return nil, err
	}
	id := AccountID{HostID: a.hostID, Email: email}

	acc := Account{
		ID:             id,
		QuotaEstimated: true,
	}

	used, cap_, resets, qErr := a.fetchQuota(ctx)
	if qErr != nil {
		return map[AccountID]Account{id: acc}, qErr
	}
	acc.QuotaUsed = used
	acc.QuotaCap = cap_
	acc.QuotaResetsAt = resets

	return map[AccountID]Account{id: acc}, nil
}

// RunPassThrough executes an arbitrary `claude` or `ccusage` invocation and
// returns the combined stdout/stderr as raw bytes. Used only by the
// pass-through resolver; PII rules still apply (caller must not log output).
func (a *ShellAdapter) RunPassThrough(ctx context.Context, tool string, args []string) ([]byte, error) {
	return a.runner.Run(ctx, tool, args...)
}

// authStatus is the JSON shape from `claude auth status --json`.
// Tries well-known field locations in order; stops at first non-empty match.
type authStatus struct {
	Email   string `json:"email"`
	Whoami  string `json:"whoami"`
	Account struct {
		Email string `json:"email"`
	} `json:"account"`
}

// fetchEmail runs `claude auth status --json` and returns the email field.
func (a *ShellAdapter) fetchEmail(ctx context.Context) (string, error) {
	out, err := a.runner.Run(ctx, "claude", "auth", "status", "--json")
	if err != nil {
		return "", err
	}
	var s authStatus
	if err := json.Unmarshal(out, &s); err != nil {
		return "", fmt.Errorf("claudeaccount: parse auth status: %w", err)
	}
	switch {
	case s.Email != "":
		return s.Email, nil
	case s.Account.Email != "":
		return s.Account.Email, nil
	case s.Whoami != "":
		return s.Whoami, nil
	default:
		return "", nil
	}
}

// ccusageBlocks is the JSON shape from `ccusage blocks --json`.
type ccusageBlocks struct {
	Blocks []struct {
		Active    bool     `json:"active"`
		Used      *float64 `json:"used"`
		Cap       *float64 `json:"cap"`
		Limit     *float64 `json:"limit"`
		ResetsAt  string   `json:"resetsAt"`
		ResetTime string   `json:"resetTime"`
	} `json:"blocks"`
}

// fetchQuota runs `ccusage blocks --json` and returns the (used, cap, resetsAt)
// triple for the currently active block.
func (a *ShellAdapter) fetchQuota(ctx context.Context) (*float64, *float64, *time.Time, error) {
	out, err := a.runner.Run(ctx, "ccusage", "blocks", "--json")
	if err != nil {
		return nil, nil, nil, err
	}
	var c ccusageBlocks
	if err := json.Unmarshal(out, &c); err != nil {
		return nil, nil, nil, fmt.Errorf("claudeaccount: parse ccusage blocks: %w", err)
	}
	for _, b := range c.Blocks {
		if !b.Active {
			continue
		}
		cap_ := b.Cap
		if cap_ == nil {
			cap_ = b.Limit
		}
		var resets *time.Time
		if t, err := parseResets(b.ResetsAt, b.ResetTime); err == nil && !t.IsZero() {
			tt := t
			resets = &tt
		}
		return b.Used, cap_, resets, nil
	}
	return nil, nil, nil, nil
}

// parseResets accepts either field name from ccusage and returns the first
// one that parses as RFC 3339.
func parseResets(primary, fallback string) (time.Time, error) {
	for _, raw := range []string{primary, fallback} {
		if raw == "" {
			continue
		}
		if t, err := time.Parse(time.RFC3339, raw); err == nil {
			return t, nil
		}
	}
	return time.Time{}, nil
}
