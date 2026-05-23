//go:build darwin

package hostservices

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// macAdapter shells `launchctl list <Label>` per watched name.
//
// State mapping:
//
//	PID present + LastExitStatus 0   → active
//	PID absent  + LastExitStatus 0   → inactive (clean stop)
//	PID absent  + LastExitStatus != 0 → failed
//	Unit not loaded → exit code != 0 + stderr "Could not find service" → not_installed
//	Anything else (parse error, unrecognised stderr) → unknown
type macAdapter struct {
	commander   launchctlCommander
	psCommander psRunner
}

// launchctlCommander is the indirection that lets tests stub PATH-based
// launchctl without needing real launchd.
type launchctlCommander interface {
	Run(ctx context.Context, args ...string) (stdout, stderr []byte, exitCode int, err error)
}

// psRunner reads a single line of `ps -p <pid> -o lstart=` output.
type psRunner interface {
	LStart(ctx context.Context, pid int) (time.Time, error)
}

// newAdapter returns the macOS adapter wired to on-PATH launchctl and ps.
func newAdapter() adapter {
	return macAdapter{
		commander:   execCommander{bin: "launchctl"},
		psCommander: psExec{},
	}
}

// execCommander runs a binary located via $PATH. Returns
// ErrServiceManagerMissing when the binary is missing from PATH.
type execCommander struct{ bin string }

func (e execCommander) Run(ctx context.Context, args ...string) ([]byte, []byte, int, error) {
	if _, err := exec.LookPath(e.bin); err != nil {
		return nil, nil, 0, ErrServiceManagerMissing
	}
	cmd := exec.CommandContext(ctx, e.bin, args...)
	var outBuf, errBuf strings.Builder
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	err := cmd.Run()
	exitCode := 0
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
			err = nil
		}
	}
	return []byte(outBuf.String()), []byte(errBuf.String()), exitCode, err
}

// psExec is the production psRunner — `ps -p <pid> -o lstart=`.
type psExec struct{}

func (psExec) LStart(ctx context.Context, pid int) (time.Time, error) {
	if _, err := exec.LookPath("ps"); err != nil {
		return time.Time{}, fmt.Errorf("ps not on PATH: %w", err)
	}
	cmd := exec.CommandContext(ctx, "ps", "-p", strconv.Itoa(pid), "-o", "lstart=")
	out, err := cmd.Output()
	if err != nil {
		return time.Time{}, fmt.Errorf("ps -p %d: %w", pid, err)
	}
	return parsePsLStart(string(out))
}

// parsePsLStart parses a `ps -o lstart=` line. macOS/BSD ps uses two
// whitespace runs around single-digit days; strings.Fields collapses them.
func parsePsLStart(out string) (time.Time, error) {
	line := strings.TrimSpace(out)
	if line == "" {
		return time.Time{}, fmt.Errorf("ps lstart: empty output")
	}
	collapsed := strings.Join(strings.Fields(line), " ")
	t, err := time.ParseInLocation("Mon Jan 2 15:04:05 2006", collapsed, time.Local)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse %q: %w", line, err)
	}
	return t, nil
}

// fetchOne calls `launchctl list <Label>` and parses the dictionary into
// a HostServiceSnapshot. Implements the adapter interface.
func (m macAdapter) fetchOne(ctx context.Context, machineID, name string) (HostServiceSnapshot, error) {
	label := nameToLabel(name)
	stdout, stderr, code, err := m.commander.Run(ctx, "list", label)
	if errors.Is(err, ErrServiceManagerMissing) {
		return HostServiceSnapshot{}, ErrServiceManagerMissing
	}
	if err != nil {
		return HostServiceSnapshot{}, fmt.Errorf("launchctl list %s: %w", label, err)
	}

	if code != 0 {
		if isUnknownLabelMessage(string(stderr)) {
			return HostServiceSnapshot{
				MachineID: machineID,
				Name:      name,
				State:     StateNotInstalled,
			}, nil
		}
		return HostServiceSnapshot{
			MachineID: machineID,
			Name:      name,
			State:     StateUnknown,
		}, nil
	}

	state, pid, exitCode, err := parseLaunchctlList(string(stdout))
	if err != nil {
		return HostServiceSnapshot{
			MachineID: machineID,
			Name:      name,
			State:     StateUnknown,
		}, nil
	}

	snap := HostServiceSnapshot{
		MachineID: machineID,
		Name:      name,
		State:     state,
	}
	if exitCode != nil {
		snap.ExitCode = exitCode
	}
	if state == StateActive && pid != nil {
		var since time.Time
		if m.psCommander != nil {
			if t, perr := m.psCommander.LStart(ctx, *pid); perr == nil {
				since = t
			}
		}
		if since.IsZero() {
			since = time.Now()
		}
		snap.Since = &since
	}
	return snap, nil
}

// parseLaunchctlList extracts State, PID, and ExitCode from the
// `launchctl list <Label>` dictionary block.
func parseLaunchctlList(out string) (State, *int, *int, error) {
	pid, lastExit, err := scanLaunchctlPairs(out)
	if err != nil {
		return "", nil, nil, err
	}
	switch {
	case pid != nil:
		return StateActive, pid, nil, nil
	case lastExit != nil && *lastExit == 0:
		zero := 0
		return StateInactive, nil, &zero, nil
	case lastExit != nil && *lastExit != 0:
		return StateFailed, nil, lastExit, nil
	default:
		return StateInactive, nil, nil, nil
	}
}

func scanLaunchctlPairs(out string) (pid *int, lastExit *int, err error) {
	for _, raw := range strings.Split(out, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || line == "{" || line == "}" || line == "};" {
			continue
		}
		key, value, ok := splitLaunchctlPair(line)
		if !ok {
			continue
		}
		switch key {
		case "PID":
			if n, parseErr := strconv.Atoi(value); parseErr == nil {
				pid = &n
			}
		case "LastExitStatus":
			n, parseErr := strconv.Atoi(value)
			if parseErr != nil {
				return nil, nil, fmt.Errorf("LastExitStatus %q: %w", value, parseErr)
			}
			lastExit = &n
		}
	}
	return pid, lastExit, nil
}

func splitLaunchctlPair(line string) (key, value string, ok bool) {
	eq := strings.Index(line, "=")
	if eq < 0 {
		return "", "", false
	}
	k := strings.TrimSpace(line[:eq])
	v := strings.TrimSpace(line[eq+1:])
	v = strings.TrimSuffix(v, ";")
	v = strings.TrimSpace(v)
	k = strings.Trim(k, `"`)
	v = strings.Trim(v, `"`)
	if k == "" {
		return "", "", false
	}
	return k, v, true
}

func isUnknownLabelMessage(stderr string) bool {
	s := strings.ToLower(stderr)
	return strings.Contains(s, "could not find service") ||
		strings.Contains(s, "service not loaded") ||
		strings.Contains(s, "no such process")
}
