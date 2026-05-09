package ps

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// PsAdapter shells out to `ps`. Stateless per ADR-011 §3 — the calling
// provider owns the cache and the watcher loop.
//
// macOS-only for v1 (per briefing "Out of scope: Linux"). The package
// builds on Linux (the parser is OS-agnostic) but the cwd resolver is
// stubbed there until a /proc-based fallback is added.
type PsAdapter struct {
	hostID string

	// pollInterval drives Watch's tick rate. Briefing AC2 fixes 3s.
	pollInterval time.Duration

	// runner is overridable for tests so we can inject a fake `ps`.
	runner CommandRunner
}

// CommandRunner is the test seam. Production wires execRunner.
type CommandRunner interface {
	Run(ctx context.Context, name string, args ...string) ([]byte, error)
}

// execRunner is the real implementation: it shells out via os/exec.
type execRunner struct{}

func (execRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.Output()
	if err != nil {
		// For commands like lsof that exit non-zero on partial results
		// (e.g. some requested pids no longer exist), the stdout bytes
		// are still meaningful. Return them alongside the error so
		// callers can choose whether to use partial output.
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && len(out) > 0 {
			return out, fmt.Errorf("%s %v: %w", name, args, err)
		}
		return nil, fmt.Errorf("%s %v: %w", name, args, err)
	}
	return out, nil
}

// NewAdapter constructs a PsAdapter for the given host id. The host id
// is the prefix of every ProcessID this adapter materialises.
func NewAdapter(hostID string) *PsAdapter {
	return &PsAdapter{
		hostID:       hostID,
		pollInterval: 3 * time.Second,
		runner:       execRunner{},
	}
}

// WithRunner returns a copy of a using the given runner. For tests.
func (a *PsAdapter) WithRunner(r CommandRunner) *PsAdapter {
	cp := *a
	cp.runner = r
	return &cp
}

// WithPollInterval returns a copy of a with a new poll interval. For
// tests that want a snappier loop.
func (a *PsAdapter) WithPollInterval(d time.Duration) *PsAdapter {
	cp := *a
	cp.pollInterval = d
	return &cp
}

// HostID exposes the host prefix used in ProcessIDs.
func (a *PsAdapter) HostID() string { return a.hostID }

// PollInterval returns the configured tick rate. The provider uses it
// to drive the Watch loop.
func (a *PsAdapter) PollInterval() time.Duration { return a.pollInterval }

// Fetch returns the Process for a single pid. Implemented as "FetchAll
// + lookup" because `ps -p` has the same overhead as `ps -ax` in
// practice and avoids a second parser path.
func (a *PsAdapter) Fetch(ctx context.Context, key ProcessID) (Process, error) {
	all, err := a.FetchAll(ctx)
	if err != nil {
		return Process{}, err
	}
	p, ok := all[key]
	if !ok {
		return Process{}, fmt.Errorf("ps: pid %d not found", key.PID)
	}
	return p, nil
}

// FetchAll runs `ps -ax -o pid,ppid,user,tty,%cpu,rss,lstart,command`
// and parses every row into a Process keyed by ProcessID. Errors only
// when ps fails to run or the output header is unrecognisable; transient
// per-line parse failures are silently dropped.
func (a *PsAdapter) FetchAll(ctx context.Context) (map[ProcessID]Process, error) {
	out, err := a.runner.Run(ctx, "ps", "-ax", "-o", "pid,ppid,user,tty,%cpu,rss,lstart,command")
	if err != nil {
		return nil, fmt.Errorf("ps: shell out: %w", err)
	}
	procs, err := parsePs(a.hostID, string(out))
	if err != nil {
		return nil, err
	}
	m := make(map[ProcessID]Process, len(procs))
	for _, p := range procs {
		m[p.ID] = p
	}
	return m, nil
}

// FetchArgs returns argv for the given pids in one shellout. macOS ps
// supports -wwax for unbounded line width — required because daemon
// argv routinely exceed the default 80-column wrap.
func (a *PsAdapter) FetchArgs(ctx context.Context, pids []int) (map[int][]string, error) {
	if len(pids) == 0 {
		return map[int][]string{}, nil
	}
	out, err := a.runner.Run(ctx, "ps", "-wwax", "-o", "pid,args")
	if err != nil {
		return nil, fmt.Errorf("ps args: shell out: %w", err)
	}
	all, err := parseArgs(string(out))
	if err != nil {
		return nil, err
	}
	// Filter to requested pids — keeps the response size proportional to
	// the resolver's actual ask, not the whole process table.
	want := make(map[int]struct{}, len(pids))
	for _, p := range pids {
		want[p] = struct{}{}
	}
	filtered := make(map[int][]string, len(pids))
	for pid, argv := range all {
		if _, ok := want[pid]; ok {
			filtered[pid] = argv
		}
	}
	return filtered, nil
}

