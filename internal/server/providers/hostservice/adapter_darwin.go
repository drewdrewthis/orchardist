//go:build darwin

package hostservice

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
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
// Unknown unit → exit code != 0 + stderr "Could not find service" → state=unknown.
//
// `launchctl print` would give us a richer record (start time, last exit
// reason) but it requires a domain target (gui/<uid>/<label>) and is far
// chattier — `list` is enough for v1's four-state surface.
type macAdapter struct {
	commander launchctlCommander
}

// launchctlCommander is the indirection that lets tests stub PATH-based
// `launchctl` without needing real launchd. Production wires the OS-
// resolved binary; tests inject a fake.
type launchctlCommander interface {
	Run(ctx context.Context, args ...string) (stdout, stderr []byte, exitCode int, err error)
}

// NewAdapter returns the macOS Adapter wired to the on-PATH launchctl.
// Tests should construct macAdapter{commander: ...} directly.
func NewAdapter() Adapter { return macAdapter{commander: execCommander{bin: "launchctl"}} }

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
		// launchctl returns non-zero for unknown labels with stderr
		// "Could not find service ...". Treat as state=unknown rather
		// than an error so the resolver can keep going on other names.
		if isUnknownLabelMessage(string(stderr)) {
			return Snapshot{
				HostID: hostID,
				Name:   name,
				State:  StateUnknown,
			}, nil
		}
		return Snapshot{}, fmt.Errorf("launchctl list %s exited %d: %s", label, code, strings.TrimSpace(string(stderr)))
	}

	state, exitCode, err := parseLaunchctlList(string(stdout))
	if err != nil {
		return Snapshot{}, fmt.Errorf("parse launchctl list %s: %w", label, err)
	}
	snap := Snapshot{
		HostID: hostID,
		Name:   name,
		State:  state,
	}
	if exitCode != nil {
		snap.ExitCode = exitCode
	}
	// macOS does NOT surface a per-unit start time in `launchctl list`;
	// `since` stays nil. logTail is also nil — `launchctl` does not
	// emit a per-unit log stream comparable to journalctl.
	return snap, nil
}

// parseLaunchctlList extracts the State + ExitCode from a `launchctl
// list <Label>` dictionary block.
//
// Mapping:
//
//	PID present  + LastExitStatus 0   → active.
//	PID absent   + LastExitStatus 0   → inactive (clean stop or never run).
//	PID absent   + LastExitStatus != 0 → failed.
func parseLaunchctlList(out string) (State, *int, error) {
	pidPresent, lastExit, err := scanLaunchctlPairs(out)
	if err != nil {
		return "", nil, err
	}

	switch {
	case pidPresent:
		// LastExitStatus from a previous run is irrelevant once the unit
		// is running again. Surface the previous code only when the unit
		// is not currently active.
		return StateActive, nil, nil
	case lastExit != nil && *lastExit == 0:
		zero := 0
		return StateInactive, &zero, nil
	case lastExit != nil && *lastExit != 0:
		return StateFailed, lastExit, nil
	default:
		// No PID, no LastExitStatus — the unit is loaded but has never run.
		return StateInactive, nil, nil
	}
}

// scanLaunchctlPairs walks the dictionary lines and pulls out the two
// fields we care about. Tolerant of arbitrary key order, indentation,
// trailing semicolons, and quote styles.
func scanLaunchctlPairs(out string) (pidPresent bool, lastExit *int, err error) {
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
			if _, parseErr := strconv.Atoi(value); parseErr == nil {
				pidPresent = true
			}
		case "LastExitStatus":
			n, parseErr := strconv.Atoi(value)
			if parseErr != nil {
				return false, nil, fmt.Errorf("LastExitStatus %q: %w", value, parseErr)
			}
			lastExit = &n
		}
	}
	return pidPresent, lastExit, nil
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
