package repodiscovery

import (
	"bytes"
	"context"
	"os/exec"
	"strings"
)

// Runner is the exec seam — production wires [execRunner]; tests pass a
// fake. Mirrors the same shape used by the tmux provider so test
// fixtures translate between packages.
type Runner interface {
	Run(ctx context.Context, name string, args ...string) ([]byte, error)
}

type execRunner struct{}

// Run executes name+args and returns combined stdout. Stderr is
// discarded; discovery is opportunistic and noisy errors from a missing
// tmux server would drown out the logs.
func (execRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		return nil, err
	}
	return stdout.Bytes(), nil
}

// TmuxSource discovers candidate repo CWDs by querying every tmux pane
// on the local host for `pane_current_path`. The query runs as a single
// `tmux list-panes -a` exec — the same coalesced shape the main tmux
// provider uses for snapshots (issue #464). When no tmux server is
// running, Roots returns an empty slice (not an error) so the [Provider]
// keeps merging the other sources.
type TmuxSource struct {
	runner Runner
}

// NewTmuxSource returns a TmuxSource using the real `tmux` binary.
func NewTmuxSource() *TmuxSource {
	return &TmuxSource{runner: execRunner{}}
}

// WithRunner swaps the exec seam. Tests inject a fake that emits
// canned pane lists. Returns a new TmuxSource so chained constructors
// stay value-safe.
func (s *TmuxSource) WithRunner(r Runner) *TmuxSource {
	cp := *s
	cp.runner = r
	return &cp
}

// Roots queries `tmux list-panes -a -F '#{pane_current_path}'` and
// returns one entry per non-empty line. The output is not deduplicated
// here — duplicates fold out at the [Provider] layer once each path has
// been walked to its repo root.
//
// A missing tmux server (the binary is installed but no daemon is
// running) returns an empty slice and a nil error. Genuine exec
// failures (binary missing) are also swallowed; the daemon must not
// fail-closed on an opportunistic discovery source.
func (s *TmuxSource) Roots(ctx context.Context) ([]string, error) {
	out, err := s.runner.Run(ctx, "tmux", "list-panes", "-a", "-F", "#{pane_current_path}")
	if err != nil {
		return nil, nil
	}
	out = bytes.TrimRight(out, "\n")
	if len(out) == 0 {
		return nil, nil
	}
	lines := strings.Split(string(out), "\n")
	roots := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		roots = append(roots, line)
	}
	return roots, nil
}
