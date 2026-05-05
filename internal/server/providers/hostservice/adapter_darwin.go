//go:build darwin

package hostservice

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
// `launchctl list <Label>` output is a plist-like text dictionary:
//
//	{
//		"LimitLoadToSessionType" = "Aqua";
//		"Label" = "ai.example.test";
//		"OnDemand" = false;
//		"LastExitStatus" = 0;
//		"PID" = 12345;
//	};
//
// PID present + LastExitStatus 0 → active.
// PID absent + LastExitStatus 0 → inactive (clean stop).
// PID absent + LastExitStatus != 0 → failed.
// Unit not loaded → exit code != 0 + stderr "Could not find service" → state=not_installed.
// Anything else (parse error, unrecognised stderr) → state=unknown.
//
// `launchctl print` would give us a richer record (start time, last exit
// reason) but it requires a domain target (gui/<uid>/<label>) and is far
// chattier — `list` is enough for v1's surface.
type macAdapter struct {
	commander launchctlCommander
	// psCommander reads `ps -p <pid> -o lstart=` to recover the process
	// start time when the unit is active. launchctl itself does not
	// surface a per-unit start timestamp, so we lift the wall-clock
	// `lstart` from ps for `since`.
	psCommander psRunner
}

// launchctlCommander is the indirection that lets tests stub PATH-based
// `launchctl` without needing real launchd. Production wires the OS-
// resolved binary; tests inject a fake.
type launchctlCommander interface {
	Run(ctx context.Context, args ...string) (stdout, stderr []byte, exitCode int, err error)
}

// psRunner reads a single line of `ps -p <pid> -o lstart=` output. Split
// from launchctlCommander so the production wiring resolves a different
// PATH binary (`ps`) and tests can stub the start time independently.
type psRunner interface {
	LStart(ctx context.Context, pid int) (time.Time, error)
}

// NewAdapter returns the macOS Adapter wired to the on-PATH launchctl
// and ps binaries. Tests should construct macAdapter{...} directly.
func NewAdapter() Adapter {
	return macAdapter{
		commander:   execCommander{bin: "launchctl"},
		psCommander: psExec{},
	}
}

// execCommander runs a binary located via $PATH. If the binary is
// missing it returns ErrServiceManagerMissing so the caller can surface
// the configured per-field GraphQL error.
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

// psExec is the production psRunner — `ps -p <pid> -o lstart=` returns
// the wall-clock start time as a single line, e.g.
// `Mon May  4 11:36:02 2026`. ps prints with a trailing newline; the
// caller trims and time.Parse-es. ps missing from PATH yields an error
// so the FetchOne caller can fall back to time.Now() — the unit is up
// right now, so "now" is the truthful upper bound for `since`.
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

// parsePsLStart parses a single `ps -o lstart=` line. macOS / BSD ps
// uses two whitespace runs around single-digit days
// (`Mon May  4 11:36:02 2026`), so we collapse runs before parsing.
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

// FetchOne calls `launchctl list <Label>` and parses the dictionary
// output into a Snapshot. See type docs for the state-mapping table.
func (m macAdapter) FetchOne(ctx context.Context, hostID, name string) (Snapshot, error) {
	label := nameToLabel(name)
	stdout, stderr, code, err := m.commander.Run(ctx, "list", label)
	if errors.Is(err, ErrServiceManagerMissing) {
		return Snapshot{}, ErrServiceManagerMissing
	}
	if err != nil {
		return Snapshot{}, fmt.Errorf("launchctl list %s: %w", label, err)
	}

	if code != 0 {
		// launchctl returns non-zero for absent Labels with stderr
		// "Could not find service ...". Treat as state=not_installed
		// so the resolver can keep going on other names.
		if isUnknownLabelMessage(string(stderr)) {
			return Snapshot{
				HostID: hostID,
				Name:   name,
				State:  StateNotInstalled,
			}, nil
		}
		// launchctl returned a non-zero exit with stderr we don't
		// recognise. Surface as state=unknown rather than erroring —
		// the resolver should keep going for peers, and the operator
		// sees that we hit *something* unexpected without the daemon
		// crashing the field.
		return Snapshot{
			HostID: hostID,
			Name:   name,
			State:  StateUnknown,
		}, nil
	}

	state, pid, exitCode, err := parseLaunchctlList(string(stdout))
	if err != nil {
		// Parse failure on a successful exit: the dictionary shape
		// drifted (a future macOS release reformatted the output, or
		// a third-party launchctl is on PATH). Same logic as above —
		// state=unknown, no error, peers keep resolving.
		return Snapshot{
			HostID: hostID,
			Name:   name,
			State:  StateUnknown,
		}, nil
	}
	snap := Snapshot{
		HostID: hostID,
		Name:   name,
		State:  state,
	}
	if exitCode != nil {
		snap.ExitCode = exitCode
	}
	if state == StateActive && pid != nil {
		// `launchctl list` omits a start timestamp; lift `lstart` from
		// `ps -p <pid> -o lstart=` instead. ps unavailable / pid
		// already reaped → fall back to "now" so the resolver still
		// returns a non-null `since` (the GraphQL contract requires a
		// timestamp for active units, and the unit is verifiably up at
		// FetchOne time).
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
	// logTail is nil — `launchctl` does not emit a per-unit log stream
	// comparable to journalctl.
	return snap, nil
}

// parseLaunchctlList extracts the State + PID + ExitCode from a
// `launchctl list <Label>` dictionary block.
//
// Mapping:
//
//	PID present  + LastExitStatus 0   → active.
//	PID absent   + LastExitStatus 0   → inactive (clean stop or never run).
//	PID absent   + LastExitStatus != 0 → failed.
//
// Returned `pid` is non-nil only when the dictionary carried a numeric
// PID — caller uses it for the ps lstart shellout that fills `since`.
func parseLaunchctlList(out string) (State, *int, *int, error) {
	pid, lastExit, err := scanLaunchctlPairs(out)
	if err != nil {
		return "", nil, nil, err
	}

	switch {
	case pid != nil:
		// LastExitStatus from a previous run is irrelevant once the unit
		// is running again. Surface the previous code only when the unit
		// is not currently active.
		return StateActive, pid, nil, nil
	case lastExit != nil && *lastExit == 0:
		zero := 0
		return StateInactive, nil, &zero, nil
	case lastExit != nil && *lastExit != 0:
		return StateFailed, nil, lastExit, nil
	default:
		// No PID, no LastExitStatus — the unit is loaded but has never run.
		return StateInactive, nil, nil, nil
	}
}

// scanLaunchctlPairs walks the dictionary lines and pulls out the two
// fields we care about. Tolerant of arbitrary key order, indentation,
// trailing semicolons, and quote styles.
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
			// PID appears only when the unit is running. Any numeric
			// value indicates a live process; -1 / "no value" is
			// represented by omission.
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

// splitLaunchctlPair pulls "Key" = value; into (key, value, true). Each
// half is trimmed of surrounding quotes and whitespace.
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

// isUnknownLabelMessage detects launchctl's "the requested service does
// not exist" surface. macOS phrases it as "Could not find service ..."
// (older) or "Service not loaded" (rare). Matching is loose on purpose.
func isUnknownLabelMessage(stderr string) bool {
	s := strings.ToLower(stderr)
	if strings.Contains(s, "could not find service") {
		return true
	}
	if strings.Contains(s, "service not loaded") {
		return true
	}
	if strings.Contains(s, "no such process") {
		return true
	}
	return false
}