// FetchCwds returns cwd for the given pids. macOS uses lsof per pid;
// Linux reads /proc/<pid>/cwd. v1 ships macOS only — Linux returns an
// empty map and no error so resolvers degrade gracefully.
func (a *PsAdapter) FetchCwds(ctx context.Context, pids []int) (map[int]string, error) {
	if len(pids) == 0 {
		return map[int]string{}, nil
	}
	if runtime.GOOS != "darwin" {
		// Linux fallback lives in a future PR. Surface "not implemented"
		// as an empty result rather than an error so a worktree on
		// Linux can still serve everything else.
		return map[int]string{}, nil
	}
	return a.fetchCwdsDarwin(ctx, pids)
}

// fetchCwdsDarwin issues a single `lsof -a -d cwd -p <pids>` shellout for
// all requested pids and parses the multi-pid -F output. macOS lsof
// accepts comma-separated pids via a single -p flag; combining the calls
// avoids N serial fork+exec on the request goroutine.
//
// pids that have exited or have no cwd entry produce no `n` line in the
// output and are simply absent from the returned map. lsof's exit code is
// non-zero when any pid is missing, but that's expected — we still parse
// what came through.
func (a *PsAdapter) fetchCwdsDarwin(ctx context.Context, pids []int) (map[int]string, error) {
	out := make(map[int]string, len(pids))
	if len(pids) == 0 {
		return out, nil
	}

	// Build comma-separated pid list: "123,456,789"
	parts := make([]string, len(pids))
	for i, pid := range pids {
		parts[i] = strconv.Itoa(pid)
	}
	pidArg := strings.Join(parts, ",")

	raw, err := a.runner.Run(ctx, "lsof", "-a", "-d", "cwd", "-p", pidArg, "-F", "n")
	// lsof exits non-zero when any pid is missing — treat as "partial result"
	// and parse whatever was emitted. Return (out, nil) so the resolver sees
	// nulls for missing pids rather than failing the whole batch.
	if err != nil && len(raw) == 0 {
		return out, nil
	}

	var (
		current int
		havePid bool
	)
	for _, line := range splitLines(raw) {
		if len(line) == 0 {
			continue
		}
		switch line[0] {
		case 'p':
			v, perr := strconv.Atoi(line[1:])
			if perr != nil {
				havePid = false // malformed pid header, skip until the next valid p<pid>
				continue
			}
			current = v
			havePid = true
		case 'n':
			if havePid && len(line) > 1 {
				out[current] = line[1:]
			}
		}
	}
	return out, nil
}

// Watch polls FetchAll on a.pollInterval and emits every key whose value
// changed since the last tick (additions, value-modifications, removals).
// Closes the returned channel when ctx is cancelled.
//
// The provider is the consumer; it diffs against its store and forwards
// the changed keys as InvalidationEvents. The adapter only returns keys
// that the *adapter* sees as changed in its own snapshot-of-snapshot
// — this duplicates a tiny amount of state, but keeping the diff inside
// the adapter lets it cope with tests that don't run a full provider.
func (a *PsAdapter) Watch(ctx context.Context) (<-chan ProcessID, error) {
	out := make(chan ProcessID, 64)
	go func() {
		defer close(out)
		var prior map[ProcessID]Process
		ticker := time.NewTicker(a.pollInterval)
		defer ticker.Stop()
		// Emit the initial snapshot immediately so a fresh subscriber
		// doesn't have to wait pollInterval for the first event.
		a.tick(ctx, &prior, out)
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				a.tick(ctx, &prior, out)
			}
		}
	}()
	return out, nil
}

// tick runs one poll cycle and pushes changed keys to out.
func (a *PsAdapter) tick(ctx context.Context, prior *map[ProcessID]Process, out chan<- ProcessID) {
	current, err := a.FetchAll(ctx)
	if err != nil {
		// Transient errors (shell exec failure, parser hiccup) are
		// logged at the provider layer; the adapter just skips this
		// tick rather than collapse the watcher.
		return
	}
	for k, v := range current {
		old, ok := (*prior)[k]
		if !ok || !processEqualsHotPath(old, v) {
			select {
			case out <- k:
			case <-ctx.Done():
				return
			}
		}
	}
	for k := range *prior {
		if _, ok := current[k]; !ok {
			select {
			case out <- k:
			case <-ctx.Done():
				return
			}
		}
	}
	*prior = current
}

// Close is a no-op for the ps adapter (no long-lived handles).
func (a *PsAdapter) Close() error { return nil }

// processEqualsHotPath compares the fields the watcher cares about for
// invalidation. CPU and memory shift on every poll for every running
// process; treating them as "always changed" would emit an event per
// pid per tick — useless noise for subscribers. We compare only the
// stable hot-path fields and let consumers re-fetch on demand.
func processEqualsHotPath(a, b Process) bool {
	return a.PPID == b.PPID &&
		a.User == b.User &&
		a.TTY == b.TTY &&
		a.Command == b.Command &&
		a.StartedRaw == b.StartedRaw
}

// splitLines yields each newline-terminated chunk of buf. Used by lsof
// -F parsing where stdlib bufio.Scanner is overkill.
func splitLines(buf []byte) []string {
	out := make([]string, 0, 4)
	start := 0
	for i, b := range buf {
		if b == '\n' {
			out = append(out, string(buf[start:i]))
			start = i + 1
		}
	}
	if start < len(buf) {
		out = append(out, string(buf[start:]))
	}
	return out
}
