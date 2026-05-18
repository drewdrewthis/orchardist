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

// Adapter shells out to `ps` and `lsof`. Stateless — the Provider
// owns the cache and the watcher loop. L4: no script exec on the read
// path for field resolvers; adapter is only called from the provider's
// poll goroutine.
type Adapter struct {
	hostID       string
	pollInterval time.Duration
	runner       commandRunner
}

// commandRunner is the test seam. Production wires execRunner.
type commandRunner interface {
	Run(ctx context.Context, name string, args ...string) ([]byte, error)
}

type execRunner struct{}

func (execRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && len(out) > 0 {
			return out, fmt.Errorf("%s %v: %w", name, args, err)
		}
		return nil, fmt.Errorf("%s %v: %w", name, args, err)
	}
	return out, nil
}

// NewAdapter constructs an Adapter for the given host id.
func NewAdapter(hostID string) *Adapter {
	return &Adapter{
		hostID:       hostID,
		pollInterval: 3 * time.Second,
		runner:       execRunner{},
	}
}

// WithRunner returns a copy with the given runner (for tests).
func (a *Adapter) WithRunner(r commandRunner) *Adapter {
	cp := *a
	cp.runner = r
	return &cp
}

// WithPollInterval returns a copy with a new poll interval (for tests).
func (a *Adapter) WithPollInterval(d time.Duration) *Adapter {
	cp := *a
	cp.pollInterval = d
	return &cp
}

// HostID exposes the host prefix used in ProcessIDs.
func (a *Adapter) HostID() string { return a.hostID }

// PollInterval returns the configured tick rate.
func (a *Adapter) PollInterval() time.Duration { return a.pollInterval }

// FetchAll runs `ps -ax -o pid,ppid,user,tty,%cpu,rss,lstart,command` and
// parses every row into a Process keyed by ProcessID. Per O10: one syscall
// for the whole table.
func (a *Adapter) FetchAll(ctx context.Context) (map[ProcessID]Process, error) {
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

// FetchArgs returns argv for the given pids in one shellout (O10).
func (a *Adapter) FetchArgs(ctx context.Context, pids []int) (map[int][]string, error) {
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

// FetchCwds returns cwd for the given pids. macOS uses lsof; Linux
// returns an empty map until a /proc fallback is added.
func (a *Adapter) FetchCwds(ctx context.Context, pids []int) (map[int]string, error) {
	if len(pids) == 0 {
		return map[int]string{}, nil
	}
	if runtime.GOOS != "darwin" {
		// Linux fallback is a future PR. Return empty so resolvers degrade
		// gracefully rather than erroring.
		return map[int]string{}, nil
	}
	return a.fetchCwdsDarwin(ctx, pids)
}

// fetchCwdsDarwin issues a single `lsof -a -d cwd -p <pids>` shellout (O10).
func (a *Adapter) fetchCwdsDarwin(ctx context.Context, pids []int) (map[int]string, error) {
	out := make(map[int]string, len(pids))
	if len(pids) == 0 {
		return out, nil
	}

	parts := make([]string, len(pids))
	for i, pid := range pids {
		parts[i] = strconv.Itoa(pid)
	}
	pidArg := strings.Join(parts, ",")

	raw, err := a.runner.Run(ctx, "lsof", "-a", "-d", "cwd", "-p", pidArg, "-F", "n")
	// lsof exits non-zero when any pid is missing — treat as partial result.
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
				havePid = false
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
// changed since the last tick. Closes the returned channel when ctx is
// cancelled.
func (a *Adapter) Watch(ctx context.Context) (<-chan ProcessID, error) {
	out := make(chan ProcessID, 64)
	go func() {
		defer close(out)
		var prior map[ProcessID]Process
		ticker := time.NewTicker(a.pollInterval)
		defer ticker.Stop()
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
func (a *Adapter) tick(ctx context.Context, prior *map[ProcessID]Process, out chan<- ProcessID) {
	current, err := a.FetchAll(ctx)
	if err != nil {
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
func (a *Adapter) Close() error { return nil }

// splitLines yields each newline-terminated chunk of buf.
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
